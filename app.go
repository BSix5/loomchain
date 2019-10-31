package loomchain

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/go-kit/kit/metrics"
	kitprometheus "github.com/go-kit/kit/metrics/prometheus"
	cctypes "github.com/loomnetwork/go-loom/builtin/types/chainconfig"
	"github.com/pkg/errors"

	// "github.com/pkg/errors"
	stdprometheus "github.com/prometheus/client_golang/prometheus"
	abci "github.com/tendermint/tendermint/abci/types"
	ttypes "github.com/tendermint/tendermint/types"

	"github.com/loomnetwork/loomchain/eth/utils"
	"github.com/loomnetwork/loomchain/features"
	"github.com/loomnetwork/loomchain/registry"
	"github.com/loomnetwork/loomchain/txhandler"

	"github.com/loomnetwork/loomchain/log"
	appstate "github.com/loomnetwork/loomchain/state"
	"github.com/loomnetwork/loomchain/store"
	blockindex "github.com/loomnetwork/loomchain/store/block_index"
	evmaux "github.com/loomnetwork/loomchain/store/evm_aux"
)

type QueryHandler interface {
	Handle(state appstate.ReadOnlyState, path string, data []byte) ([]byte, error)
}

type KarmaHandler interface {
	Upkeep() error
}

type ValidatorsManager interface {
	BeginBlock(abci.RequestBeginBlock, int64) error
	EndBlock(abci.RequestEndBlock) ([]abci.ValidatorUpdate, error)
}

type ChainConfigManager interface {
	EnableFeatures(blockHeight int64) error
	UpdateConfig() (int, error)
}

type ValidatorsManagerFactoryFunc func(state appstate.State) (ValidatorsManager, error)

type ChainConfigManagerFactoryFunc func(state appstate.State) (ChainConfigManager, error)

type Application struct {
	lastBlockHeader abci.Header
	curBlockHeader  abci.Header
	curBlockHash    []byte
	Store           store.VersionedKVStore
	Init            func(appstate.State) error
	txhandler.TxHandler
	QueryHandler
	EventHandler
	ReceiptHandlerProvider
	txhandler.TxHandlerFactory
	EvmAuxStore *evmaux.EvmAuxStore
	blockindex.BlockIndexStore
	CreateValidatorManager   ValidatorsManagerFactoryFunc
	CreateChainConfigManager ChainConfigManagerFactoryFunc
	// Callback function used to construct a contract upkeep handler at the start of each block,
	// should return a nil handler when the contract upkeep feature is disabled.
	CreateContractUpkeepHandler func(state appstate.State) (KarmaHandler, error)
	GetValidatorSet             appstate.GetValidatorSet
	EventStore                  store.EventStore
	config                      *cctypes.Config
	childTxRefs                 []evmaux.ChildTxRef // links Tendermint txs to EVM txs
	ReceiptsVersion             int32
	DebugTxHandler              txhandler.TxHandler
}

var _ abci.Application = &Application{}

//Metrics
var (
	deliverTxLatency     metrics.Histogram
	checkTxLatency       metrics.Histogram
	commitBlockLatency   metrics.Histogram
	beginBlockLatency    metrics.Histogram
	endBlockLatency      metrics.Histogram
	requestCount         metrics.Counter
	committedBlockCount  metrics.Counter
	validatorFuncLatency metrics.Histogram
)

