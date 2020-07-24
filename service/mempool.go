package service

import (
	"context"

	rtypes "github.com/coinbase/rosetta-sdk-go/types"
	"gitlab.com/NebulousLabs/Sia/crypto"
	stypes "gitlab.com/NebulousLabs/Sia/types"
)

// Mempool implements the /mempool endpoint.
func (rs *RosettaService) Mempool(ctx context.Context, request *rtypes.NetworkRequest) (*rtypes.MempoolResponse, *rtypes.Error) {
	txns := rs.tp.Transactions()
	ids := make([]*rtypes.TransactionIdentifier, len(txns))
	for i := range ids {
		ids[i] = &rtypes.TransactionIdentifier{
			Hash: txns[i].ID().String(),
		}
	}
	return &rtypes.MempoolResponse{
		TransactionIdentifiers: ids,
	}, nil
}

// MempoolTransaction implements the /mempool/transaction endpoint.
func (rs *RosettaService) MempoolTransaction(ctx context.Context, request *rtypes.MempoolTransactionRequest) (*rtypes.MempoolTransactionResponse, *rtypes.Error) {
	var txid stypes.TransactionID
	if err := (*crypto.Hash)(&txid).LoadString(request.TransactionIdentifier.Hash); err != nil {
		return nil, errInvalidTxnID(err)
	}
	txn, _, ok := rs.tp.Transaction(txid)
	if !ok {
		return nil, errUnknownTxn
	}
	var rtxn *rtypes.Transaction
	err := rs.dbView(func(h *txnHelper) {
		rtxn = convertTransaction(h, txn)
	})
	if err != nil {
		return nil, errDatabase(err)
	}
	return &rtypes.MempoolTransactionResponse{
		Transaction: rtxn,
	}, nil
}
