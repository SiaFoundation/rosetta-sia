package main

import (
	"flag"
	"log"
	"net/http"
	"path/filepath"

	"github.com/coinbase/rosetta-sdk-go/asserter"
	"github.com/coinbase/rosetta-sdk-go/server"
	rtypes "github.com/coinbase/rosetta-sdk-go/types"
	"gitlab.com/NebulousLabs/Sia/modules/consensus"
	"gitlab.com/NebulousLabs/Sia/modules/gateway"
	"gitlab.com/NebulousLabs/Sia/modules/transactionpool"
	"gitlab.com/NebulousLabs/rosetta-sia/service"
)

func main() {
	serverAddr := flag.String("a", ":8080", "address that the API listens on")
	rpcAddr := flag.String("rpc-addr", ":9381", "address that the gateway listens on")
	dir := flag.String("d", "data", "directory where node state is stored")
	flag.Parse()

	n := &rtypes.NetworkIdentifier{
		Blockchain: "Sia",
		Network:    "Mainnet",
	}
	rs, err := startNode(n, *dir, *rpcAddr)
	if err != nil {
		log.Fatal(err)
	}
	supportedOps := []string{"Transfer"}
	historicalBalanceLookup := false
	a, err := asserter.NewServer(supportedOps, historicalBalanceLookup, []*rtypes.NetworkIdentifier{n})
	if err != nil {
		log.Fatal(err)
	}
	router := server.NewRouter(
		server.NewNetworkAPIController(rs, a),
		server.NewBlockAPIController(rs, a),
		server.NewMempoolAPIController(rs, a),
		server.NewAccountAPIController(rs, a),
		server.NewConstructionAPIController(rs, a),
	)
	log.Println("Listening on port", *serverAddr)
	log.Fatal(http.ListenAndServe(*serverAddr, router))
}

func startNode(network *rtypes.NetworkIdentifier, dir string, rpcAddr string) (*service.RosettaService, error) {
	g, err := gateway.New(rpcAddr, true, filepath.Join(dir, "gateway"))
	if err != nil {
		return nil, err
	}
	cs, errChan := consensus.New(g, true, filepath.Join(dir, "consensus"))
	err = handleAsyncErr(errChan)
	if err != nil {
		return nil, err
	}
	tp, err := transactionpool.New(cs, g, filepath.Join(dir, "tpool"))
	if err != nil {
		return nil, err
	}
	return service.New(network, g, cs, tp, filepath.Join(dir, "db"))
}

func handleAsyncErr(errCh <-chan error) error {
	select {
	case err := <-errCh:
		return err
	default:
	}
	go func() {
		err := <-errCh
		if err != nil {
			log.Println("WARNING: consensus initialization returned an error:", err)
		}
	}()
	return nil
}