func init() {
	fieldKeys := []string{"method", "error"}
	requestCount = kitprometheus.NewCounterFrom(stdprometheus.CounterOpts{
		Namespace: "loomchain",
		Subsystem: "application",
		Name:      "request_count",
		Help:      "Number of requests received.",
	}, fieldKeys)
	deliverTxLatency = kitprometheus.NewSummaryFrom(stdprometheus.SummaryOpts{
		Namespace:  "loomchain",
		Subsystem:  "application",
		Name:       "delivertx_latency_microseconds",
		Help:       "Total duration of delivertx in microseconds.",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	}, []string{"method", "error", "evm"})

	checkTxLatency = kitprometheus.NewSummaryFrom(stdprometheus.SummaryOpts{
		Namespace:  "loomchain",
		Subsystem:  "application",
		Name:       "checktx_latency_microseconds",
		Help:       "Total duration of checktx in microseconds.",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	}, fieldKeys)
	commitBlockLatency = kitprometheus.NewSummaryFrom(stdprometheus.SummaryOpts{
		Namespace:  "loomchain",
		Subsystem:  "application",
		Name:       "commit_block_latency_microseconds",
		Help:       "Total duration of commit block in microseconds.",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	}, fieldKeys)
	beginBlockLatency = kitprometheus.NewSummaryFrom(stdprometheus.SummaryOpts{
		Namespace:  "loomchain",
		Subsystem:  "application",
		Name:       "begin_block_latency",
		Help:       "Total duration of begin block in seconds.",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	}, []string{"method"})
	endBlockLatency = kitprometheus.NewSummaryFrom(stdprometheus.SummaryOpts{
		Namespace:  "loomchain",
		Subsystem:  "application",
		Name:       "end_block_latency",
		Help:       "Total duration of end block in seconds.",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	}, []string{"method"})

	committedBlockCount = kitprometheus.NewCounterFrom(stdprometheus.CounterOpts{
		Namespace: "loomchain",
		Subsystem: "application",
		Name:      "block_count",
		Help:      "Number of committed blocks.",
	}, fieldKeys)

	validatorFuncLatency = kitprometheus.NewSummaryFrom(stdprometheus.SummaryOpts{
		Namespace:  "loomchain",
		Subsystem:  "application",
		Name:       "validator_election_latency",
		Help:       "Total duration of validator election in seconds.",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	}, []string{})
}

func (a *Application) Info(req abci.RequestInfo) abci.ResponseInfo {
	return abci.ResponseInfo{
		LastBlockAppHash: a.Store.Hash(),
		LastBlockHeight:  a.Store.Version(),
	}
}

func (a *Application) SetOption(req abci.RequestSetOption) abci.ResponseSetOption {
	return abci.ResponseSetOption{}
}

func (a *Application) InitChain(req abci.RequestInitChain) abci.ResponseInitChain {
	if a.height() != 1 {
		panic("state version is not 1")
	}

	state := appstate.NewStoreState(
		context.Background(),
		a.Store,
		abci.Header{
			ChainID: req.ChainId,
		},
		nil,
		a.GetValidatorSet,
	)

	if a.Init != nil {
		err := a.Init(state)
		if err != nil {
			panic(err)
		}
	}

	return abci.ResponseInitChain{}
}

func (a *Application) BeginBlock(req abci.RequestBeginBlock) abci.ResponseBeginBlock {
	defer func(begin time.Time) {
		lvs := []string{"method", "BeginBlock"}
		beginBlockLatency.With(lvs...).Observe(time.Since(begin).Seconds())
	}(time.Now())

	block := req.Header
	if block.Height != a.height() {
		panic(fmt.Sprintf("app height %d doesn't match BeginBlock height %d", a.height(), block.Height))
	}

	if a.config == nil {
		var err error
		a.config, err = store.LoadOnChainConfig(a.Store)
		if err != nil {
			panic(err)
		}
	}

	a.curBlockHeader = block
	a.curBlockHash = req.Hash

	if a.CreateContractUpkeepHandler != nil {
		upkeepStoreTx := store.WrapAtomic(a.Store).BeginTx()
		upkeepState := appstate.NewStoreState(
			context.Background(),
			upkeepStoreTx,
			a.curBlockHeader,
			a.curBlockHash,
			a.GetValidatorSet,
		).WithOnChainConfig(a.config)
		contractUpkeepHandler, err := a.CreateContractUpkeepHandler(upkeepState)
		if err != nil {
			panic(err)
		}
		if contractUpkeepHandler != nil {
			if err := contractUpkeepHandler.Upkeep(); err != nil {
				panic(err)
			}
			upkeepStoreTx.Commit()
		}
	}

	storeTx := store.WrapAtomic(a.Store).BeginTx()
	state := appstate.NewStoreState(
		context.Background(),
		storeTx,
		a.curBlockHeader,
		nil,
		a.GetValidatorSet,
	).WithOnChainConfig(a.config)

	validatorManager, err := a.CreateValidatorManager(state)
	if err != registry.ErrNotFound {
		if err != nil {
			panic(err)
		}

		err = validatorManager.BeginBlock(req, a.height())
		if err != nil {
			panic(err)
		}
	}

	//Enable Features
	chainConfigManager, err := a.CreateChainConfigManager(state)
	if err != nil {
		panic(err)
	}
	if chainConfigManager != nil {
		if err := chainConfigManager.EnableFeatures(a.height()); err != nil {
			panic(err)
		}

		numConfigChanges, err := chainConfigManager.UpdateConfig()
		if err != nil {
			panic(err)
		}

		if numConfigChanges > 0 {
			// invalidate cached config so it's reloaded next time it's accessed
			a.config = nil
		}
	}

	storeTx.Commit()

	return abci.ResponseBeginBlock{}
}

