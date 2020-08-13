package service

import (
	"context"

	rtypes "github.com/coinbase/rosetta-sdk-go/types"
	"github.com/dgraph-io/badger"
	stypes "gitlab.com/NebulousLabs/Sia/types"
)

func getInput(h *txnHelper, sci stypes.SiacoinInput) stypes.SiacoinOutput {
	utxo := h.getUTXO(sci.ParentID)
	return stypes.SiacoinOutput{
		UnlockHash: sci.UnlockConditions.UnlockHash(),
		Value:      utxo.Value,
	}
}

func convertTransaction(h *txnHelper, txn stypes.Transaction) *rtypes.Transaction {
	var ops []*rtypes.Operation
	for _, sci := range txn.SiacoinInputs {
		ops = append(ops, transferOp(len(ops), getInput(h, sci), sci.ParentID, false))
	}
	for i, sco := range txn.SiacoinOutputs {
		ops = append(ops, transferOp(len(ops), sco, txn.SiacoinOutputID(uint64(i)), true))
	}
	return &rtypes.Transaction{
		TransactionIdentifier: &rtypes.TransactionIdentifier{
			Hash: txn.ID().String(),
		},
		Operations: ops,
	}
}

func (rs *RosettaService) convertBlock(b stypes.Block) (*rtypes.Block, *rtypes.Error) {
	bid := b.ID()
	var info blockInfo
	var txns []*rtypes.Transaction
	err := rs.dbView(func(h *txnHelper) {
		info = h.getBlockInfo(bid)
		for _, txn := range b.Transactions {
			if rtxn := convertTransaction(h, txn); len(rtxn.Operations) > 0 {
				txns = append(txns, rtxn)
			}
		}
	})
	if err == badger.ErrKeyNotFound {
		return nil, errUnknownBlock
	} else if err != nil {
		return nil, errDatabase(err)
	}
	// add miner payouts and file contract conclusions
	//
	// NOTE: every block has at least one miner payout, so this slice is
	// guaranteed to be non-empty
	minerPayouts := make(map[stypes.SiacoinOutputID]struct{}, len(b.MinerPayouts))
	for i := range b.MinerPayouts {
		minerPayouts[b.MinerPayoutID(uint64(i))] = struct{}{}
	}
	var blockOps []*rtypes.Operation
	for _, do := range info.DelayedOutputs {
		op := transferOp(len(blockOps), do.SiacoinOutput, do.ID, true)
		if _, ok := minerPayouts[do.ID]; ok {
			op.Type = opTypeBlock
		} else {
			op.Type = opTypeContract
		}
		op.Metadata = map[string]interface{}{
			"timelock": info.Height + int64(stypes.MaturityDelay),
		}
		blockOps = append(blockOps, op)
	}
	txns = append(txns, &rtypes.Transaction{
		TransactionIdentifier: &rtypes.TransactionIdentifier{
			Hash: bid.String(),
		},
		Operations: blockOps,
	})

	rb := &rtypes.Block{
		BlockIdentifier: &rtypes.BlockIdentifier{
			Index: info.Height,
			Hash:  bid.String(),
		},
		ParentBlockIdentifier: &rtypes.BlockIdentifier{
			Index: info.Height - 1,
			Hash:  b.ParentID.String(),
		},
		Timestamp:    int64(b.Timestamp) * 1000,
		Transactions: txns,
	}
	if info.Height == 0 {
		rb.ParentBlockIdentifier = genesisIdentifier
	}
	return rb, nil
}

// Block implements the /block endpoint.
func (rs *RosettaService) Block(ctx context.Context, request *rtypes.BlockRequest) (*rtypes.BlockResponse, *rtypes.Error) {
	var block *rtypes.Block
	var err *rtypes.Error
	switch {
	case request.BlockIdentifier.Index != nil:
		b, ok := rs.cs.BlockAtHeight(stypes.BlockHeight(*request.BlockIdentifier.Index))
		if !ok {
			return nil, errUnknownBlock
		}
		block, err = rs.convertBlock(b)
		// sanity check
		if err == nil && block.BlockIdentifier.Index != *request.BlockIdentifier.Index {
			panic("block height mismatch")
		}

	case request.BlockIdentifier.Hash != nil:
		var bid stypes.BlockID
		if err := bid.LoadString(*request.BlockIdentifier.Hash); err != nil {
			return nil, errInvalidBlockID(err)
		}
		b, _, ok := rs.cs.BlockByID(bid)
		if !ok {
			return nil, errUnknownBlock
		}
		block, err = rs.convertBlock(b)
		// sanity check
		if err == nil && block.BlockIdentifier.Hash != *request.BlockIdentifier.Hash {
			panic("block hash mismatch")
		}

	default:
		block, err = rs.convertBlock(rs.cs.CurrentBlock())
	}

	return &rtypes.BlockResponse{
		Block: block,
	}, err
}

// BlockTransaction implements the /block/transaction endpoint.
func (rs *RosettaService) BlockTransaction(ctx context.Context, request *rtypes.BlockTransactionRequest) (*rtypes.BlockTransactionResponse, *rtypes.Error) {
	// all transactions are returned from /block
	return nil, errNotImplemented
}
