package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/loomnetwork/loom"
	"github.com/loomnetwork/loom/abci/backend"
	"github.com/loomnetwork/loom/auth"
	"github.com/loomnetwork/loom/log"
	"github.com/loomnetwork/loom/plugin"
	"github.com/loomnetwork/loom/rpc"
	"github.com/loomnetwork/loom/store"
	"github.com/loomnetwork/loom/util"
	"github.com/loomnetwork/loom/vm"

	"github.com/spf13/cobra"
	dbm "github.com/tendermint/tmlibs/db"
)

var RootCmd = &cobra.Command{
	Use:   "loom",
	Short: "Loom DAppChain",
}

var codeLoaders map[vm.VMType]ContractCodeLoader

func init() {
	codeLoaders = map[vm.VMType]ContractCodeLoader{
		vm.VMType_PLUGIN: &PluginCodeLoader{},
		vm.VMType_EVM:    &TruffleCodeLoader{},
	}
}

func newInitCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize configs and data",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := parseConfig()
			if err != nil {
				return err
			}
			backend := initBackend(cfg)
			if force {
				err = backend.Destroy()
				if err != nil {
					return err
				}
				err = destroyApp(cfg)
				if err != nil {
					return err
				}
			}
			err = backend.Init()
			if err != nil {
				return err
			}

			err = initApp(cfg)
			if err != nil {
				return err
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "force initialization")
	return cmd
}

func newResetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "reset",
		Short: "Reset the app and blockchain state only",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := parseConfig()
			if err != nil {
				return err
			}

			backend := initBackend(cfg)

			err = backend.Reset(0)
			if err != nil {
				return err
			}

			err = resetApp(cfg)
			if err != nil {
				return err
			}

			return nil
		},
	}
}

func newRunCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "run [root contract]",
		Short: "Run the blockchain node",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := parseConfig()
			if err != nil {
				return err
			}
			backend := initBackend(cfg)
			loader := plugin.NewManager(cfg.PluginsPath())
			chainID, err := backend.ChainID()
			if err != nil {
				return err
			}
			app, err := loadApp(chainID, cfg, loader)
			if err != nil {
				return err
			}
			if err := backend.Start(app); err != nil {
				return err
			}
			qs := &rpc.QueryServer{
				StateProvider: app,
				ChainID:       chainID,
				Host:          cfg.QueryServerHost,
				Logger:        log.Root.With("module", "query-server"),
				Loader:        loader,
			}
			if err := qs.Start(); err != nil {
				return err
			}
			backend.RunForever()
			return nil
		},
	}
}

func initDB(name, dir string) error {
	dbPath := filepath.Join(dir, name+".db")
	if util.FileExists(dbPath) {
		return errors.New("db already exists")
	}

	return nil
}

func destroyDB(name, dir string) error {
	dbPath := filepath.Join(dir, name+".db")
	return os.RemoveAll(dbPath)
}

func resetApp(cfg *Config) error {
	return destroyDB(cfg.DBName, cfg.RootPath())
}

func initApp(cfg *Config) error {
	var gen genesis

	file, err := os.OpenFile(cfg.GenesisPath(), os.O_EXCL|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "    ")
	err = enc.Encode(gen)
	if err != nil {
		return err
	}

	err = initDB(cfg.DBName, cfg.RootPath())
	if err != nil {
		return err
	}

	return nil
}

func destroyApp(cfg *Config) error {
	err := os.Remove(cfg.GenesisPath())
	if err != nil {
		return err
	}
	return resetApp(cfg)
}

func loadApp(chainID string, cfg *Config, loader plugin.Loader) (*loom.Application, error) {
	db, err := dbm.NewGoLevelDB(cfg.DBName, cfg.RootPath())
	if err != nil {
		return nil, err
	}

	appStore, err := store.NewIAVLStore(db)
	if err != nil {
		return nil, err
	}

	vmManager := vm.NewManager()
	vmManager.Register(vm.VMType_PLUGIN, func(state loom.State) vm.VM {
		return &plugin.PluginVM{
			Loader: loader,
			State:  state,
		}
	})

	deployTxHandler := &vm.DeployTxHandler{
		Manager: vmManager,
	}

	callTxHandler := &vm.CallTxHandler{
		Manager: vmManager,
	}

	gen, err := readGenesis(cfg.GenesisPath())
	if err != nil {
		return nil, err
	}

	init := func(state loom.State) error {
		for _, contractCfg := range gen.Contracts {
			vmType := contractCfg.VMType()
			vm, err := vmManager.InitVM(vmType, state)
			if err != nil {
				return err
			}

			loader := codeLoaders[vmType]
			initCode, err := loader.LoadContractCode(
				contractCfg.Location,
				contractCfg.Init,
			)
			if err != nil {
				return err
			}

			_, _, err = vm.Create(loom.RootAddress(chainID), initCode)
			return err
		}
		return nil
	}

	router := loom.NewTxRouter()
	router.Handle(1, deployTxHandler)
	router.Handle(2, callTxHandler)

	return &loom.Application{
		Store: appStore,
		Init:  init,
		TxHandler: loom.MiddlewareTxHandler(
			[]loom.TxMiddleware{
				log.TxMiddleware,
				auth.SignatureTxMiddleware,
				auth.NonceTxMiddleware,
			},
			router,
		),
	}, nil
}

func initBackend(cfg *Config) backend.Backend {
	return &backend.TendermintBackend{
		RootPath: path.Join(cfg.RootPath(), "tendermint"),
	}
}

func main() {
	RootCmd.AddCommand(
		newInitCommand(),
		newResetCommand(),
		newRunCommand(),
	)
	err := RootCmd.Execute()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
