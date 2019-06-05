package plugin

import (
	"github.com/loomnetwork/go-loom"
	contract "github.com/loomnetwork/go-loom/plugin/contractpb"
	"github.com/loomnetwork/loomchain"
	"github.com/loomnetwork/loomchain/builtin/plugins/coin"
	regcommon "github.com/loomnetwork/loomchain/registry"
	"github.com/pkg/errors"
)

var (
	// ErrCoinContractNotFound indicates that the Coin contract hasn't been deployed yet.
	ErrCoinContractNotFound = errors.New("[CoinDeflationManager] CoinContract contract not found")
)

// CoinDeflationManager implements loomchain.CoinDeflationManager interface
type CoinDeflationManager struct {
	ctx   contract.Context
	state loomchain.State
}

// NewCoinDeflationManager attempts to create an instance of CoinDeflationManager.
func NewCoinDeflationManager(pvm *PluginVM, state loomchain.State) (*CoinDeflationManager, error) {
	caller := loom.RootAddress(pvm.State.Block().ChainID)
	contractAddr, err := pvm.Registry.Resolve("coin")
	if err != nil {
		if err == regcommon.ErrNotFound {
			return nil, ErrCoinContractNotFound
		}
		return nil, err
	}
	readOnly := false
	ctx := contract.WrapPluginContext(pvm.CreateContractContext(caller, contractAddr, readOnly))
	return &CoinDeflationManager{
		ctx:   ctx,
		state: state,
	}, nil
}

//MintCoins method of coin_deflation_Manager will be called from Block
func (c *CoinDeflationManager) MintCoins(blockHeight int64) error {
	if c.state.FeatureEnabled(loomchain.CoinDeflationManagerFeature, false) {
		err := coin.MintByCDM(c.ctx,blockHeight)
		if err != nil {
			return err
		}
	}
	return nil
}
