package main

import (
	"strings"
	"testing"

	proto "github.com/gogo/protobuf/proto"
	loom "github.com/loomnetwork/go-loom"
	lauth "github.com/loomnetwork/go-loom/auth"
	"github.com/loomnetwork/go-loom/types"
	"github.com/loomnetwork/loomchain"
	"github.com/loomnetwork/loomchain/auth"
	"github.com/loomnetwork/loomchain/store"
	"github.com/loomnetwork/loomchain/vm"
	"github.com/stretchr/testify/require"
	abci "github.com/tendermint/abci/types"
	"golang.org/x/crypto/ed25519"
)

// Tx handlers must not process txs in which the caller doesn't match the signer.
func TestTxHandlerWithInvalidCaller(t *testing.T) {
	_, alicePrivKey, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	bobPubKey, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	vmManager := vm.NewManager()
	router := loomchain.NewTxRouter()
	router.Handle(1, &vm.DeployTxHandler{Manager: vmManager})
	router.Handle(2, &vm.CallTxHandler{Manager: vmManager})

	txMiddleWare := []loomchain.TxMiddleware{
		auth.SignatureTxMiddleware,
		auth.NonceTxMiddleware,
	}

	state := loomchain.NewStoreState(nil, store.NewMemStore(), abci.Header{ChainID: "default"})
	rootHandler := loomchain.MiddlewareTxHandler(txMiddleWare, router, nil)
	signer := lauth.NewEd25519Signer(alicePrivKey)
	caller := loom.Address{
		ChainID: "default",
		Local:   loom.LocalAddressFromPublicKey(bobPubKey),
	}

	// Try to process txs in which Alice attempts to impersonate Bob
	_, err = rootHandler.ProcessTx(state, createTxWithInvalidCaller(t, signer, caller, &vm.DeployTx{
		VmType: vm.VMType_PLUGIN,
		Code:   nil,
		Name:   "hello",
	}, 1, 1))
	require.Error(t, err)
	require.True(t, strings.HasPrefix(err.Error(), "Origin doesn't match caller"))

	_, err = rootHandler.ProcessTx(state, createTxWithInvalidCaller(t, signer, caller, &vm.CallTx{
		VmType: vm.VMType_PLUGIN,
	}, 2, 2))
	require.Error(t, err)
	require.True(t, strings.HasPrefix(err.Error(), "Origin doesn't match caller"))
}

func createTxWithInvalidCaller(t *testing.T, signer lauth.Signer, caller loom.Address,
	tx proto.Message, txType uint32, nonce uint64) []byte {
	payload, err := proto.Marshal(tx)
	require.NoError(t, err)

	msgBytes, err := proto.Marshal(&vm.MessageTx{
		From: caller.MarshalPB(),
		To:   loom.RootAddress("default").MarshalPB(),
		Data: payload,
	})
	require.NoError(t, err)

	txBytes, err := proto.Marshal(&types.Transaction{
		Id:   txType,
		Data: msgBytes,
	})
	require.NoError(t, err)

	nonceTxBytes, err := proto.Marshal(&auth.NonceTx{
		Inner:    txBytes,
		Sequence: nonce,
	})
	require.NoError(t, err)

	signedTx := lauth.SignTx(signer, nonceTxBytes)
	signedTxBytes, err := proto.Marshal(signedTx)
	require.NoError(t, err)
	return signedTxBytes
}
