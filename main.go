package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

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
	rs, shutdown, err := startNode(n, *dir, *rpcAddr)
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
	srv := &http.Server{
		Addr:    *serverAddr,
		Handler: router,
	}

	// install signal handler
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt)
		<-sigChan
		fmt.Println("\rReceived interrupt, shutting down...")
		srv.Shutdown(context.Background())
	}()

	log.Println("Listening on port", *serverAddr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Println("ListenAndServe:", err)
	}
	if err := shutdown(); err != nil {
		log.Println("WARN: error shutting down modules:", err)
	}
	if err := rs.Close(); err != nil {
		log.Println("WARN: error shutting down service:", err)
	}
}

func startNode(network *rtypes.NetworkIdentifier, dir string, rpcAddr string) (*service.RosettaService, func() error, error) {
	g, err := gateway.New(rpcAddr, true, filepath.Join(dir, "gateway"))
	if err != nil {
		return nil, nil, err
	}
	cs, errChan := consensus.New(g, true, filepath.Join(dir, "consensus"))
	err = handleAsyncErr(errChan)
	if err != nil {
		return nil, nil, err
	}
	tp, err := transactionpool.New(cs, g, filepath.Join(dir, "tpool"))
	if err != nil {
		return nil, nil, err
	}

	rs, err := service.New(network, g, cs, tp, filepath.Join(dir, "db"))
	if err != nil {
		return nil, nil, err
	}

	shutdown := func() error {
		var errs []string
		if err := tp.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("transaction pool: %v", err))
		}
		if err := cs.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("consensus set: %v", err))
		}
		if err := g.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("gateway: %v", err))
		}
		if len(errs) > 0 {
			return errors.New(strings.Join(errs, "; "))
		}
		return nil
	}

	return rs, shutdown, nil
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
