package store

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
	"github.com/tendermint/iavl"
	"github.com/tendermint/tendermint/libs/db"

	// "github.com/syndtr/goleveldb/leveldb"

	"github.com/loomnetwork/loomchain/log"
)

const (
	minCache = 20
	maxCache = 500
)

var (
	testno = int(0)
)

type benchFunc func(b require.TestingT)

func TestMain(m *testing.M) {
	log.Setup("debug", "file://-")
	log.Root.With("module", "diff-iavlstore")
	testno = 0
	os.RemoveAll("testdata")
	_, err := os.Stat("testdata")
	os.IsNotExist(err)

	diskDbType = "goleveldb"
	//diskDbType = "membd"
	numBlocks = 10
	blockSize = 10
	saveFrequency = 100
	versionFrequency = 20
	maxVersions = 2

	err = generateBlocks2()
	if err != nil {
		return
	}

	m.Run()

	files, err := ioutil.ReadDir("./testdata")
	for _, f := range files {
		if !f.IsDir() {
			continue
		}

		size, err := DirSize2(f)
		if err != nil {
			continue
		}
		fmt.Println()
		fmt.Println("size of "+f.Name()+" : ", size)

		ldb, err := leveldb.OpenFile("./testdata/"+f.Name(), nil)
		err = ldb.CompactRange(util.Range{nil, nil})
		if err != nil {
			fmt.Println("error compating", f.Name(), err)
		}
		err = ldb.Close()
		if err != nil {
			fmt.Println("error closing", f.Name(), err)
		}

		size, err = DirSize2(f)
		fmt.Println("after compact size of "+f.Name()+" : ", size)
	}
}
func TestNormal(t *testing.T) {
	//t.Skip()
	testIavlStore(t, "normal", benchmarkNormal)
}
func TestNormalDif(t *testing.T) {
	//t.Skip()
	testIavlStore(t, "normal-dif", benchmarkNormalDif)
}
func TestMaxVersionsDif(t *testing.T) {
	//t.Skip()
	testIavlStore(t, "maxVersions-dif", benchmarkMaxVersionsDif)
}
func TestMaxVersions(t *testing.T) {
	//t.Skip()
	testIavlStore(t, "maxVersions", benchmarkMaxVersions)
}
func TestVarableCacheDif(t *testing.T) {
	//t.Skip()
	testIavlStore(t, "VarableCache-dif", benchmarkVarableCacheDif)
}
func TestVarableCache(t *testing.T) {
	//t.Skip()
	testIavlStore(t, "VarableCache", benchmarkVarableCache)
}
func TestMaxVersionFrequencySaveFrequencyDif(t *testing.T) {
	//t.Skip()
	testIavlStore(t, "maxVersionFreqSaveFreq-dif", benchmarkMaxVersionFrequencySaveFrequencyDif)
}
func TestVersionFrequency(t *testing.T) {
	//t.Skip()
	testIavlStore(t, "maxVerFreq", benchmarkVersionFrequency)
}
func TestVersionFrequencyDif(t *testing.T) {
	//t.Skip()
	testIavlStore(t, "maxVerFreq-diff", benchmarkVersionFrequencyDif)
}
func TestSaveFrequencyDif(t *testing.T) {
	//t.Skip()
	testIavlStore(t, "SaveFrequency-diff", benchmarkSaveFrequencyDif)
}
func TestSaveFrequency(t *testing.T) {
	//t.Skip()
	testIavlStore(t, "SaveFrequency", benchmarkSaveFrequency)
}

