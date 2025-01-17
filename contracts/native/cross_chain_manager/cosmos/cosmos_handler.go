/*
 * Copyright (C) 2020 The poly network Authors
 * This file is part of The poly network library.
 *
 * The  poly network  is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Lesser General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * The  poly network  is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Lesser General Public License for more details.
 * You should have received a copy of the GNU Lesser General Public License
 * along with The poly network .  If not, see <http://www.gnu.org/licenses/>.
 */

package cosmos

import (
	"encoding/json"
	"fmt"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/store/rootmulti"
	"github.com/ethereum/go-ethereum/contracts/native"
	scom "github.com/ethereum/go-ethereum/contracts/native/cross_chain_manager/common"
	common2 "github.com/ethereum/go-ethereum/contracts/native/info_sync"
	"github.com/ethereum/go-ethereum/contracts/native/utils"
	tm34types "github.com/switcheo/tendermint/types"
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/ed25519"
	"github.com/tendermint/tendermint/crypto/merkle"
	"github.com/tendermint/tendermint/crypto/multisig"
	"github.com/tendermint/tendermint/crypto/secp256k1"
)

type CosmosHandler struct{}

func NewCosmosHandler() *CosmosHandler {
	return &CosmosHandler{}
}

type CosmosProofValue struct {
	Kp    string
	Value []byte
}

func newCDC() *codec.Codec {
	cdc := codec.New()
	cdc.RegisterInterface((*crypto.PubKey)(nil), nil)
	cdc.RegisterConcrete(ed25519.PubKeyEd25519{}, ed25519.PubKeyAminoName, nil)
	cdc.RegisterConcrete(secp256k1.PubKeySecp256k1{}, secp256k1.PubKeyAminoName, nil)
	cdc.RegisterConcrete(multisig.PubKeyMultisigThreshold{}, multisig.PubKeyMultisigThresholdAminoRoute, nil)

	cdc.RegisterInterface((*crypto.PrivKey)(nil), nil)
	cdc.RegisterConcrete(ed25519.PrivKeyEd25519{}, ed25519.PrivKeyAminoName, nil)
	cdc.RegisterConcrete(secp256k1.PrivKeySecp256k1{}, secp256k1.PrivKeyAminoName, nil)
	return cdc
}

func (this *CosmosHandler) MakeDepositProposal(service *native.NativeContract) (*scom.MakeTxParam, error) {
	ctx := service.ContractRef().CurrentContext()
	params := &scom.EntranceParam{}
	if err := utils.UnpackMethod(scom.ABI, scom.MethodImportOuterTransfer, params, ctx.Payload); err != nil {
		return nil, err
	}

	value, err := common2.GetRootInfo(service, params.SourceChainID, params.Height)
	if err != nil {
		return nil, fmt.Errorf("Cosmos MakeDepositProposal, GetRootInfo error:%s", err)
	}
	header := &tm34types.Header{}
	err = json.Unmarshal(value, header)
	if err != nil {
		return nil, fmt.Errorf("verifyFromEthTx, json unmarshal header error:%s", err)
	}

	cdc := newCDC()
	var proofValue CosmosProofValue
	if err = cdc.UnmarshalBinaryBare(params.Extra, &proofValue); err != nil {
		return nil, fmt.Errorf("Cosmos MakeDepositProposal, unmarshal proof value err: %v", err)
	}
	var proof merkle.Proof
	err = cdc.UnmarshalBinaryBare(params.Proof, &proof)
	if err != nil {
		return nil, fmt.Errorf("Cosmos MakeDepositProposal, unmarshal proof err: %v", err)
	}
	if len(proofValue.Kp) != 0 {
		prt := rootmulti.DefaultProofRuntime()
		err = prt.VerifyValue(&proof, header.AppHash, proofValue.Kp, proofValue.Value)
		if err != nil {
			return nil, fmt.Errorf("Cosmos MakeDepositProposal, proof error: %s", err)
		}
	} else {
		prt := rootmulti.DefaultProofRuntime()
		err = prt.VerifyAbsence(&proof, header.AppHash, string(proofValue.Value))
		if err != nil {
			return nil, fmt.Errorf("Cosmos MakeDepositProposal, proof error: %s", err)
		}
	}
	txParam, err := scom.DecodeTxParam(proofValue.Value)
	if err != nil {
		return nil, fmt.Errorf("Cosmos MakeDepositProposal, deserialize merkleValue error:%s", err)
	}
	if err := scom.CheckDoneTx(service, txParam.CrossChainID, params.SourceChainID); err != nil {
		return nil, fmt.Errorf("Cosmos MakeDepositProposal, check done transaction error:%s", err)
	}
	if err := scom.PutDoneTx(service, txParam.CrossChainID, params.SourceChainID); err != nil {
		return nil, fmt.Errorf("Cosmos MakeDepositProposal, PutDoneTx error:%s", err)
	}
	return txParam, nil
}
