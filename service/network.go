package service

import (
	"context"

	rtypes "github.com/coinbase/rosetta-sdk-go/types"
	stypes "gitlab.com/NebulousLabs/Sia/types"
)

// errors

func errorFn(code int32, retriable bool, msg string) func(error) *rtypes.Error {
	return func(err error) *rtypes.Error {
		if err != nil {
			msg += ": " + err.Error()
		}
		return &rtypes.Error{
			Code:      code,
			Message:   msg,
			Retriable: retriable,
		}
	}
}

var (
	errNotImplemented   = errorFn(0, false, "not implemented")(nil)
	errDatabase         = errorFn(1, false, "database error")
	errInvalidAddress   = errorFn(2, false, "invalid address")
	errInvalidBlockID   = errorFn(3, false, "invalid block ID")
	errInvalidTxnID     = errorFn(4, false, "invalid transaction ID")
	errInvalidTxn       = errorFn(5, false, "invalid transaction")
	errUnsupportedCurve = errorFn(6, false, "unsupported curve")(nil)
	errUnknownBlock     = errorFn(7, true, "unknown block")(nil)
	errUnknownTxn       = errorFn(8, true, "unknown transaction")(nil)
	errTxnNotAccepted   = errorFn(9, true, "transaction not accepted")
)

var networkAllow = &rtypes.Allow{
	OperationStatuses: []*rtypes.OperationStatus{
		{
			Status:     "Applied",
			Successful: true,
		},
	},
	OperationTypes: []string{
		"Transfer",
	},
	Errors: []*rtypes.Error{
		errNotImplemented,
		errDatabase(nil),
		errInvalidAddress(nil),
		errInvalidBlockID(nil),
		errInvalidTxnID(nil),
		errInvalidTxn(nil),
		errUnsupportedCurve,
		errUnknownBlock,
		errUnknownTxn,
		errTxnNotAccepted(nil),
	},
}

var genesisIdentifier = &rtypes.BlockIdentifier{
	Index: 0,
	Hash:  stypes.GenesisID.String(),
}

// NetworkList implements the /network/list endpoint.
func (rs *RosettaService) NetworkList(ctx context.Context, request *rtypes.MetadataRequest) (*rtypes.NetworkListResponse, *rtypes.Error) {
	return &rtypes.NetworkListResponse{
		NetworkIdentifiers: []*rtypes.NetworkIdentifier{rs.ni},
	}, nil
}

// NetworkStatus implements the /network/status endpoint.
func (rs *RosettaService) NetworkStatus(ctx context.Context, request *rtypes.NetworkRequest) (*rtypes.NetworkStatusResponse, *rtypes.Error) {
	b, err := rs.convertBlock(rs.cs.CurrentBlock())
	if err != nil {
		return nil, err
	}
	var peers []*rtypes.Peer
	for _, p := range rs.g.Peers() {
		peers = append(peers, &rtypes.Peer{
			PeerID: string(p.NetAddress),
		})
	}
	return &rtypes.NetworkStatusResponse{
		CurrentBlockIdentifier: b.BlockIdentifier,
		CurrentBlockTimestamp:  b.Timestamp,
		GenesisBlockIdentifier: genesisIdentifier,
		Peers:                  peers,
	}, nil
}

// NetworkOptions implements the /network/options endpoint.
func (rs *RosettaService) NetworkOptions(ctx context.Context, request *rtypes.NetworkRequest) (*rtypes.NetworkOptionsResponse, *rtypes.Error) {
	return &rtypes.NetworkOptionsResponse{
		Version: &rtypes.Version{
			RosettaVersion: "1.4.0",
			NodeVersion:    "1.4.11",
		},
		Allow: networkAllow,
	}, nil
}
