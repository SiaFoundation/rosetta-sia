package service

import (
	"context"
	"encoding/hex"
	"io/ioutil"
	"reflect"
	"testing"
	"time"

	"github.com/coinbase/rosetta-sdk-go/keys"
	"github.com/coinbase/rosetta-sdk-go/parser"
	rtypes "github.com/coinbase/rosetta-sdk-go/types"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/node"
	stypes "gitlab.com/NebulousLabs/Sia/types"
)

func TestDataAPI(t *testing.T) {
	testDir, err := ioutil.TempDir("", "rosetta-sia")
	if err != nil {
		t.Fatal(err)
	}
	n, errCh := node.New(node.Miner(testDir), time.Time{})
	if err = <-errCh; err != nil {
		t.Fatal(err)
	}
	masterKey := crypto.GenerateSiaKey(crypto.TypeDefaultWallet)
	if _, err = n.Wallet.Encrypt(masterKey); err != nil {
		t.Fatal(err)
	}
	if err = n.Wallet.Unlock(masterKey); err != nil {
		t.Fatal(err)
	}

	ni := &rtypes.NetworkIdentifier{
		Blockchain: "Sia",
		Network:    "Testnet",
	}
	rs, err := New(ni, n.Gateway, n.ConsensusSet, n.TransactionPool, testDir)
	if err != nil {
		t.Fatal(err)
	}

	// check network info
	ctx := context.Background()
	listResp, rerr := rs.NetworkList(ctx, &rtypes.MetadataRequest{})
	if rerr != nil {
		t.Fatal(err)
	} else if !reflect.DeepEqual(listResp.NetworkIdentifiers, []*rtypes.NetworkIdentifier{ni}) {
		t.Fatal("unexpected network list")
	}
	optionsResp, rerr := rs.NetworkOptions(ctx, &rtypes.NetworkRequest{
		NetworkIdentifier: ni,
	})
	if rerr != nil {
		t.Fatal(err)
	} else if optionsResp.Version.RosettaVersion != "1.4.0" {
		t.Fatal("unexpected Rosetta version")
	} else if !reflect.DeepEqual(optionsResp.Allow, networkAllow) {
		t.Fatal("unexpected allow")
	}
	statusResp, rerr := rs.NetworkStatus(ctx, &rtypes.NetworkRequest{
		NetworkIdentifier: ni,
	})
	if rerr != nil {
		t.Fatal(err)
	} else if statusResp.CurrentBlockIdentifier.Hash != stypes.GenesisID.String() {
		t.Error("expected current block to be genesis")
	} else if !reflect.DeepEqual(statusResp.CurrentBlockIdentifier, statusResp.GenesisBlockIdentifier) {
		t.Error("expected current block and genesis block to be the same")
	}

	// request block at tip; should be genesis
	blockResp, rerr := rs.Block(ctx, &rtypes.BlockRequest{
		BlockIdentifier: &rtypes.PartialBlockIdentifier{},
	})
	if rerr != nil {
		t.Fatal(rerr)
	} else if blockResp.Block.BlockIdentifier.Hash != stypes.GenesisID.String() {
		t.Error("expected current block to be genesis")
	} else if blockResp.Block.ParentBlockIdentifier.Hash != stypes.GenesisID.String() {
		t.Error("expected parent block to be genesis")
	} else if blockResp.Block.BlockIdentifier.Index != 0 {
		t.Error("expected current height to be to be 0, got", blockResp.Block.BlockIdentifier.Index)
	}

	// mine a block, and request tip again
	block, err := n.Miner.AddBlock()
	if err != nil {
		t.Fatal(err)
	}
	blockResp, rerr = rs.Block(ctx, &rtypes.BlockRequest{
		BlockIdentifier: &rtypes.PartialBlockIdentifier{},
	})
	if rerr != nil {
		t.Fatal(rerr)
	} else if blockResp.Block.BlockIdentifier.Hash != block.ID().String() {
		t.Error("expected current block to be last mined block")
	} else if blockResp.Block.ParentBlockIdentifier.Hash != stypes.GenesisID.String() {
		t.Error("expected parent block to be genesis")
	} else if blockResp.Block.BlockIdentifier.Index != 1 {
		t.Error("expected current height to be 1, got", blockResp.Block.BlockIdentifier.Index)
	}

	// should also be able to request by index and hash
	blockIndexResp, rerr := rs.Block(ctx, &rtypes.BlockRequest{
		BlockIdentifier: &rtypes.PartialBlockIdentifier{
			Index: &blockResp.Block.BlockIdentifier.Index,
		},
	})
	if rerr != nil {
		t.Fatal(rerr)
	} else if !reflect.DeepEqual(blockIndexResp, blockResp) {
		t.Error("index response does not match tip response")
	}
	blockHashResp, rerr := rs.Block(ctx, &rtypes.BlockRequest{
		BlockIdentifier: &rtypes.PartialBlockIdentifier{
			Hash: &blockResp.Block.BlockIdentifier.Hash,
		},
	})
	if rerr != nil {
		t.Fatal(rerr)
	} else if !reflect.DeepEqual(blockHashResp, blockResp) {
		t.Error("hash response does not match tip response")
	}

	// mine until block rewards mature, then send coins and check balance
	for i := stypes.BlockHeight(0); i <= stypes.MaturityDelay; i++ {
		if _, err := n.Miner.AddBlock(); err != nil {
			t.Fatal(err)
		}
	}
	tenSC := stypes.SiacoinPrecision.Mul64(10)
	void := stypes.UnlockHash{1, 2, 3}
	if _, err := n.Wallet.SendSiacoins(tenSC, void); err != nil {
		t.Fatal(err)
	} else if _, err := n.Miner.AddBlock(); err != nil {
		t.Fatal(err)
	}
	balanceResp, rerr := rs.AccountBalance(ctx, &rtypes.AccountBalanceRequest{
		NetworkIdentifier: ni,
		BlockIdentifier:   &rtypes.PartialBlockIdentifier{},
		AccountIdentifier: &rtypes.AccountIdentifier{
			Address: void.String(),
		},
	})
	if rerr != nil {
		t.Fatal(rerr)
	}
	balance := balanceResp.Balances[0].Value
	utxos := balanceResp.Coins
	if balance != tenSC.String() || len(utxos) != 1 || utxos[0].Amount.Value != balance {
		t.Fatal("expected 1 utxo worth 10 SC, got", balance, utxos)
	}

	// test reorg handling sending some coins, then mining a longer chain on an
	// unconnected node, then connecting them
	testDir2, err := ioutil.TempDir("", "rosetta-sia")
	if err != nil {
		t.Fatal(err)
	}
	n2, errCh := node.New(node.Miner(testDir2), time.Time{})
	if err = <-errCh; err != nil {
		t.Fatal(err)
	} else if _, err = n2.Wallet.Encrypt(masterKey); err != nil {
		t.Fatal(err)
	} else if err = n2.Wallet.Unlock(masterKey); err != nil {
		t.Fatal(err)
	}
	for i := stypes.BlockHeight(0); i <= stypes.MaturityDelay*2; i++ {
		if _, err := n2.Miner.AddBlock(); err != nil {
			t.Fatal(err)
		}
	}
	if err := n.Gateway.Connect(n2.Gateway.Address()); err != nil {
		t.Fatal(err)
	}
	// wait for sync
	for n.ConsensusSet.Height() != n2.ConsensusSet.Height() {
		time.Sleep(50 * time.Millisecond)
	}

	// tip should now be the tip of n2
	statusResp, rerr = rs.NetworkStatus(ctx, &rtypes.NetworkRequest{
		NetworkIdentifier: ni,
	})
	if rerr != nil {
		t.Fatal(err)
	} else if statusResp.CurrentBlockIdentifier.Hash != n2.ConsensusSet.CurrentBlock().ID().String() {
		t.Error("expected current block to match reorged chain")
	}
	// old chain should be inaccessible
	blockIndexResp, rerr = rs.Block(ctx, &rtypes.BlockRequest{
		BlockIdentifier: &rtypes.PartialBlockIdentifier{
			Index: &blockResp.Block.BlockIdentifier.Index,
		},
	})
	if rerr != nil {
		t.Fatal(rerr)
	} else if reflect.DeepEqual(blockIndexResp, blockResp) {
		t.Error("index response should have changed")
	}
	blockHashResp, rerr = rs.Block(ctx, &rtypes.BlockRequest{
		BlockIdentifier: &rtypes.PartialBlockIdentifier{
			Hash: &blockResp.Block.BlockIdentifier.Hash,
		},
	})
	if rerr != errUnknownBlock {
		t.Fatal("expected unknown block error, got", rerr)
	}
	// balance of void should be back to 0
	balanceResp, rerr = rs.AccountBalance(ctx, &rtypes.AccountBalanceRequest{
		NetworkIdentifier: ni,
		BlockIdentifier:   &rtypes.PartialBlockIdentifier{},
		AccountIdentifier: &rtypes.AccountIdentifier{
			Address: void.String(),
		},
	})
	if rerr != nil {
		t.Fatal(rerr)
	}
	balance = balanceResp.Balances[0].Value
	utxos = balanceResp.Coins
	if balance != "0" || len(utxos) != 0 {
		t.Fatal("expected 0 utxos, got", balance, utxos)
	}
}

