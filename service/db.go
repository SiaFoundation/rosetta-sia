package service

import (
	"bytes"
	"encoding/binary"

	rtypes "github.com/coinbase/rosetta-sdk-go/types"
	"github.com/dgraph-io/badger"
	"gitlab.com/NebulousLabs/Sia/modules"
	stypes "gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/encoding"
)

// db keys

var (
	keyVersion           = []byte("version")
	keyCurrentHeight     = []byte("currentheight")
	keyCurrentBlockID    = []byte("currentblockid")
	keyConsensusChangeID = []byte("consensuschangeid")
	keyVoidBalance       = []byte("voidbalance")
)

func keyAddress(addr stypes.UnlockHash) []byte {
	return append([]byte("addrs"), addr[:]...)
}

func keyBlockID(bid stypes.BlockID) []byte {
	return append([]byte("blocks"), bid[:]...)
}

func keyUTXO(scoid stypes.SiacoinOutputID) []byte {
	return append([]byte("utxos"), scoid[:]...)
}

func convertAmount(c stypes.Currency, positive bool) *rtypes.Amount {
	s := c.String()
	if !positive {
		s = "-" + s
	}
	return &rtypes.Amount{
		Value: s,
		Currency: &rtypes.Currency{
			Symbol:   "SC",
			Decimals: 24,
		},
	}
}

func transferOp(index int, sco stypes.SiacoinOutput, id stypes.SiacoinOutputID, credit bool) *rtypes.Operation {
	action := rtypes.CoinSpent
	typ := opTypeInput
	if credit {
		action = rtypes.CoinCreated
		typ = opTypeOutput
	}
	return &rtypes.Operation{
		OperationIdentifier: &rtypes.OperationIdentifier{
			Index: int64(index),
		},
		Type:   typ,
		Status: "Applied",
		Account: &rtypes.AccountIdentifier{
			Address: sco.UnlockHash.String(),
		},
		CoinChange: &rtypes.CoinChange{
			CoinIdentifier: &rtypes.CoinIdentifier{
				Identifier: id.String(),
			},
			CoinAction: action,
		},
		Amount: convertAmount(sco.Value, credit),
	}
}

type blockInfo struct {
	Height         int64
	DelayedOutputs []modules.DelayedSiacoinOutputDiff // from miner payouts and file contracts
}

func parseBlock(b stypes.Block, height stypes.BlockHeight, diffs modules.ConsensusChangeDiffs) blockInfo {
	var outputs []modules.DelayedSiacoinOutputDiff
	for _, dscod := range diffs.DelayedSiacoinOutputDiffs {
		if dscod.Direction == modules.DiffApply {
			outputs = append(outputs, dscod)
		}
	}
	return blockInfo{
		Height:         int64(height),
		DelayedOutputs: outputs,
	}
}

type txnHelper struct {
	txn *badger.Txn
	err error
}

func (h *txnHelper) mustGet(key []byte, v interface{}) {
	if !h.get(key, v) && h.err == nil {
		h.err = badger.ErrKeyNotFound
	}
}

func (h *txnHelper) getBytes(key []byte) (v []byte) {
	if h.err == nil {
		item, err := h.txn.Get(key)
		if err != nil {
			if err != badger.ErrKeyNotFound {
				h.err = err
			}
			return nil
		}
		v, h.err = item.ValueCopy(nil)
	}
	return
}

func (h *txnHelper) get(key []byte, v interface{}) bool {
	if h.err == nil {
		item, err := h.txn.Get(key)
		if err != nil {
			if err != badger.ErrKeyNotFound {
				h.err = err
			}
			return false
		}
		h.err = item.Value(func(val []byte) error {
			return encoding.Unmarshal(val, v)
		})
	}
	return h.err == nil
}

func (h *txnHelper) putBytes(key []byte, v []byte) {
	if h.err == nil {
		h.err = h.txn.Set(key, v)
	}
}

func (h *txnHelper) put(key []byte, v interface{}) {
	h.putBytes(key, encoding.Marshal(v))
}

func (h *txnHelper) delete(key []byte) {
	if h.err == nil {
		h.err = h.txn.Delete(key)
	}
}

func (h *txnHelper) getVersion() (v string) {
	h.get(keyVersion, &v)
	return
}

