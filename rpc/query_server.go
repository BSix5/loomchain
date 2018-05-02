package rpc

import (
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	proto "github.com/gogo/protobuf/proto"
	loom "github.com/loomnetwork/go-loom"
	"github.com/loomnetwork/go-loom/plugin"
	"github.com/loomnetwork/loomchain"
	"github.com/loomnetwork/loomchain/auth"
	llog "github.com/loomnetwork/loomchain/log"
	lcp "github.com/loomnetwork/loomchain/plugin"
	amino "github.com/tendermint/go-amino"
	rpcserver "github.com/tendermint/tendermint/rpc/lib/server"
	tmcmn "github.com/tendermint/tmlibs/common"
)

// StateProvider interface is used by QueryServer to access the read-only application state
type StateProvider interface {
	ReadOnlyState() loomchain.State
}

// QueryServer provides the ability to query the current state of the DAppChain via RPC.
//
// Contract state can be queried via:
// - POST request of a JSON-RPC 2.0 object to "/" endpoint:
//   {
//     "jsonrpc": "2.0",
//     "method": "query",
//     "params": {
//       "contract": "0x000000000000000000",
//       "query": { /* query params */ }
//     },
//     "id": "123456789"
//   }
// - POST request to "/query" endpoint with form-encoded contract & query params.
//
// Contract query requests must contain two parameters:
// - contract: the address of the contract to be queried (hex encoded string), and
// - query: a JSON object containing the query parameters, the Loom SDK makes no assumptions about
//          the structure of the object, it is entirely up to the contract author to define the
//          query interface.
//
// The JSON-RPC 2.0 response object will contain the query result as a JSON object:
// {
//   "jsonrpc": "2.0",
//   "result": { /* query result */ },
//   "id": "123456789"
// }
//
// On error the JSON-RPC 2.0 response object will look similar to this:
// {
//   "jsonrpc": "2.0",
//   "error": {
//	   "code": -32603,
//	   "message": "Internal error",
//	   "data": "invalid query"
//   },
//   "id": "123456789"
// }
//
// The nonce associated with a particular signer can be obtained via:
// - GET request to /nonce?key="<hex-encoded-public-key-of-signer>"
// - POST request of a JSON-RPC 2.0 object to "/" endpoint:
//   {
//     "jsonrpc": "2.0",
//     "method": "nonce",
//     "params": {
//       "key": "hex-encoded-public-key-of-signer",
//     },
//     "id": "123456789"
//   }
// - POST request to "/nonce" endpoint with form-encoded key param.
type QueryServer struct {
	StateProvider
	ChainID string
	Host    string
	Logger  llog.TMLogger
	Loader  lcp.Loader
}

func (s *QueryServer) Start() error {
	codec := amino.NewCodec()
	smux := http.NewServeMux()
	routes := map[string]*rpcserver.RPCFunc{}
	routes["query"] = rpcserver.NewRPCFunc(s.queryRoute, "contract,query")
	routes["nonce"] = rpcserver.NewRPCFunc(s.nonceRoute, "key")
	rpcserver.RegisterRPCFuncs(smux, routes, codec, s.Logger)
	wm := rpcserver.NewWebsocketManager(routes, codec)
	smux.HandleFunc("/queryws", wm.WebsocketHandler)

	topMux := http.NewServeMux()
	topMux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Access-Control-Allow-Headers", "true")
		if req.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		smux.ServeHTTP(w, req)
	})

	_, err := rpcserver.StartHTTPServer(s.Host, topMux, s.Logger)
	if err != nil {
		return err
	}
	return nil
}

func (s *QueryServer) RunForever() {
	tmcmn.TrapSignal(func() {
		// cleanup
	})
}

// The contract parameter should be a hex-encoded local address prefixed by 0x
func (s *QueryServer) queryRoute(contract string, query []byte) ([]byte, error) {
	vm := &lcp.PluginVM{
		Loader: s.Loader,
		State:  s.StateProvider.ReadOnlyState(),
	}
	req := &plugin.Request{
		ContentType: plugin.EncodingType_PROTOBUF3,
		Accept:      plugin.EncodingType_PROTOBUF3,
		Body:        query,
	}
	reqBytes, err := proto.Marshal(req)
	if err != nil {
		return nil, err
	}
	var caller loom.Address
	localContractAddr, err := decodeHexAddress(contract)
	if err != nil {
		return nil, err
	}
	contractAddr := loom.Address{
		ChainID: s.ChainID,
		Local:   localContractAddr,
	}
	respBytes, err := vm.StaticCall(caller, contractAddr, reqBytes)
	if err != nil {
		return nil, err
	}
	resp := &plugin.Response{}
	err = proto.Unmarshal(respBytes, resp)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (s *QueryServer) nonceRoute(key string) (uint64, error) {
	k, err := hex.DecodeString(key)
	if err != nil {
		return 0, err
	}
	addr := loom.Address{
		ChainID: s.ChainID,
		Local:   loom.LocalAddressFromPublicKey(k),
	}
	return auth.Nonce(s.StateProvider.ReadOnlyState(), addr), nil
}

func decodeHexAddress(s string) ([]byte, error) {
	if !strings.HasPrefix(s, "0x") {
		return nil, errors.New("string has no hex prefix")
	}

	return hex.DecodeString(s[2:])
}