func BenchmarkIavlStore(b *testing.B) {
	//b.Skip()
	log.Setup("debug", "file://-")
	log.Root.With("module", "diff-iavlstore")

	testno = 0
	require.NoError(b, os.RemoveAll("testdata"))
	_, err := os.Stat("testdata")
	require.True(b, os.IsNotExist(err))

	diskDbType = "goleveldb"
	numBlocks = 20
	blockSize = 10
	saveFrequency = 70
	versionFrequency = 20
	maxVersions = 2

	generateBlocks(b)
	fmt.Println("num blocks", numBlocks, "block size", blockSize, "save frequency", saveFrequency,
		"version frequecny", versionFrequency, "max versions", maxVersions)

	//benchmarkIavlStore(b, "normal", benchmarkNormal)
	//benchmarkIavlStore(b, "normal-dif", benchmarkNormalDif)
	//benchmarkIavlStore(b, "maxVersions-dif", benchmarkMaxVersionsDif)
	benchmarkIavlStore(b, "maxVersions", benchmarkMaxVersions)
	//benchmarkIavlStore(b, "VarableCache-dif", benchmarkVarableCacheDif)
	//benchmarkIavlStore(b, "VarableCache", benchmarkVarableCache)
	//benchmarkIavlStore(b, "maxVerFreq-diff", benchmarkVersionFrequencyDif)
	//benchmarkIavlStore(b, "maxVerFreq", benchmarkVersionFrequency)
	//benchmarkIavlStore(b, "SaveFrequency-diff", benchmarkSaveFrequencyDif)
	//benchmarkIavlStore(b, "SaveFrequency", benchmarkSaveFrequency)
	//benchmarkIavlStore(b, "maxVersionFreqSaveFreq-dif", benchmarkMaxVersionFrequencySaveFrequencyDif)

	files, err := ioutil.ReadDir("./testdata")
	require.NoError(b, err)
	for _, f := range files {
		require.True(b, f.IsDir())
		fmt.Println()
		fmt.Println("size of "+f.Name()+" : ", DirSize(b, f), DirSize(b, f)/1000000, "Mb")

		diskDb := getDiskDb(b, f.Name()[0:len(f.Name())-3])
		//showDiskVersions(b, diskDb, f.Name())
		//fmt.Println(f.Name(),"------------------------before--------------------------------")
		//for k,v := range diskDb.Stats() {
		//	fmt.Println("k",k,"v",v)
		//}
		//fmt.Println(f.Name(),"------------------------before--------------------------------")
		diskDb.Close()

		ldb, err := leveldb.OpenFile("./testdata/"+f.Name(), nil)
		require.NoError(b, err)
		//stats := leveldb.DBStats{}
		//require.NoError(b,  ldb.Stats(&stats))
		//fmt.Println(f.Name(), "stats", stats)
		require.NoError(b, ldb.CompactRange(util.Range{nil, nil}))
		require.NoError(b, ldb.Close())

		fmt.Println("after compact size of "+f.Name()+" : ", DirSize(b, f), DirSize(b, f)/1000000, "Mb")
		diskDb = getDiskDb(b, f.Name()[0:len(f.Name())-3])
		//fmt.Println(f.Name(),"------------------------after--------------------------------")
		//for k,v := range diskDb.Stats() {
		//	fmt.Println("k",k,"v",v)
		//}
		//fmt.Println(f.Name(),"------------------------after--------------------------------")
		diskDb.Close()
	}
}

func testIavlStore(b require.TestingT, name string, fn benchFunc) {
	startTime := time.Now()

	fn(b)

	now := time.Now()
	elapsed := now.Sub(startTime)
	fmt.Println("test "+name+" took", elapsed)
}

func benchmarkIavlStore(b *testing.B, name string, fn benchFunc) {
	b.Run(name, func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			fn(b)
		}
	})
}

func showDiskVersions(b require.TestingT, diskDb db.DB, testname string) {
	if !verbose {
		return
	}

	diskTree := iavl.NewMutableTree(diskDb, 0)
	_, err := diskTree.Load()
	require.NoError(b, err)
	fmt.Println("versions found on disk on test", testname)
	for i := 1; i <= numBlocks; i++ {
		if diskTree.VersionExists(int64(i)) {
			fmt.Println(testname, "disk version exists for block", i)
		}
	}

}

func benchmarkNormalDif(b require.TestingT) {
	testno++
	diskDb := getDiskDb(b, "normal-diff")
	store, err := NewDiffIavlStore(diskDb, 0, 0, 0, 0, 1000, 0)
	require.NoError(b, err)
	executeBlocks(b, blocks, *store)
	_, _, err = store.tree.SaveVersion()
	require.NoError(b, err)
	diskDb.Close()
}

func benchmarkNormal(b require.TestingT) {
	testno++
	diskDb := getDiskDb(b, "Normal")
	store, err := NewIAVLStore(diskDb, 0, 0, 0, 0, minCache, 0)
	require.NoError(b, err)
	executeBlocks(b, blocks, *store)
	_, _, err = store.tree.SaveVersion()
	require.NoError(b, err)
	diskDb.Close()
}

func benchmarkMaxVersions(b require.TestingT) {
	testno++
	diskDb := getDiskDb(b, "maxVers")
	store, err := NewIAVLStore(diskDb, int64(maxVersions), 0, 0, 0, minCache, 0)
	require.NoError(b, err)
	executeBlocks(b, blocks, *store)
	_, _, err = store.tree.SaveVersion()
	require.NoError(b, err)
	diskDb.Close()
}

