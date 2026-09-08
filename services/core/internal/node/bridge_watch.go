package node

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/ecirlabs/matrix-core/internal/bridge"
	"github.com/ecirlabs/matrix-core/internal/consensus"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/marketapi"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// This file wires the lock-and-mint bridge into matrixd as a node subsystem.
//
// Before this wiring the burn->unlock relayer existed only as a library type
// (internal/bridge.Watcher) driven out of process by cmd/bridge-watch against a
// throwaway ledger. That is fine for a demo but wrong for a deployment: the
// unlock must land on the SAME ledger that holds the locks, otherwise escrow
// accounting and the 1:1 backing invariant are split across two ledgers. Here
// the node constructs the Bridge over its own market ledger and KV store and
// (when configured) runs the Watcher against it, so a burn observed on Ethereum
// releases escrowed native MATRIX on the node's real ledger and
// Bridge.Reconcile is a meaningful check.
//
// AUTHORITY. The Watcher is a per-node polling relayer: it is the thing with an
// Ethereum endpoint, and nothing about running it inside matrixd makes what it
// OBSERVES agreed. What the observation is allowed to DO now depends on the size
// of the validator set, and that is decided here rather than by the operator.
//
// On a single-node network the watcher applies the escrow release itself. One
// ledger, one authority, nothing to diverge.
//
// On a validator SET it may not, because the release would land on one node's
// ledger and nowhere else, splitting the escrow balance and the 1:1 backing
// invariant across nodes. So a set gets a consensus-ordered bridge
// (bridge.NewConsensusOrdered): the watcher SUBMITS AN ATTESTATION, and the
// engine releases escrow on the block where attesting voting power crosses
// quorum. See internal/consensus/burnunlock.go for the encoding and the tally.
// The bridge in that mode refuses a direct release outright, so a caller that
// bypasses the attestation path gets ErrConsensusOrdered rather than a silent
// divergence.
//
// This paragraph used to say the opposite - that the unlock was "still applied
// by whichever node runs the watcher" and that making it consensus-ordered was
// "a separate, larger design change". That was true when it was written and
// false once newConfiguredBridgeForSet landed below it. A comment that
// contradicts the code it heads is worse than none, because it is the thing a
// reader trusts instead of reading on.

// defaultBridgeConfirmations is the confirmation depth used when the operator
// does not set one. It is deliberately conservative rather than zero: a node
// pointed at a real Ethereum endpoint must not release escrow on a block that
// can still be reorged away. Set confirmations explicitly to 0 for a local
// dev chain with instant finality (hardhat).
const defaultBridgeConfirmations = uint64(12)

// BridgeConfig configures the node's lock-and-mint bridge subsystem. The bridge
// is opt-in: with no contract configured the node runs exactly as before and
// GetBridge returns nil.
type BridgeConfig struct {
	// Contract is the deployed WrappedMatrix contract address (0x-prefixed hex).
	// It is required to enable the bridge: every attestation the bridge signs is
	// bound to this address, so there is no sane default and an unset value
	// leaves the subsystem off rather than binding attestations to the zero
	// address.
	Contract string `yaml:"contract"`
	// ChainID is the EVM chain id the contract is deployed on. Like Contract it
	// is bound into every attestation digest, so a wrong value produces
	// signatures the contract rejects. Required when Contract is set.
	ChainID int64 `yaml:"chain_id"`
	// Watch configures the always-on burn->unlock watcher.
	Watch BridgeWatchConfig `yaml:"watch"`
}

