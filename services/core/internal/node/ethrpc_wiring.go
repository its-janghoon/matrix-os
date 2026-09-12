package node

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/ecirlabs/matrix-core/internal/consensus"
	"github.com/ecirlabs/matrix-core/internal/ethrpc"
	"github.com/ecirlabs/matrix-core/internal/token"
	"github.com/ecirlabs/matrix-core/internal/version"
)

// Wiring the Ethereum JSON-RPC surface to the consensus engine.
//
// The adapter is the one place that knows how this chain's vocabulary and
// Ethereum's line up, which keeps internal/ethrpc free of consensus types and
// keeps the engine free of any idea that a wallet exists.

// ethRPCBackend adapts the consensus engine to what internal/ethrpc asks for.
type ethRPCBackend struct {
	engine  *consensus.Engine
	chainID uint64
}

func (b *ethRPCBackend) ChainHeight() uint64 { return b.engine.Height() }

func (b *ethRPCBackend) Balance(accountID string) (uint64, error) {
	return b.engine.Ledger().Balance(accountID)
}

func (b *ethRPCBackend) NextNonce(accountID string, pending bool) uint64 {
	return b.engine.NextNonce(accountID, pending)
}

// SubmitRaw turns a wallet's signed envelope into a chain transaction and offers
// it to consensus. The id is returned only once the transaction has been
// ADMITTED: a wallet handed an id for something that was refused shows it
// pending forever, and the user has no way to learn it never existed.
func (b *ethRPCBackend) SubmitRaw(raw []byte) ([]byte, error) {
	tx, err := token.NewTransactionFromEVM(raw, b.chainID)
	if err != nil {
		return nil, err
	}
	if err := b.engine.Submit(tx); err != nil {
		return nil, err
	}
	return tx.EVMHash()
}

func (b *ethRPCBackend) BlockByHeight(height uint64) (*ethrpc.BlockView, bool) {
	block, err := b.engine.Chain().BlockAt(height)
	if err != nil || block == nil {
		return nil, false
	}
	return &ethrpc.BlockView{
		Height:     block.Height,
		Hash:       block.Hash(),
		ParentHash: block.PrevBlockHash,
		Timestamp:  block.Timestamp,
		ProposerID: block.ProposerID,
		Txs:        block.Txs,
	}, true
}

func (b *ethRPCBackend) BlockByHash(hash []byte) (*ethrpc.BlockView, bool) {
	height, ok := b.engine.BlockHeightByHash(hex.EncodeToString(hash))
	if !ok {
		return nil, false
	}
	return b.BlockByHeight(height)
}

// TransactionByHash answers from committed state first and the mempool second.
// A wallet polls within a second of sending, long before the transaction
// commits, and answering "unknown" there is indistinguishable from "dropped".
func (b *ethRPCBackend) TransactionByHash(hashHex string) (ethrpc.TxLookup, bool) {
	if tx, at, ok := b.engine.EVMTransaction(hashHex); ok {
		applied, known := b.engine.TransferApplied(tx)
		return ethrpc.TxLookup{
			Tx:           tx,
			Committed:    true,
			Height:       at.Height,
			Index:        at.Index,
			Applied:      applied,
			AppliedKnown: known,
		}, true
	}
	if tx, ok := b.engine.PendingEVMTransaction(hashHex); ok {
		return ethrpc.TxLookup{Tx: tx}, true
	}
	return ethrpc.TxLookup{}, false
}

// startEthRPC brings up the JSON-RPC listener a wallet connects to.
//
// It is off unless both a chain id and an address are configured. Either alone
// is a half-configured surface: an address with no chain id would serve a wallet
// an endpoint it cannot safely send to, and a chain id with no address is simply
// the transaction rule, which stands on its own.
func (n *Node) startEthRPC() error {
	addr := n.config.EthRPC.Addr
	if addr == "" || addr == "off" {
		return nil
	}
	if n.config.Consensus.ChainID == 0 {
		return fmt.Errorf("eth_rpc.addr is set but consensus.chain_id is not: a wallet would be " +
			"offered an endpoint on a chain that accepts no wallet-signed transaction")
	}

	handler, err := ethrpc.NewHandler(ethrpc.Config{
		ChainID:       n.config.Consensus.ChainID,
		Backend:       &ethRPCBackend{engine: n.consensus, chainID: n.config.Consensus.ChainID},
		ClientVersion: "matrix-os/" + version.String(),
	})
	if err != nil {
		return fmt.Errorf("failed to create the ethereum RPC handler: %w", err)
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen for ethereum RPC on %s: %w", addr, err)
	}
	n.ethRPCServer = &http.Server{
		Handler: withEthRPCCORS(handler, n.config.EthRPC.AllowedOrigins),
		// A wallet poll is small and quick; these bound a stuck client rather
		// than shaping any legitimate request.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		if err := n.ethRPCServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Printf("ethereum RPC server stopped: %v\n", err)
		}
	}()
	fmt.Printf("Ethereum JSON-RPC listening on %s for chain id %d.\n", addr, n.config.Consensus.ChainID)
	return nil
}

// stopEthRPC shuts the listener down.
func (n *Node) stopEthRPC(ctx context.Context) {
	if n.ethRPCServer == nil {
		return
	}
	_ = n.ethRPCServer.Shutdown(ctx)
}

// withEthRPCCORS applies the browser policy.
//
// Deny by default, exactly as the Connect surface does. A wallet EXTENSION sends
// no Origin this policy would gate - it proxies the request itself - so the list
// only ever matters for a web page calling the endpoint directly, and a wide
// policy there lets any page the user visits read the chain through their node.
func withEthRPCCORS(next http.Handler, allowed []string) http.Handler {
	permitted := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		permitted[o] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			_, ok := permitted[origin]
			if !ok {
				_, ok = permitted["*"]
			}
			if !ok {
				http.Error(w, "origin is not allowed to call this endpoint", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