func benchmarkMaxVersionsDif(b require.TestingT) {
	testno++
	diskDb := getDiskDb(b, "maxVers-diff")
	store, err := NewDiffIavlStore(diskDb, int64(maxVersions), 0, 0, 0, minCache, 0)
	require.NoError(b, err)
	require.NoError(b, err)
	executeBlocks(b, blocks, *store)
	_, _, err = store.tree.SaveVersion()
	require.NoError(b, err)
	diskDb.Close()
}

func benchmarkVarableCacheDif(b require.TestingT) {
	testno++
	diskDb := getDiskDb(b, "varCache-diff")
	store, err := NewDiffIavlStore(diskDb, int64(maxVersions), 0, 0, 0, minCache, maxCache)
	require.NoError(b, err)
	executeBlocks(b, blocks, *store)
	_, _, err = store.tree.SaveVersion()
	require.NoError(b, err)
	diskDb.Close()
}

func benchmarkVarableCache(b require.TestingT) {
	testno++
	diskDb := getDiskDb(b, "varCache")
	store, err := NewIAVLStore(diskDb, int64(maxVersions), 0, 0, 0, minCache, maxCache)
	require.NoError(b, err)
	executeBlocks(b, blocks, *store)
	_, _, err = store.tree.SaveVersion()
	require.NoError(b, err)
	diskDb.Close()
}

func benchmarkMaxVersionFrequencySaveFrequencyDif(b require.TestingT) {
	testno++
	diskDb := getDiskDb(b, "maxVFSF-diff")
	store, err := NewDiffIavlStore(diskDb, int64(maxVersions), 0, uint64(saveFrequency), uint64(versionFrequency), minCache, 0)
	require.NoError(b, err)
	executeBlocks(b, blocks, *store)
	_, _, err = store.tree.SaveVersion()
	require.NoError(b, err)
	diskDb.Close()
}

func benchmarkVersionFrequency(b require.TestingT) {
	testno++
	diskDb := getDiskDb(b, "verFreq")
	store, err := NewIAVLStore(diskDb, int64(maxVersions), 0, 0, uint64(versionFrequency), minCache, 0)
	require.NoError(b, err)
	executeBlocks(b, blocks, *store)
	_, _, err = store.tree.SaveVersion()
	require.NoError(b, err)
	diskDb.Close()
}

func benchmarkVersionFrequencyDif(b require.TestingT) {
	testno++
	diskDb := getDiskDb(b, "verFreq-diff")
	store, err := NewDiffIavlStore(diskDb, int64(maxVersions), 0, 0, uint64(versionFrequency), minCache, 0)
	require.NoError(b, err)
	executeBlocks(b, blocks, *store)
	_, _, err = store.tree.SaveVersion()
	require.NoError(b, err)
	diskDb.Close()
}

func benchmarkSaveFrequencyDif(b require.TestingT) {
	testno++
	diskDb := getDiskDb(b, "saveFreq-diff")
	store, err := NewDiffIavlStore(diskDb, int64(maxVersions), 0, uint64(saveFrequency), 0, minCache, 0)
	require.NoError(b, err)
	executeBlocks(b, blocks, *store)
	_, _, err = store.tree.SaveVersion()
	require.NoError(b, err)
	diskDb.Close()
}

func benchmarkSaveFrequency(b require.TestingT) {
	testno++
	diskDb := getDiskDb(b, "saveFreq")
	store, err := NewIAVLStore(diskDb, int64(maxVersions), 0, uint64(saveFrequency), 0, minCache, 0)
	require.NoError(b, err)
	executeBlocks(b, blocks, *store)
	_, _, err = store.tree.SaveVersion()
	require.NoError(b, err)
	diskDb.Close()
}

func DirSize(b require.TestingT, fi os.FileInfo) int64 {
	var size int64
	cwd, err := os.Getwd()
	require.NoError(b, err)

	path := filepath.Join(cwd, "testdata", fi.Name())
	err = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return err
	})
	require.NoError(b, err)
	return size
}

func DirSize2(fi os.FileInfo) (int64, error) {
	var size int64
	cwd, err := os.Getwd()
	if err != nil {
		return 0, err
	}

	path := filepath.Join(cwd, "testdata", fi.Name())
	err = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return err
	})
	return size, err
}