func (a *Application) EndBlock(req abci.RequestEndBlock) abci.ResponseEndBlock {
	defer func(begin time.Time) {
		lvs := []string{"method", "EndBlock"}
		endBlockLatency.With(lvs...).Observe(time.Since(begin).Seconds())
	}(time.Now())

	if req.Height != a.height() {
		panic(fmt.Sprintf("app height %d doesn't match EndBlock height %d", a.height(), req.Height))
	}

	// TODO: receiptHandler.CommitBlock() should be moved to Application.Commit()
	storeTx := store.WrapAtomic(a.Store).BeginTx()

	if a.ReceiptHandlerProvider != nil {
		receiptHandler := a.ReceiptHandlerProvider.Store()
		if err := receiptHandler.CommitBlock(a.height()); err != nil {
			storeTx.Rollback()
			// TODO: maybe panic instead?
			log.Error(fmt.Sprintf("aborted committing block receipts, %v", err.Error()))
		} else {
			storeTx.Commit()
		}
	}

	storeTx = store.WrapAtomic(a.Store).BeginTx()
	state := appstate.NewStoreState(
		context.Background(),
		storeTx,
		a.curBlockHeader,
		nil,
		a.GetValidatorSet,
	).WithOnChainConfig(a.config)

	validatorManager, err := a.CreateValidatorManager(state)
	if err != registry.ErrNotFound {
		if err != nil {
			panic(err)
		}
		t2 := time.Now()
		validators, err := validatorManager.EndBlock(req)

		diffsecs := time.Since(t2).Seconds()
		validatorFuncLatency.Observe(diffsecs)

		log.Info(fmt.Sprintf("validator manager took %f seconds-----\n", diffsecs))
		if err != nil {
			panic(err)
		}
		storeTx.Commit()

		return abci.ResponseEndBlock{
			ValidatorUpdates: validators,
		}
	}
	return abci.ResponseEndBlock{
		ValidatorUpdates: []abci.ValidatorUpdate{},
	}
}

func (a *Application) CheckTx(txBytes []byte) abci.ResponseCheckTx {
	var err error
	defer func(begin time.Time) {
		lvs := []string{"method", "CheckTx", "error", fmt.Sprint(err != nil)}
		checkTxLatency.With(lvs...).Observe(time.Since(begin).Seconds())
	}(time.Now())

	// If the chain is configured not to generate empty blocks then CheckTx may be called before
	// BeginBlock when the application restarts, which means that both curBlockHeader and
	// lastBlockHeader will be default initialized. Instead of invoking a contract method with
	// a vastly innacurate block header simply skip invoking the contract. This has the minor
	// disadvantage of letting an potentially invalid tx propagate to other nodes, but this should
	// only happen on node restarts, and only if the node doesn't receive any txs from it's peers
	// before a client sends it a tx.
	if a.curBlockHeader.Height == 0 {
		return abci.ResponseCheckTx{Code: abci.CodeTypeOK}
	}

	storeTx := store.WrapAtomic(a.Store).BeginTx()
	defer storeTx.Rollback()

	state := appstate.NewStoreState(
		context.Background(),
		storeTx,
		a.curBlockHeader,
		a.curBlockHash,
		a.GetValidatorSet,
	).WithOnChainConfig(a.config)

	// Receipts & events generated in CheckTx must be discarded since the app state changes they
	// reflect aren't persisted.
	defer a.ReceiptHandlerProvider.Store().DiscardCurrentReceipt()
	defer a.EventHandler.Rollback()

	_, err = a.TxHandler.ProcessTx(state, txBytes, true)
	if err != nil {
		log.Error("CheckTx", "tx", hex.EncodeToString(ttypes.Tx(txBytes).Hash()), "err", err)
		return abci.ResponseCheckTx{Code: 1, Log: err.Error()}
	}

	return abci.ResponseCheckTx{Code: abci.CodeTypeOK}
}

