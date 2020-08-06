package service

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	rtypes "github.com/coinbase/rosetta-sdk-go/types"
	"gitlab.com/NebulousLabs/Sia/crypto"
	stypes "gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/encoding"
)

type constructionTxn struct {
	stypes.Transaction
	InputParents []stypes.SiacoinOutput
}

func (ct constructionTxn) MarshalSia(w io.Writer) error {
	return encoding.NewEncoder(w).EncodeAll(ct.Transaction, ct.InputParents)
}

func (ct *constructionTxn) UnmarshalSia(r io.Reader) error {
	return encoding.NewDecoder(r, encoding.DefaultAllocLimit).DecodeAll(&ct.Transaction, &ct.InputParents)
}

func decodeTxn(b64 string) (txn constructionTxn, err error) {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err == nil {
		err = encoding.Unmarshal(b, &txn)
	}
	return
}

// ConstructionCombine implements the /construction/combine endpoint.
func (rs *RosettaService) ConstructionCombine(ctx context.Context, request *rtypes.ConstructionCombineRequest) (*rtypes.ConstructionCombineResponse, *rtypes.Error) {
	txn, err := decodeTxn(request.UnsignedTransaction)
	if err != nil {
		return nil, errInvalidTxn(err)
	}
	for _, sig := range request.Signatures {
		// not the most efficient strategy, but the best we can do for now
		var sigHash crypto.Hash
		copy(sigHash[:], sig.SigningPayload.Bytes)
		for sigIndex := range txn.TransactionSignatures {
			if txn.SigHash(sigIndex, stypes.ASICHardforkHeight+1) == sigHash {
				txn.TransactionSignatures[sigIndex].Signature = sig.Bytes
				break
			}
		}
	}
	return &rtypes.ConstructionCombineResponse{
		SignedTransaction: base64.StdEncoding.EncodeToString(encoding.Marshal(txn)),
	}, nil
}

// ConstructionDerive implements the /construction/derive endpoint.
func (rs *RosettaService) ConstructionDerive(ctx context.Context, request *rtypes.ConstructionDeriveRequest) (*rtypes.ConstructionDeriveResponse, *rtypes.Error) {
	if request.PublicKey.CurveType != rtypes.Edwards25519 {
		return nil, errUnsupportedCurve
	}
	return &rtypes.ConstructionDeriveResponse{
		Address: stypes.UnlockConditions{
			PublicKeys: []stypes.SiaPublicKey{{
				Algorithm: stypes.SignatureEd25519,
				Key:       request.PublicKey.Bytes,
			}},
			SignaturesRequired: 1,
			Timelock:           0,
		}.UnlockHash().String(),
	}, nil
}

// ConstructionHash implements the /construction/hash endpoint.
func (rs *RosettaService) ConstructionHash(ctx context.Context, request *rtypes.ConstructionHashRequest) (*rtypes.TransactionIdentifierResponse, *rtypes.Error) {
	txn, err := decodeTxn(request.SignedTransaction)
	if err != nil {
		return nil, errInvalidTxn(err)
	}
	return &rtypes.TransactionIdentifierResponse{
		TransactionIdentifier: &rtypes.TransactionIdentifier{
			Hash: txn.ID().String(),
		},
	}, nil
}

// ConstructionMetadata implements the /construction/metadata endpoint.
func (rs *RosettaService) ConstructionMetadata(ctx context.Context, request *rtypes.ConstructionMetadataRequest) (*rtypes.ConstructionMetadataResponse, *rtypes.Error) {
	return &rtypes.ConstructionMetadataResponse{}, nil
}

// ConstructionParse implements the /construction/parse endpoint.
func (rs *RosettaService) ConstructionParse(ctx context.Context, request *rtypes.ConstructionParseRequest) (*rtypes.ConstructionParseResponse, *rtypes.Error) {
	txn, err := decodeTxn(request.Transaction)
	if err != nil {
		return nil, errInvalidTxn(err)
	}

	var ops []*rtypes.Operation
	for i, sci := range txn.SiacoinInputs {
		ops = append(ops, transferOp(len(ops), txn.InputParents[i], sci.ParentID, false))
	}
	for i, sco := range txn.SiacoinOutputs {
		ops = append(ops, transferOp(len(ops), sco, txn.SiacoinOutputID(uint64(i)), true))
	}
	var signers []string
	for _, sig := range txn.TransactionSignatures {
		for _, in := range txn.SiacoinInputs {
			if in.ParentID == stypes.SiacoinOutputID(sig.ParentID) {
				signers = append(signers, hex.EncodeToString(in.UnlockConditions.PublicKeys[sig.PublicKeyIndex].Key))
				break
			}
		}
	}

	return &rtypes.ConstructionParseResponse{
		Operations: ops,
		Signers:    signers,
	}, nil
}

