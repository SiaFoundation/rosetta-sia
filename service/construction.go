package service

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	rtypes "github.com/coinbase/rosetta-sdk-go/types"
	"gitlab.com/NebulousLabs/Sia/crypto"
	stypes "gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/encoding"
)

func decodeTxn(b64 string) (txn stypes.Transaction, err error) {
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
func (rs *RosettaService) ConstructionHash(ctx context.Context, request *rtypes.ConstructionHashRequest) (*rtypes.ConstructionHashResponse, *rtypes.Error) {
	txn, err := decodeTxn(request.SignedTransaction)
	if err != nil {
		return nil, errInvalidTxn(err)
	}
	return &rtypes.ConstructionHashResponse{
		TransactionHash: txn.ID().String(),
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
	err = rs.dbView(func(h *txnHelper) {
		ops = convertTransaction(h, txn).Operations
	})
	if err != nil {
		return nil, errDatabase(err)
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
// request must include two extra metadata fields for each "input" operation:
//
//   parent_identifier    (the SiacoinOutputID of the input)
//   public_key           (the ed25519 pubkey of the operation's address)
//
// Both fields should be provided as hex-encoded strings.
func (rs *RosettaService) ConstructionPayloads(ctx context.Context, request *rtypes.ConstructionPayloadsRequest) (*rtypes.ConstructionPayloadsResponse, *rtypes.Error) {
	var txn stypes.Transaction
	var payloads []*rtypes.SigningPayload
	for _, op := range request.Operations {
		if strings.HasPrefix(op.Amount.Value, "-") {
			var parentID stypes.SiacoinOutputID
			hex.Decode(parentID[:], []byte(op.Metadata["parent_identifier"].(string)))
			key, _ := hex.DecodeString(op.Metadata["public_key"].(string))
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
				PublicKeyIndex: 0,
				Timelock:       0,
				CoveredFields:  stypes.FullCoveredFields,
			})
			payloads = append(payloads, &rtypes.SigningPayload{
				Address:       op.Account.Address,
				Bytes:         nil, // to be supplied later
				SignatureType: rtypes.Ed25519,
			})
		} else {
			// add output
			var addr stypes.UnlockHash
			addr.LoadString(op.Account.Address)
			var value stypes.Currency
			fmt.Sscan(op.Amount.Value, &value)
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
func (rs *RosettaService) ConstructionSubmit(ctx context.Context, request *rtypes.ConstructionSubmitRequest) (*rtypes.ConstructionSubmitResponse, *rtypes.Error) {
	txn, err := decodeTxn(request.SignedTransaction)
	if err != nil {
		return nil, errInvalidTxn(err)
	} else if err := rs.tp.AcceptTransactionSet([]stypes.Transaction{txn}); err != nil {
		return nil, errTxnNotAccepted(err)
	}
	return &rtypes.ConstructionSubmitResponse{
		TransactionIdentifier: &rtypes.TransactionIdentifier{
			Hash: txn.ID().String(),
		},
	}, nil
}