func (a *Application) DeliverTx(txBytes []byte) abci.ResponseDeliverTx {
	var txFailed, isEvmTx bool
	defer func(begin time.Time) {
		lvs := []string{
			"method", "DeliverTx",
			"error", fmt.Sprint(txFailed),
			"evm", fmt.Sprint(isEvmTx),
		}
		requestCount.With(lvs[:4]...).Add(1)
		deliverTxLatency.With(lvs...).Observe(time.Since(begin).Seconds())
	}(time.Now())

	storeTx := store.WrapAtomic(a.Store).BeginTx()
	defer storeTx.Rollback()

	state := appstate.NewStoreState(
		context.Background(),
		storeTx,
		a.curBlockHeader,
		a.curBlockHash,
		a.GetValidatorSet,
	).WithOnChainConfig(a.config)

	var r abci.ResponseDeliverTx

	if state.FeatureEnabled(features.EvmTxReceiptsVersion3_1, false) {
		r = a.deliverTx2(storeTx, txBytes)
	} else {
		r = a.deliverTx(storeTx, txBytes)
	}

	txFailed = r.Code != abci.CodeTypeOK
	// TODO: this isn't 100% reliable when txFailed == true
	isEvmTx = r.Info == utils.CallEVM || r.Info == utils.DeployEvm
	return r
}

// This version of DeliverTx doesn't store the receipts for failed EVM txs.
func (a *Application) deliverTx(storeTx store.KVStoreTx, txBytes []byte) abci.ResponseDeliverTx {
	r, err := a.processTx(storeTx, txBytes, false)
	if err != nil {
		log.Error("DeliverTx", "tx", hex.EncodeToString(ttypes.Tx(txBytes).Hash()), "err", err)
		return abci.ResponseDeliverTx{Code: 1, Log: err.Error()}
	}
	return abci.ResponseDeliverTx{Code: abci.CodeTypeOK, Data: r.Data, Tags: r.Tags, Info: r.Info}
}

func (a *Application) processTx(storeTx store.KVStoreTx, txBytes []byte, isCheckTx bool) (txhandler.TxHandlerResult, error) {
	state := appstate.NewStoreState(
		context.Background(),
		storeTx,
		a.curBlockHeader,
		a.curBlockHash,
		a.GetValidatorSet,
	).WithOnChainConfig(a.config)

	receiptHandler := a.ReceiptHandlerProvider.Store()
	defer receiptHandler.DiscardCurrentReceipt()
	defer a.EventHandler.Rollback()

	r, err := a.TxHandler.ProcessTx(state, txBytes, isCheckTx)
	if err != nil {
		return r, err
	}

	if !isCheckTx {
		a.EventHandler.Commit(uint64(a.curBlockHeader.GetHeight()))

		saveEvmTxReceipt := r.Info == utils.CallEVM || r.Info == utils.DeployEvm ||
			state.FeatureEnabled(features.EvmTxReceiptsVersion3, false) || a.ReceiptsVersion == 3

		if saveEvmTxReceipt {
			if err := a.EventHandler.LegacyEthSubscriptionSet().EmitTxEvent(r.Data, r.Info); err != nil {
				log.Error("Emit Tx Event error", "err", err)
			}

			reader := a.ReceiptHandlerProvider.Reader()
			if reader.GetCurrentReceipt() != nil {
				receiptTxHash := reader.GetCurrentReceipt().TxHash
				if err := a.EventHandler.EthSubscriptionSet().EmitTxEvent(receiptTxHash); err != nil {
					log.Error("failed to emit tx event to subscribers", "err", err)
				}
				txHash := ttypes.Tx(txBytes).Hash()
				// If a receipt was generated for an EVM tx add a link between the TM tx hash and the EVM tx hash
				// so that we can use it to lookup relevant events using the TM tx hash.
				if !bytes.Equal(txHash, receiptTxHash) {
					a.childTxRefs = append(a.childTxRefs, evmaux.ChildTxRef{
						ParentTxHash: txHash,
						ChildTxHash:  receiptTxHash,
					})
				}
				receiptHandler.CommitCurrentReceipt()
			}
		}
		storeTx.Commit()
	}
	return r, nil
}