func TestConstructionAPI(t *testing.T) {
	testDir, err := ioutil.TempDir("", "rosetta-sia")
	if err != nil {
		t.Fatal(err)
	}

	n, errCh := node.New(node.Miner(testDir), time.Time{})
	if err = <-errCh; err != nil {
		t.Fatal(err)
	}
	masterKey := crypto.GenerateSiaKey(crypto.TypeDefaultWallet)
	if _, err = n.Wallet.Encrypt(masterKey); err != nil {
		t.Fatal(err)
	}
	if err = n.Wallet.Unlock(masterKey); err != nil {
		t.Fatal(err)
	}
	// mine enough to get spendable coins
	for i := stypes.BlockHeight(0); i <= stypes.MaturityDelay; i++ {
		if _, err := n.Miner.AddBlock(); err != nil {
			t.Fatal(err)
		}
	}
	if bal, _, _, _ := n.Wallet.ConfirmedBalance(); bal.IsZero() {
		t.Fatal("expected non-zero balance")
	}

	ni := &rtypes.NetworkIdentifier{
		Blockchain: "Sia",
		Network:    "Testnet",
	}
	rs, err := New(ni, n.Gateway, n.ConsensusSet, n.TransactionPool, testDir)
	if err != nil {
		t.Fatal(err)
	}

	// generate keypair
	keypair, err := keys.GenerateKeypair(rtypes.Edwards25519)
	if err != nil {
		t.Fatal(err)
	}

	// derive an address
	ctx := context.Background()
	deriveResp, rerr := rs.ConstructionDerive(ctx, &rtypes.ConstructionDeriveRequest{
		NetworkIdentifier: ni,
		PublicKey:         keypair.PublicKey,
	})
	if rerr != nil {
		t.Fatal(rerr)
	}
	var addr stypes.UnlockHash
	if err := addr.LoadString(deriveResp.Address); err != nil {
		t.Fatal(err)
	}

	// send 10 SC to the address
	tenSC := stypes.SiacoinPrecision.Mul64(10)
	if _, err := n.Wallet.SendSiacoins(tenSC, addr); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Miner.AddBlock(); err != nil {
		t.Fatal(err)
	}

	// query balance
	balanceResp, rerr := rs.AccountBalance(ctx, &rtypes.AccountBalanceRequest{
		NetworkIdentifier: ni,
		BlockIdentifier:   &rtypes.PartialBlockIdentifier{},
		AccountIdentifier: &rtypes.AccountIdentifier{
			Address: addr.String(),
		},
	})
	if rerr != nil {
		t.Fatal(rerr)
	}
	balance := balanceResp.Balances[0].Value
	utxos := balanceResp.Coins
	if balance != tenSC.String() || len(utxos) != 1 || utxos[0].Amount.Value != balance {
		t.Fatal("expected 1 utxo worth 10 SC, got", balance, utxos)
	}

	// construct transaction, sending 5 SC to the void and 5 SC back to ourselves
	fiveSC := tenSC.Div64(2)
	void := stypes.UnlockHash{1, 2, 3}
	ops := []*rtypes.Operation{
		transferOp(0, stypes.SiacoinOutput{UnlockHash: addr, Value: tenSC}, stypes.SiacoinOutputID{}, false),
		transferOp(1, stypes.SiacoinOutput{UnlockHash: void, Value: fiveSC}, stypes.SiacoinOutputID{}, true),
		transferOp(2, stypes.SiacoinOutput{UnlockHash: addr, Value: fiveSC}, stypes.SiacoinOutputID{}, true),
	}
	ops[0].CoinChange.CoinIdentifier = utxos[0].CoinIdentifier
	ops[0].Metadata = map[string]interface{}{
		"public_key": hex.EncodeToString(keypair.PublicKey.Bytes),
	}

	// no-op
	preprocessResp, rerr := rs.ConstructionPreprocess(ctx, &rtypes.ConstructionPreprocessRequest{
		NetworkIdentifier: ni,
		Operations:        ops,
	})
	if rerr != nil {
		t.Fatal(rerr)
	}
	// no-op
	metadataResp, rerr := rs.ConstructionMetadata(ctx, &rtypes.ConstructionMetadataRequest{
		NetworkIdentifier: ni,
		Options:           preprocessResp.Options,
	})
	if rerr != nil {
		t.Fatal(rerr)
	}
	// get payloads to sign
	payloadsResp, rerr := rs.ConstructionPayloads(ctx, &rtypes.ConstructionPayloadsRequest{
		NetworkIdentifier: ni,
		Operations:        ops,
		Metadata:          metadataResp.Metadata,
	})
	if rerr != nil {
		t.Fatal(rerr)
	}
	// validate (unsigned)
	parseResp, rerr := rs.ConstructionParse(ctx, &rtypes.ConstructionParseRequest{
		NetworkIdentifier: ni,
		Transaction:       payloadsResp.UnsignedTransaction,
		Signed:            false,
	})
	if rerr != nil {
		t.Fatal(rerr)
	}
	if err := new(parser.Parser).ExpectedOperations(ops, parseResp.Operations, false, false); err != nil {
		t.Fatal("parsed ops do not match intended ops:", err)
	}
	// sign
	signer := keys.SignerEdwards25519{
		KeyPair: keypair,
	}
	sig, err := signer.Sign(payloadsResp.Payloads[0], rtypes.Ed25519)
	if err != nil {
		t.Fatal(err)
	}
	// add signatures
	combineResp, rerr := rs.ConstructionCombine(ctx, &rtypes.ConstructionCombineRequest{
		NetworkIdentifier:   ni,
		UnsignedTransaction: payloadsResp.UnsignedTransaction,
		Signatures:          []*rtypes.Signature{sig},
	})
	if rerr != nil {
		t.Fatal(rerr)
	}
	// validate (signed)
	parseResp, rerr = rs.ConstructionParse(ctx, &rtypes.ConstructionParseRequest{
		NetworkIdentifier: ni,
		Transaction:       combineResp.SignedTransaction,
		Signed:            true,
	})
	if rerr != nil {
		t.Fatal(rerr)
	}
	if err := new(parser.Parser).ExpectedOperations(ops, parseResp.Operations, false, false); err != nil {
		t.Fatal("parsed ops do not match intended ops:", err)
	}
	// hash
	hashResp, rerr := rs.ConstructionHash(ctx, &rtypes.ConstructionHashRequest{
		NetworkIdentifier: ni,
		SignedTransaction: combineResp.SignedTransaction,
	})
	if rerr != nil {
		t.Fatal(rerr)
	}
	// submit
	submitResp, rerr := rs.ConstructionSubmit(ctx, &rtypes.ConstructionSubmitRequest{
		NetworkIdentifier: ni,
		SignedTransaction: combineResp.SignedTransaction,
	})
	if rerr != nil {
		t.Fatal(rerr)
	}
	if hashResp.TransactionIdentifier.Hash != submitResp.TransactionIdentifier.Hash {
		t.Fatal("hash mismatch")
	}

	// transaction should be in mempool
	mempoolResp, rerr := rs.Mempool(ctx, &rtypes.NetworkRequest{
		NetworkIdentifier: ni,
	})
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !reflect.DeepEqual(mempoolResp.TransactionIdentifiers, []*rtypes.TransactionIdentifier{submitResp.TransactionIdentifier}) {
		t.Fatal("mempool should contain constructed transaction")
	}
	transactionResp, rerr := rs.MempoolTransaction(ctx, &rtypes.MempoolTransactionRequest{
		NetworkIdentifier:     ni,
		TransactionIdentifier: submitResp.TransactionIdentifier,
	})
	if rerr != nil {
		t.Fatal(rerr)
	}
	exp := &rtypes.Transaction{
		TransactionIdentifier: submitResp.TransactionIdentifier,
		Operations:            ops,
	}
	transactionResp.Transaction.Operations[0].Metadata = ops[0].Metadata
	transactionResp.Transaction.Operations[1].CoinChange.CoinIdentifier.Identifier = stypes.SiacoinOutputID{}.String()
	transactionResp.Transaction.Operations[2].CoinChange.CoinIdentifier.Identifier = stypes.SiacoinOutputID{}.String()
	if !reflect.DeepEqual(transactionResp.Transaction, exp) {
		t.Fatal("mempool transaction mismatch")
	}

	// mine a block
	if _, err := n.Miner.AddBlock(); err != nil {
		t.Fatal(err)
	}

	// transaction should no longer appear in mempool
	mempoolResp, rerr = rs.Mempool(ctx, &rtypes.NetworkRequest{
		NetworkIdentifier: ni,
	})
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(mempoolResp.TransactionIdentifiers) != 0 {
		t.Fatal("mempool should be empty")
	}
	_, rerr = rs.MempoolTransaction(ctx, &rtypes.MempoolTransactionRequest{
		NetworkIdentifier:     ni,
		TransactionIdentifier: submitResp.TransactionIdentifier,
	})
	if rerr == nil {
		t.Fatal("expected error when requesting transaction not in mempool")
	}

	// check balances
	balanceResp, rerr = rs.AccountBalance(ctx, &rtypes.AccountBalanceRequest{
		NetworkIdentifier: ni,
		BlockIdentifier:   &rtypes.PartialBlockIdentifier{},
		AccountIdentifier: &rtypes.AccountIdentifier{
			Address: addr.String(),
		},
	})
	if rerr != nil {
		t.Fatal(rerr)
	}
	balance = balanceResp.Balances[0].Value
	utxos = balanceResp.Coins
	if balance != fiveSC.String() || len(utxos) != 1 || utxos[0].Amount.Value != balance {
		t.Fatal("expected 1 utxo worth 5 SC, got", balance, utxos)
	}
	balanceResp, rerr = rs.AccountBalance(ctx, &rtypes.AccountBalanceRequest{
		NetworkIdentifier: ni,
		BlockIdentifier:   &rtypes.PartialBlockIdentifier{},
		AccountIdentifier: &rtypes.AccountIdentifier{
			Address: void.String(),
		},
	})
	if rerr != nil {
		t.Fatal(rerr)
	}
	balance = balanceResp.Balances[0].Value
	utxos = balanceResp.Coins
	if balance != fiveSC.String() || len(utxos) != 1 || utxos[0].Amount.Value != balance {
		t.Fatal("expected 1 utxo worth 5 SC, got", balance, utxos)
	}
}
