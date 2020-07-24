package service

import (
	"context"

	rtypes "github.com/coinbase/rosetta-sdk-go/types"
	stypes "gitlab.com/NebulousLabs/Sia/types"
)

type balanceUTXO struct {
	ID       string             `json:"id"`
	Value    string             `json:"value"`
	Timelock stypes.BlockHeight `json:"timelock"`
}

func (rs *RosettaService) balance(addr stypes.UnlockHash) (*rtypes.Amount, []balanceUTXO, *rtypes.BlockIdentifier, *rtypes.Error) {
	var balance stypes.Currency
	var utxos []balanceUTXO
	var height stypes.BlockHeight
	var bid stypes.BlockID
	err := rs.dbView(func(h *txnHelper) {
		if addr == (stypes.UnlockHash{}) {
			balance = h.getVoidBalance()
			return
		}
		var ids []stypes.SiacoinOutputID
		h.get(keyAddress(addr), &ids)
		for _, id := range ids {
			utxo := h.getUTXO(id)
			balance = balance.Add(utxo.Value)
			utxos = append(utxos, balanceUTXO{
				ID:       id.String(),
				Value:    utxo.Value.String(),
				Timelock: utxo.Timelock,
			})
		}
		height = h.getCurrentHeight()
		bid = h.getCurrentBlockID()
	})
	if err != nil {
		return nil, nil, nil, errDatabase(err)
	}
	return convertAmount(balance, true), utxos, &rtypes.BlockIdentifier{
		Index: int64(height),
		Hash:  bid.String(),
	}, nil
}

// AccountBalance implements the /account/balance endpoint.
func (rs *RosettaService) AccountBalance(ctx context.Context, request *rtypes.AccountBalanceRequest) (*rtypes.AccountBalanceResponse, *rtypes.Error) {
	var uh stypes.UnlockHash
	if err := uh.LoadString(request.AccountIdentifier.Address); err != nil {
		return nil, errInvalidAddress(err)
	}

	balance, utxos, bi, err := rs.balance(uh)
	if err != nil {
		return nil, err
	}

	return &rtypes.AccountBalanceResponse{
		BlockIdentifier: bi,
		Balances:        []*rtypes.Amount{balance},
		Metadata: map[string]interface{}{
			"utxos": utxos,
		},
	}, nil
}