// This version of DeliverTx stores the receipts for failed EVM txs.
func (a *Application) deliverTx2(storeTx store.KVStoreTx, txBytes []byte) abci.ResponseDeliverTx {
	state := appstate.NewStoreState(
		context.Background(),
		storeTx,
		a.curBlockHeader,
		a.curBlockHash,
		a.GetValidatorSet,
	).WithOnChainConfig(a.config)
	if a.ReceiptHandlerProvider != nil {
		//receiptHandler := a.ReceiptHandlerProvider.Store()
		defer a.ReceiptHandlerProvider.Store().DiscardCurrentReceipt()
		defer a.EventHandler.Rollback()
	}

	r, txErr := a.TxHandler.ProcessTx(state, txBytes, false)

	// Store the receipt even if the tx itself failed
	var receiptTxHash []byte
	if a.ReceiptHandlerProvider != nil && a.ReceiptHandlerProvider.Reader().GetCurrentReceipt() != nil {
		receiptTxHash = a.ReceiptHandlerProvider.Reader().GetCurrentReceipt().TxHash
		txHash := ttypes.Tx(txBytes).Hash()
		// If a receipt was generated for an EVM tx add a link between the TM tx hash and the EVM tx hash
		// so that we can use it to lookup relevant events using the TM tx hash.
		if !bytes.Equal(txHash, receiptTxHash) {
			a.childTxRefs = append(a.childTxRefs, evmaux.ChildTxRef{
				ParentTxHash: txHash,
				ChildTxHash:  receiptTxHash,
			})
		}
		a.ReceiptHandlerProvider.Store().CommitCurrentReceipt()
	}

	if txErr != nil {
		log.Error("DeliverTx", "tx", hex.EncodeToString(ttypes.Tx(txBytes).Hash()), "err", txErr)
		// FIXME: Really shouldn't be using r.Data if txErr != nil, but need to refactor TxHandler.ProcessTx
		//        so it only returns r with the correct status code & log fields.
		// Pass the EVM tx hash (if any) back to Tendermint so it stores it in block results
		return abci.ResponseDeliverTx{Code: 1, Data: r.Data, Log: txErr.Error()}
	}

	storeTx.Commit()

	if a.EventHandler != nil {
		a.EventHandler.Commit(uint64(a.curBlockHeader.GetHeight()))
		// FIXME: Really shouldn't be sending out events until the whole block is committed because
		//        the state changes from the tx won't be visible to queries until after Application.Commit()
		if err := a.EventHandler.LegacyEthSubscriptionSet().EmitTxEvent(r.Data, r.Info); err != nil {
			log.Error("Emit Tx Event error", "err", err)
		}
	}

	if len(receiptTxHash) > 0 {
		if err := a.EventHandler.EthSubscriptionSet().EmitTxEvent(receiptTxHash); err != nil {
			log.Error("failed to emit tx event to subscribers", "err", err)
		}
	}

	return abci.ResponseDeliverTx{Code: abci.CodeTypeOK, Data: r.Data, Tags: r.Tags, Info: r.Info}
}