func (h *txnHelper) putVersion(v string) {
	h.put(keyVersion, v)
}

func (h *txnHelper) getCurrentHeight() (height stypes.BlockHeight) {
	h.mustGet(keyCurrentHeight, &height)
	return
}

func (h *txnHelper) putCurrentHeight(height stypes.BlockHeight) {
	h.put(keyCurrentHeight, height)
}

func (h *txnHelper) getCurrentBlockID() (bid stypes.BlockID) {
	h.mustGet(keyCurrentBlockID, &bid)
	return
}

func (h *txnHelper) putCurrentBlockID(bid stypes.BlockID) {
	h.put(keyCurrentBlockID, bid)
}

func (h *txnHelper) getConsensusChangeID() (ccid modules.ConsensusChangeID) {
	h.mustGet(keyConsensusChangeID, &ccid)
	return
}

func (h *txnHelper) putConsensusChangeID(ccid modules.ConsensusChangeID) {
	h.put(keyConsensusChangeID, ccid)
}

func (h *txnHelper) getVoidBalance() (bal stypes.Currency) {
	h.mustGet(keyVoidBalance, &bal)
	return
}

func (h *txnHelper) putVoidBalance(bal stypes.Currency) {
	h.put(keyVoidBalance, bal)
}

func (h *txnHelper) getBlockInfo(id stypes.BlockID) (info blockInfo) {
	h.mustGet(keyBlockID(id), &info)
	return
}

func (h *txnHelper) putBlockInfo(id stypes.BlockID, info blockInfo) {
	h.put(keyBlockID(id), info)
}

func (h *txnHelper) deleteBlockInfo(id stypes.BlockID) {
	h.delete(keyBlockID(id))
}

type dbUTXO struct {
	Value    stypes.Currency
	Timelock stypes.BlockHeight
}

func (h *txnHelper) getUTXO(id stypes.SiacoinOutputID) (utxo dbUTXO) {
	h.mustGet(keyUTXO(id), &utxo)
	return
}

func (h *txnHelper) putUTXO(id stypes.SiacoinOutputID, value stypes.Currency, timelock stypes.BlockHeight) {
	h.put(keyUTXO(id), dbUTXO{value, timelock})
}

// NOTE: there is no deleteUTXO; all UTXOs are kept indefinitely

func (h *txnHelper) giveUTXO(addr stypes.UnlockHash, id stypes.SiacoinOutputID, value stypes.Currency) {
	if addr == (stypes.UnlockHash{}) {
		h.putVoidBalance(h.getVoidBalance().Add(value))
		return
	}
	// because this is one of the "hottest" functions, we encode+decode manually
	utxoBytes := h.getBytes(keyAddress(addr))
	if bytes.Contains(utxoBytes, id[:]) {
		panic("attempted to give UTXO already owned by address")
	}
	// append+increment
	if len(utxoBytes) == 0 {
		utxoBytes = make([]byte, 8, 8+32) // initial length of 0
	}
	utxoBytes = append(utxoBytes, id[:]...)
	binary.LittleEndian.PutUint64(utxoBytes[:8], binary.LittleEndian.Uint64(utxoBytes[:8])+1)
	h.putBytes(keyAddress(addr), utxoBytes)
}

func (h *txnHelper) takeUTXO(addr stypes.UnlockHash, id stypes.SiacoinOutputID, value stypes.Currency) {
	if addr == (stypes.UnlockHash{}) {
		h.putVoidBalance(h.getVoidBalance().Sub(value))
		return
	}
	// because this is one of the "hottest" functions, we encode+decode manually
	utxoBytes := h.getBytes(keyAddress(addr))
	i := bytes.Index(utxoBytes, id[:])
	if i < 8 {
		panic("attempted to take UTXO not owned by address")
	} else if (i-8)%32 != 0 {
		panic("misaligned id") // should never happen
	}
	// delete+decrement
	copy(utxoBytes[i:], utxoBytes[len(utxoBytes)-32:])
	utxoBytes = utxoBytes[:len(utxoBytes)-32]
	binary.LittleEndian.PutUint64(utxoBytes[:8], binary.LittleEndian.Uint64(utxoBytes[:8])-1)
	if len(utxoBytes) == 8 {
		h.delete(keyAddress(addr))
	} else {
		h.putBytes(keyAddress(addr), utxoBytes)
	}
}