// ConstructionPayloads implements the /construction/payloads endpoint. The
// request must include an extra metadata field for each "input" operation:
//
//   public_key        (hex-encoded ed25519 pubkey of the operation's address)
//
func (rs *RosettaService) ConstructionPayloads(ctx context.Context, request *rtypes.ConstructionPayloadsRequest) (*rtypes.ConstructionPayloadsResponse, *rtypes.Error) {
	var txn constructionTxn
	var payloads []*rtypes.SigningPayload
	for _, op := range request.Operations {
		if strings.HasPrefix(op.Amount.Value, "-") {
			var parentID stypes.SiacoinOutputID
			err := (*crypto.Hash)(&parentID).LoadString(op.CoinChange.CoinIdentifier.Identifier)
			if err != nil {
				return nil, errInvalidUnlockConditions(err)
			}
			key, err := hex.DecodeString(op.Metadata["public_key"].(string))
			if err != nil {
				return nil, errInvalidUnlockConditions(err)
			}
			uc := stypes.UnlockConditions{
				PublicKeys: []stypes.SiaPublicKey{{
					Algorithm: stypes.SignatureEd25519,
					Key:       key,
				}},
				SignaturesRequired: 1,
				Timelock:           0,
			}
			// add input + sig
			txn.SiacoinInputs = append(txn.SiacoinInputs, stypes.SiacoinInput{
				ParentID:         parentID,
				UnlockConditions: uc,
			})
			txn.TransactionSignatures = append(txn.TransactionSignatures, stypes.TransactionSignature{
				ParentID:       crypto.Hash(parentID),
				PublicKeyIndex: 0, // TODO: this assumes standard UnlockConditions
				Timelock:       0,
				CoveredFields:  stypes.FullCoveredFields,
			})
			payloads = append(payloads, &rtypes.SigningPayload{
				Address:       op.Account.Address,
				Bytes:         nil, // to be supplied later
				SignatureType: rtypes.Ed25519,
			})
			// add InputParent metadata
			var parent stypes.SiacoinOutput
			err = parent.UnlockHash.LoadString(op.Account.Address)
			if err != nil {
				return nil, errInvalidAddress(err)
			}
			_, err = fmt.Sscan(op.Amount.Value[1:], &parent.Value)
			if err != nil {
				return nil, errInvalidAmount(err)
			}
			txn.InputParents = append(txn.InputParents, parent)
		} else {
			// add output
			var addr stypes.UnlockHash
			err := addr.LoadString(op.Account.Address)
			if err != nil {
				return nil, errInvalidAddress(err)
			}
			var value stypes.Currency
			_, err = fmt.Sscan(op.Amount.Value, &value)
			if err != nil {
				return nil, errInvalidAmount(err)
			}
			txn.SiacoinOutputs = append(txn.SiacoinOutputs, stypes.SiacoinOutput{
				UnlockHash: addr,
				Value:      value,
			})
		}
	}
	// compute signing payloads (this must be done after the transaction is fully constructed)
	for i := range txn.TransactionSignatures {
		sigHash := txn.SigHash(i, stypes.ASICHardforkHeight+1)
		payloads[i].Bytes = sigHash[:]
	}
	return &rtypes.ConstructionPayloadsResponse{
		UnsignedTransaction: base64.StdEncoding.EncodeToString(encoding.Marshal(txn)),
		Payloads:            payloads,
	}, nil
}

// ConstructionPreprocess implements the /construction/preprocess endpoint.
func (rs *RosettaService) ConstructionPreprocess(ctx context.Context, request *rtypes.ConstructionPreprocessRequest) (*rtypes.ConstructionPreprocessResponse, *rtypes.Error) {
	// maybe return txnsig payloads?
	return &rtypes.ConstructionPreprocessResponse{}, nil
}

// ConstructionSubmit implements the /construction/submit endpoint.
func (rs *RosettaService) ConstructionSubmit(ctx context.Context, request *rtypes.ConstructionSubmitRequest) (*rtypes.TransactionIdentifierResponse, *rtypes.Error) {
	txn, err := decodeTxn(request.SignedTransaction)
	if err != nil {
		return nil, errInvalidTxn(err)
	} else if err := rs.tp.AcceptTransactionSet([]stypes.Transaction{txn.Transaction}); err != nil {
		return nil, errTxnNotAccepted(err)
	}
	return &rtypes.TransactionIdentifierResponse{
		TransactionIdentifier: &rtypes.TransactionIdentifier{
			Hash: txn.ID().String(),
		},
	}, nil
}