// Commit commits the current block
func (a *Application) Commit() abci.ResponseCommit {
	var err error
	defer func(begin time.Time) {
		lvs := []string{"method", "Commit", "error", fmt.Sprint(err != nil)}
		committedBlockCount.With(lvs...).Add(1)
		commitBlockLatency.With(lvs...).Observe(time.Since(begin).Seconds())
	}(time.Now())
	appHash, _, err := a.Store.SaveVersion()
	if err != nil {
		panic(err)
	}

	height := a.curBlockHeader.GetHeight()

	if a.EvmAuxStore != nil {
		if err := a.EvmAuxStore.SaveChildTxRefs(a.childTxRefs); err != nil {
			// TODO: consider panic instead
			log.Error("Failed to save Tendermint -> EVM tx hash refs", "height", height, "err", err)
		}
	}
	a.childTxRefs = nil

	if a.EventHandler != nil {
		go func(height int64, blockHeader abci.Header) {
			if err := a.EventHandler.EmitBlockTx(uint64(height), blockHeader.Time); err != nil {
				log.Error("Emit Block Event error", "err", err)
			}
			if err := a.EventHandler.LegacyEthSubscriptionSet().EmitBlockEvent(blockHeader); err != nil {
				log.Error("Emit Block Event error", "err", err)
			}
			if err := a.EventHandler.EthSubscriptionSet().EmitBlockEvent(blockHeader); err != nil {
				log.Error("Emit Block Event error", "err", err)
			}
		}(height, a.curBlockHeader)
	}

	a.lastBlockHeader = a.curBlockHeader

	if err := a.Store.Prune(); err != nil {
		log.Error("failed to prune app.db", "err", err)
	}

	if a.BlockIndexStore != nil {
		a.BlockIndexStore.SetBlockHashAtHeight(uint64(height), a.curBlockHash)
	}

	return abci.ResponseCommit{
		Data: appHash,
	}
}

func (a *Application) Query(req abci.RequestQuery) abci.ResponseQuery {
	if a.QueryHandler == nil {
		return abci.ResponseQuery{Code: 1, Log: "not implemented"}
	}

	result, err := a.QueryHandler.Handle(a.ReadOnlyState(), req.Path, req.Data)
	if err != nil {
		return abci.ResponseQuery{Code: 1, Log: err.Error()}
	}

	return abci.ResponseQuery{Code: abci.CodeTypeOK, Value: result}
}

func (a *Application) height() int64 {
	return a.Store.Version() + 1
}

func (a *Application) ReadOnlyState() appstate.State {
	// TODO: the store snapshot should be created atomically, otherwise the block header might
	//       not match the state... need to figure out why this hasn't spectacularly failed already
	return appstate.NewStoreStateSnapshot(
		nil,
		a.Store.GetSnapshot(),
		a.lastBlockHeader,
		nil, // TODO: last block hash!
		a.GetValidatorSet,
	)
}

func (a *Application) ReplayApplication(blockNumber uint64, blockstore store.BlockStore) (*Application, int64, error) {
	startVersion := int64(blockNumber) - 1
	if startVersion < 0 {
		return nil, 0, errors.Errorf("invalid block number %d", blockNumber)
	}
	var snapshot store.Snapshot
	for err := error(nil); (snapshot == nil || err != nil) && startVersion > 0; startVersion-- {
		snapshot, err = a.Store.GetSnapshotAt(startVersion)
	}
	if startVersion == 0 {
		return nil, 0, errors.Errorf("no saved version for height %d", blockNumber)
	}

	txHandle, err := a.TxHandlerFactory.TxHandler(nil, false)
	if err != nil {
		return nil, 0, err
	}
	newApp := &Application{
		Store: store.NewSplitStore(snapshot, store.NewMemStore(), startVersion-1),
		Init: func(state appstate.State) error {
			panic("init should not be called")
		},
		TxHandler:                   txHandle,
		TxHandlerFactory:            a.TxHandlerFactory,
		BlockIndexStore:             nil,
		EventHandler:                nil,
		ReceiptHandlerProvider:      nil,
		CreateValidatorManager:      a.CreateValidatorManager,
		CreateChainConfigManager:    a.CreateChainConfigManager,
		CreateContractUpkeepHandler: a.CreateContractUpkeepHandler,
		EventStore:                  nil,
		GetValidatorSet:             a.GetValidatorSet,
		EvmAuxStore:                 nil,
		ReceiptsVersion:             a.ReceiptsVersion,
	}
	return newApp, startVersion, nil
}

func (a *Application) SetTracer(tracer vm.Tracer, metrics bool) error {
	newTxHandle, err := a.TxHandlerFactory.TxHandler(tracer, metrics)
	if err != nil {
		return errors.Wrap(err, "making transaction handle")
	}
	a.TxHandler = newTxHandle
	return nil
}