// BridgeWatchConfig configures the in-node burn->unlock watcher: an always-on
// poller that pulls WrappedMatrix `Burned` events off an Ethereum JSON-RPC
// endpoint and releases the matching escrowed native MATRIX on this node's
// ledger, exactly once per burn.
type BridgeWatchConfig struct {
	// Enabled turns the watcher on. It requires the parent BridgeConfig to have
	// a Contract and ChainID, plus an RPCURL here; a config that enables the
	// watcher without them is rejected at startup rather than silently ignored.
	Enabled bool `yaml:"enabled"`
	// RPCURL is the Ethereum JSON-RPC endpoint to poll (e.g.
	// http://127.0.0.1:8545 for a local hardhat node, or a provider URL).
	RPCURL string `yaml:"rpc_url"`
	// StartBlock is the earliest block to scan, normally the block the
	// WrappedMatrix contract was deployed in. Scanning resumes from the persisted
	// cursor when it is ahead of this value.
	StartBlock uint64 `yaml:"start_block"`
	// Confirmations is how many blocks behind head the watcher stays before
	// treating a block as final. Unset defaults to defaultBridgeConfirmations;
	// set it explicitly to 0 only on a chain with instant finality.
	Confirmations *uint64 `yaml:"confirmations"`
	// PollInterval is how often to poll once caught up to head. Zero uses the
	// watcher default (2s). Accepts a Go duration string, e.g. "12s".
	PollInterval time.Duration `yaml:"poll_interval"`
	// MaxBlockSpan bounds the blocks scanned per eth_getLogs call so a large
	// backlog is chunked. Zero uses the watcher default (2000).
	MaxBlockSpan uint64 `yaml:"max_block_span"`
}

// confirmations resolves the configured confirmation depth, applying the
// conservative default when the operator left it unset.
func (c BridgeWatchConfig) confirmations() uint64 {
	if c.Confirmations == nil {
		return defaultBridgeConfirmations
	}
	return *c.Confirmations
}

// bridgeEnabled reports whether the operator configured a bridge at all.
func (c BridgeConfig) bridgeEnabled() bool { return c.Contract != "" }

// newConfiguredBridge builds the node's Bridge from cfg over the given ledger
// and store, or returns (nil, nil) when no bridge is configured. It validates
// the contract address and chain id up front so a misconfigured deployment
// fails at startup rather than producing attestations no contract will accept.
func newConfiguredBridge(ledger *market.Ledger, store *kv.Store, cfg BridgeConfig) (*bridge.Bridge, error) {
	return newConfiguredBridgeForSet(ledger, store, cfg, 1)
}

// newConfiguredBridgeForSet is newConfiguredBridge with the validator count,
// which decides who is allowed to apply an unlock.
//
// On a single-node network the watcher applies the escrow release itself, which
// is correct there: one ledger, one authority. On a validator SET that would
// move collateral on one node's ledger and nowhere else, splitting the escrow
// balance and the 1:1 backing invariant across nodes - the same divergence that
// made FundAccount unsafe, except this one moves real collateral rather than
// reward-pool allocation. So a set gets a consensus-ordered bridge: the watcher
// attests, and escrow is released by the engine once a quorum agrees.
func newConfiguredBridgeForSet(ledger *market.Ledger, store *kv.Store, cfg BridgeConfig, validators int) (*bridge.Bridge, error) {
	if !cfg.bridgeEnabled() {
		if cfg.Watch.Enabled {
			return nil, fmt.Errorf("bridge: watch is enabled but bridge.contract is not set")
		}
		return nil, nil
	}
	contract, err := bridge.ParseAddress(cfg.Contract)
	if err != nil {
		return nil, fmt.Errorf("bridge: invalid bridge.contract: %w", err)
	}
	if cfg.ChainID <= 0 {
		return nil, fmt.Errorf("bridge: bridge.chain_id must be a positive EVM chain id, got %d", cfg.ChainID)
	}
	params := bridge.AttestationParams{
		ChainID:        big.NewInt(cfg.ChainID),
		BridgeContract: contract,
	}
	if validators > 1 {
		return bridge.NewConsensusOrdered(ledger, store, params), nil
	}
	return bridge.New(ledger, store, params), nil
}

// The consensus-ordered unlock is wired as TWO small adapters rather than one,
// because one would be a dependency cycle: the engine needs something that can
// release escrow, and the thing that submits attestations needs the engine.
// Splitting them by direction breaks it - each half needs only what already
// exists when it is built.

