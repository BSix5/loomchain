package evmaux

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	dbm "github.com/tendermint/tendermint/libs/db"
)

func TestTxHashOperation(t *testing.T) {
	txHashList1 := [][]byte{
		[]byte("hash1"),
		[]byte("hash2"),
	}
	evmAuxStore := NewEvmAuxStore(dbm.NewMemDB(), 10000)
	txHashList, err := evmAuxStore.GetTxHashList(40)
	require.NoError(t, err)
	require.Equal(t, 0, len(txHashList))
	require.NoError(t, err)
	evmAuxStore.SetTxHashList(txHashList1, 30)
	evmAuxStore.Commit()
	txHashList, err = evmAuxStore.GetTxHashList(30)
	require.NoError(t, err)
	require.Equal(t, 2, len(txHashList))
	require.Equal(t, true, bytes.Equal(txHashList1[0], txHashList1[0]))
	require.Equal(t, true, bytes.Equal(txHashList1[1], txHashList1[1]))
	evmAuxStore.ClearData()
}

func TestBloomFilterOperation(t *testing.T) {
	bf1 := []byte("bloomfilter1")
	evmAuxStore := NewEvmAuxStore(dbm.NewMemDB(), 10000)
	bf := evmAuxStore.GetBloomFilter(40)
	require.Nil(t, bf)
	evmAuxStore.SetBloomFilter(bf1, 30)
	evmAuxStore.Commit()
	bf = evmAuxStore.GetBloomFilter(30)
	require.Equal(t, true, bytes.Equal(bf, bf1))
	evmAuxStore.ClearData()
}
