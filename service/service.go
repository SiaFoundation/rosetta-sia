package service

import (
	"log"
	"time"

	rtypes "github.com/coinbase/rosetta-sdk-go/types"
	"github.com/dgraph-io/badger"
	"gitlab.com/NebulousLabs/Sia/modules"
	stypes "gitlab.com/NebulousLabs/Sia/types"
)

// RosettaService implements the various Rosetta Service interfaces.
type RosettaService struct {
	ni *rtypes.NetworkIdentifier
	g  modules.Gateway
	cs modules.ConsensusSet
	tp modules.TransactionPool
	db *badger.DB
}

func (rs *RosettaService) dbUpdate(fn func(h *txnHelper)) error {
	return rs.db.Update(func(txn *badger.Txn) error {
		h := &txnHelper{txn: txn}
		fn(h)
		return h.err
	})
}

func (rs *RosettaService) dbView(fn func(h *txnHelper)) error {
	return rs.db.View(func(txn *badger.Txn) error {
		h := &txnHelper{txn: txn}
		fn(h)
		return h.err
	})
}

// ProcessConsensusChange implements modules.ConsensusSetSubscriber.
func (rs *RosettaService) ProcessConsensusChange(cc modules.ConsensusChange) {
	err := rs.dbUpdate(func(h *txnHelper) {
		height := h.getCurrentHeight()
		for i, b := range cc.RevertedBlocks {
			for _, diff := range cc.RevertedDiffs[i].SiacoinOutputDiffs {
				if diff.Direction == modules.DiffApply {
					h.putUTXO(diff.ID, diff.SiacoinOutput.Value, 0)
					h.giveUTXO(diff.SiacoinOutput.UnlockHash, diff.ID, diff.SiacoinOutput.Value)
				} else {
					h.takeUTXO(diff.SiacoinOutput.UnlockHash, diff.ID, diff.SiacoinOutput.Value)
				}
			}
			for _, diff := range cc.RevertedDiffs[i].DelayedSiacoinOutputDiffs {
				if diff.Direction == modules.DiffApply {
					h.putUTXO(diff.ID, diff.SiacoinOutput.Value, diff.MaturityHeight)
					h.giveUTXO(diff.SiacoinOutput.UnlockHash, diff.ID, diff.SiacoinOutput.Value)
				} else {
					h.takeUTXO(diff.SiacoinOutput.UnlockHash, diff.ID, diff.SiacoinOutput.Value)
				}
			}

			h.deleteBlockInfo(b.ID())
			height--
		}

		for i, b := range cc.AppliedBlocks {
			for _, diff := range cc.AppliedDiffs[i].DelayedSiacoinOutputDiffs {
				// due to a consensus bug, a diff is created for the miner payout of
				// the genesis block -- despite that output never actually existing.
				// Ignore it.
				if diff.ID == stypes.GenesisBlock.MinerPayoutID(0) {
					continue
				}
				if diff.Direction == modules.DiffApply {
					h.putUTXO(diff.ID, diff.SiacoinOutput.Value, diff.MaturityHeight)
					h.giveUTXO(diff.SiacoinOutput.UnlockHash, diff.ID, diff.SiacoinOutput.Value)
				} else {
					h.takeUTXO(diff.SiacoinOutput.UnlockHash, diff.ID, diff.SiacoinOutput.Value)
				}
			}
			for _, diff := range cc.AppliedDiffs[i].SiacoinOutputDiffs {
				if diff.Direction == modules.DiffApply {
					h.putUTXO(diff.ID, diff.SiacoinOutput.Value, 0)
					h.giveUTXO(diff.SiacoinOutput.UnlockHash, diff.ID, diff.SiacoinOutput.Value)
				} else {
					h.takeUTXO(diff.SiacoinOutput.UnlockHash, diff.ID, diff.SiacoinOutput.Value)
				}
			}

			info := parseBlock(b, height, cc.AppliedDiffs[i])
			h.putBlockInfo(b.ID(), info)
			height++
		}
		h.putCurrentHeight(height)
		h.putCurrentBlockID(cc.AppliedBlocks[len(cc.AppliedBlocks)-1].ID())
		h.putConsensusChangeID(cc.ID)

		if cc.Synced {
			log.Printf("Synced at height %v (block %v)", height, cc.AppliedBlocks[len(cc.AppliedBlocks)-1].ID())
		} else if height%1000 == 0 {
			log.Printf("Still syncing (current height: %v)", height)
		}
	})
	if err != nil {
		log.Fatalln("Failed to update database:", err)
	}
}

// Close shuts down the service.
func (rs *RosettaService) Close() error {
	rs.cs.Unsubscribe(rs)
	return rs.db.Close()
}

// New constructs a RosettaService from the provided modules, storing its
// database within dir.
func New(ni *rtypes.NetworkIdentifier, g modules.Gateway, cs modules.ConsensusSet, tp modules.TransactionPool, dir string) (*RosettaService, error) {
	db, err := badger.Open(badger.DefaultOptions(dir).WithLogger(nil).WithSyncWrites(false))
	if err != nil {
		return nil, err
	}

	rs := &RosettaService{
		ni: ni,
		db: db,
		g:  g,
		cs: cs,
		tp: tp,
	}

	// initialize (if necessary) and fetch CCID
	var ccid modules.ConsensusChangeID
	err = rs.dbUpdate(func(h *txnHelper) {
		version := h.getVersion()
		if version == "" {
			log.Println("initializing db")
			h.putVersion("0.1.0")
			h.putConsensusChangeID(modules.ConsensusChangeBeginning)
			h.putCurrentHeight(0)
			h.putCurrentBlockID(stypes.GenesisID)
			h.putVoidBalance(stypes.ZeroCurrency)
		}
		ccid = h.getConsensusChangeID()
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	go gcLoop(db)
	if err := cs.ConsensusSetSubscribe(rs, ccid, nil); err != nil {
		_ = db.Close()
		return nil, err
	}

	return rs, nil
}

func gcLoop(db *badger.DB) {
	// check the db size once per minute, attempting garbage collection if the
	// db has grown by 1 GB
	_, size := db.Size()
	nextGC := size + 1e9
	for range time.Tick(time.Minute) {
		if _, size := db.Size(); size < nextGC {
			continue
		}
		err := db.RunValueLogGC(0.5)
		if err == badger.ErrRejected {
			return // db was closed
		} else if err != nil && err != badger.ErrNoRewrite {
			log.Fatalln("GC failed:", err)
		}
		nextGC += 1e9
	}
}