// attestedUnlockTranslator is the consensus.BurnUnlocker half: the engine calls
// it once a quorum has attested, from inside its own ledger critical section.
// It needs only the bridge, so it can be handed to the engine at construction.
//
// Its whole job beyond delegation is translating the bridge's
// already-processed sentinel into the one the consensus interface contract
// names. That is what lets the engine tell a late attestation (ordinary, and a
// no-op) from a real failure without importing the bridge or matching on error
// text.
type attestedUnlockTranslator struct {
	bridge *bridge.Bridge
}

func (t attestedUnlockTranslator) ApplyAttestedUnlock(ltx market.LedgerTx, burnIDHash, toAccount string, native uint64) error {
	err := t.bridge.ApplyAttestedUnlock(ltx, burnIDHash, toAccount, native)
	if errors.Is(err, bridge.ErrBurnAlreadyProcessed) {
		return fmt.Errorf("%w: %s", consensus.ErrBurnAlreadyReleased, burnIDHash)
	}
	return err
}

// consensusAttestingApplier is the bridge.Applier half, given to the watcher on
// a validator set: instead of releasing escrow, it submits this node's
// attestation that the burn happened. Escrow moves when a quorum of the set has
// attested to the same burn, account and amount - never on this node's word
// alone.
type consensusAttestingApplier struct {
	engine *consensus.Engine
}

func (a consensusAttestingApplier) ProcessBurn(burn bridge.BurnEvent) error {
	native, err := token.ERC20ToNative(burn.ERC20Amount)
	if err != nil {
		return err
	}
	_, err = a.engine.SubmitBurnAttestation(consensus.BurnUnlock{
		BurnIDHash:   bridge.BurnIDHash(burn.ID),
		ToAccount:    burn.ToAccount,
		NativeAmount: native,
	})
	return err
}

// newConfiguredBridgeWatcher builds the burn->unlock Watcher for the configured
// bridge, persisting its scan cursor in the node's KV store so a matrixd restart
// resumes where it stopped instead of re-scanning the whole range from
// start_block. It returns (nil, nil) when the watcher is not enabled.
//
// The Ethereum client is injected rather than dialed here so the same wiring the
// node runs can be driven by a fake client in tests.
func newConfiguredBridgeWatcher(
	b bridge.Applier,
	store bridge.CursorStore,
	client bridge.EthClient,
	cfg BridgeConfig,
	onBurn func(bridge.DecodedBurn),
	onError func(error),
) (*bridge.Watcher, error) {
	if !cfg.Watch.Enabled {
		return nil, nil
	}
	if b == nil {
		return nil, fmt.Errorf("bridge: watch is enabled but no bridge is configured")
	}
	if client == nil {
		return nil, fmt.Errorf("bridge: watch is enabled but bridge.watch.rpc_url is not set")
	}
	contract, err := bridge.ParseAddress(cfg.Contract)
	if err != nil {
		return nil, fmt.Errorf("bridge: invalid bridge.contract: %w", err)
	}
	return bridge.NewWatcher(bridge.WatcherConfig{
		Client:        client,
		Bridge:        b,
		Contract:      contract,
		StartBlock:    cfg.Watch.StartBlock,
		Confirmations: cfg.Watch.confirmations(),
		PollInterval:  cfg.Watch.PollInterval,
		MaxBlockSpan:  cfg.Watch.MaxBlockSpan,
		// Persist the cursor under a key namespaced by contract address so a store
		// that ever hosts watchers for two deployments keeps their cursors apart.
		Store:     store,
		CursorKey: "bridge/watch_cursor/" + contract.Hex(),
		OnBurn:    onBurn,
		OnError:   onError,
	})
}

// dialBridgeClient builds the Ethereum JSON-RPC client for the configured
// watcher endpoint, or returns nil when the watcher is disabled. An enabled
// watcher with no endpoint is an error, not a silent no-op.
func dialBridgeClient(cfg BridgeConfig) (bridge.EthClient, error) {
	if !cfg.Watch.Enabled {
		return nil, nil
	}
	if cfg.Watch.RPCURL == "" {
		return nil, fmt.Errorf("bridge: watch is enabled but bridge.watch.rpc_url is not set")
	}
	return bridge.NewHTTPEthClient(cfg.Watch.RPCURL, nil), nil
}

// heightSource reports the committed consensus height, so the reconciliation
// snapshot can be anchored to a stated block. It is the narrow read the
// reconciler needs from the consensus engine (satisfied by *consensus.Engine
// via Height()), declared here so bridgeReconciler does not depend on the whole
// engine surface and a test can supply a fixed height.
type heightSource interface {
	// Height returns the committed chain length (the number of blocks committed
	// so far), which is the height the snapshot reflects.
	Height() uint64
}

// bridgeReconciler adapts the node's *bridge.Bridge to marketapi.Reconciler: it
// runs Bridge.Reconcile and stamps the resulting snapshot with the committed
// consensus height, so GetBridgeReconciliation returns a report anchored to a
// stated, reproducible point. It is the seam that keeps internal/marketapi free
// of an internal/bridge import (which would cycle through internal/node).
type bridgeReconciler struct {
	bridge *bridge.Bridge
	height heightSource
}

// newBridgeReconciler builds the adapter, or returns nil when no bridge is
// configured so the market service leaves GetBridgeReconciliation reporting
// FailedPrecondition rather than serving an empty snapshot. A nil height source
// is tolerated (the snapshot's block height is reported as 0) so the reconciler
// still works on a node wired without consensus.
func newBridgeReconciler(b *bridge.Bridge, h heightSource) *bridgeReconciler {
	if b == nil {
		return nil
	}
	return &bridgeReconciler{bridge: b, height: h}
}

// Reconcile implements marketapi.Reconciler. It surfaces the same escrow /
// accounting mismatch error Bridge.Reconcile raises (which the handler maps to
// codes.Internal) and, on success, copies the snapshot into the marketapi shape
// with the committed height stamped in.
func (r *bridgeReconciler) Reconcile() (*marketapi.BridgeSnapshot, error) {
	snap, err := r.bridge.Reconcile()
	if err != nil {
		return nil, err
	}
	var height uint64
	if r.height != nil {
		height = r.height.Height()
	}
	return &marketapi.BridgeSnapshot{
		LockedNative:      snap.LockedNative,
		UnlockedNative:    snap.UnlockedNative,
		OutstandingNative: snap.OutstandingNative,
		EscrowBalance:     snap.EscrowBalance,
		OutstandingERC20:  snap.OutstandingERC20,
		BlockHeight:       height,
	}, nil
}

// runBridgeWatcher starts the watcher's polling loop in a goroutine and returns
// a channel closed when that loop has exited. Cancelling ctx stops the loop, so
// Stop cancels the node context and then waits on this channel: the watcher is
// joined rather than leaked, which matters because it writes to the ledger and
// KV store the node is about to close.
func runBridgeWatcher(ctx context.Context, w *bridge.Watcher, onExit func(error)) chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := w.Run(ctx)
		if onExit != nil {
			onExit(err)
		}
	}()
	return done
}

// bridgeLockerFor adapts the bridge to the consensus engine's BridgeLocker, and
// returns a nil INTERFACE when there is no bridge.
//
// The explicit nil matters: a nil *bridge.Bridge stored in an interface is not
// itself nil, so returning the pointer directly would give the engine a
// non-nil-looking locker that panics on first use. The bridge builder has the
// same trap documented at its own call site.
func bridgeLockerFor(b *bridge.Bridge) consensus.BridgeLocker {
	if b == nil {
		return nil
	}
	return bridgeLockAdapter{bridge: b}
}

// bridgeLockAdapter translates the engine's plain [20]byte address into the
// bridge's Address type. It exists so consensus does not import the bridge
// package - the same split the burn unlock uses in the other direction.
type bridgeLockAdapter struct{ bridge *bridge.Bridge }

func (a bridgeLockAdapter) RecordLock(lockID [32]byte, from string, recipient [20]byte, nativeAmount uint64) error {
	return a.bridge.RecordLock(lockID, from, bridge.Address(recipient), nativeAmount)
}

func (a bridgeLockAdapter) EscrowAccount() string { return a.bridge.EscrowAccount() }
