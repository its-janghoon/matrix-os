package node

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/ecirlabs/matrix-core/internal/bridge"
	"github.com/ecirlabs/matrix-core/internal/consensus"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/marketapi"
	"github.com/ecirlabs/matrix-core/internal/token"
	"net/url"
)

// This file wires the lock-and-mint bridge into matrixd as a node subsystem.
//
// Before this wiring the burn->unlock relayer existed only as a library type
// (internal/bridge.Watcher) driven out of process by cmd/bridge-watch against a
// throwaway ledger. That is fine for a demo but wrong for a deployment: the
// unlock must land on the SAME ledger that holds the locks, otherwise escrow
// accounting and the 1:1 backing invariant are split across two ledgers. Here
// the node constructs the Bridge over its own market ledger and KV store and
// (when configured) has the Watcher submit burn observations to consensus, so
// the committed release lands on every node's real ledger and Bridge.Reconcile
// remains a meaningful check.
//
// AUTHORITY. The Watcher is a per-node polling relayer: it is the thing with an
// Ethereum endpoint, and nothing about running it inside matrixd makes what it
// OBSERVES agreed. Every configured daemon bridge is therefore consensus-ordered,
// including a singleton. The watcher submits an attestation and the engine
// releases escrow on the block where attesting voting power crosses quorum; it
// never calls the direct release path. This remains safe when dynamic membership
// grows a singleton into a validator set. See internal/consensus/burnunlock.go
// for the encoding and tally. The bridge refuses a bypass with
// ErrConsensusOrdered rather than silently diverging.

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

	// AttestorKeystore is the path to this validator's encrypted secp256k1
	// attestor key. Empty means this node cannot attest: it still applies locks
	// and unlocks like any other node, it just produces no mint authorizations.
	//
	// It is a KEYSTORE rather than a hex string in this file because the key is
	// unilateral authority to mint wrapped tokens against escrow. The same
	// scrypt-and-GCM format an account key gets, for the same reason, and the
	// repo's standing rule holds: no real key is committed and the passphrase
	// comes from the environment.
	//
	// Generate one with `matrix bridge attestor-new`. Its ADDRESS is what goes
	// in the contract's registered attestor set; a node whose key is not
	// registered signs attestations the contract rejects.
	AttestorKeystore string `yaml:"attestor_keystore"`
}

// AttestorPassphraseEnv is where the attestor keystore's passphrase is read
// from. It is env-only and never a config field, matching how the wallet
// passphrase is handled: a secret in the file is a secret in every backup of the
// file.
const AttestorPassphraseEnv = "MATRIX_ATTESTOR_PASSPHRASE"

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

// newConfiguredBridge builds matrixd's Bridge from cfg over the given ledger
// and store, or returns (nil, nil) when no bridge is configured. Every configured
// daemon bridge is consensus-ordered, including a singleton: validator membership
// is dynamic chain state, so selecting direct mode from the startup set could
// leave burn release outside consensus after the set grows. bridge.New remains
// available only to library callers and explicit test helpers.
func newConfiguredBridge(ledger *market.Ledger, store *kv.Store, cfg BridgeConfig) (*bridge.Bridge, error) {
	return newConfiguredBridgeForSet(ledger, store, cfg, 0)
}

// newConfiguredBridgeForSet retains the old test seam, but validator count no
// longer selects authority. It always returns the same consensus-ordered bridge
// matrixd uses so a singleton and a multi-validator set cannot drift in mode.
func newConfiguredBridgeForSet(ledger *market.Ledger, store *kv.Store, cfg BridgeConfig, validators int) (*bridge.Bridge, error) {
	_ = validators
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
	return bridge.NewConsensusOrdered(ledger, store, params), nil
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

// loadAttestor unlocks this node's attestor key, if one is configured.
//
// A configured-but-unusable key is a STARTUP failure rather than a warning. The
// alternative is a validator that looks like it is attesting and is not, which
// on a threshold bridge means mints silently stop reaching quorum with nothing
// pointing at the node responsible.
func loadAttestor(cfg BridgeConfig) (*bridge.Attestor, error) {
	if cfg.AttestorKeystore == "" {
		return nil, nil
	}
	pass := os.Getenv(AttestorPassphraseEnv)
	if pass == "" {
		return nil, fmt.Errorf("bridge.attestor_keystore is set but %s is empty; the passphrase "+
			"is read from the environment so it is not in the config file or its backups",
			AttestorPassphraseEnv)
	}
	att, err := bridge.LoadAttestorKeystore(cfg.AttestorKeystore, pass)
	if err != nil {
		return nil, fmt.Errorf("unlock attestor keystore: %w", err)
	}
	return att, nil
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

func (a bridgeLockAdapter) RecordLock(ltx market.LedgerTx, lockID [32]byte, from string, recipient [20]byte, nativeAmount uint64) error {
	return a.bridge.RecordLock(ltx, lockID, from, bridge.Address(recipient), nativeAmount)
}

func (a bridgeLockAdapter) EscrowAccount() string { return a.bridge.EscrowAccount() }

// bridgeReadinessAdapter signs fresh deployment-readiness challenges with the
// same live secp256k1 key whose address the contract must register, but under a
// digest domain that can never be reused as a mint authorization.
type bridgeReadinessAdapter struct {
	bridge   *bridge.Bridge
	attestor *bridge.Attestor
}

func (a bridgeReadinessAdapter) SignBridgeReadiness(challenge []byte) (*marketapi.BridgeReadinessProof, error) {
	if len(challenge) != bridge.ReadinessChallengeLen {
		return nil, fmt.Errorf("bridge: readiness challenge is %d bytes, want %d", len(challenge), bridge.ReadinessChallengeLen)
	}
	params := a.bridge.Params()
	if params.ChainID == nil || !params.ChainID.IsUint64() || params.ChainID.Sign() <= 0 {
		return nil, fmt.Errorf("bridge: readiness chain id is not a positive uint64")
	}
	var nonce [bridge.ReadinessChallengeLen]byte
	copy(nonce[:], challenge)
	sig, err := bridge.SignReadiness(a.attestor, nonce, params, token.MinBridgeLockAmount)
	if err != nil {
		return nil, err
	}
	return &marketapi.BridgeReadinessProof{
		ChainID:       params.ChainID.Uint64(),
		Contract:      params.BridgeContract.Hex(),
		Attestor:      a.attestor.AddressHex(),
		MinLockNative: token.MinBridgeLockAmount,
		Challenge:     append([]byte(nil), challenge...),
		Signature:     sig,
	}, nil
}

func bridgeReadinessFor(b *bridge.Bridge, att *bridge.Attestor) marketapi.BridgeReadinessSigner {
	if b == nil || att == nil {
		return nil
	}
	return bridgeReadinessAdapter{bridge: b, attestor: att}
}

// lockAttestorAdapter signs mint authorizations for committed locks. It is the
// marketapi.LockAttestor half of the split that keeps the market API free of an
// internal/bridge import, the same shape bridgeReconciler uses.
type lockAttestorAdapter struct {
	bridge   *bridge.Bridge
	attestor *bridge.Attestor
}

// AttestLock looks the lock up in the bridge's own records and signs the
// canonical digest with this node's attestor key.
//
// The lock has to be COMMITTED for this to find it: consensus applies the escrow
// move and records the event, and only then is there anything to attest to. A
// client that asks too early gets a not-found, which is the honest answer -
// signing an authorization for value that is not yet in escrow is precisely what
// the 1:1 backing forbids.
func (a lockAttestorAdapter) AttestLock(lockID []byte) (*marketapi.LockAttestation, error) {
	var id [bridge.LockIDLen]byte
	if len(lockID) != len(id) {
		return nil, fmt.Errorf("bridge: lock id is %d bytes, want %d", len(lockID), len(id))
	}
	copy(id[:], lockID)

	ev, err := a.bridge.GetLock(id)
	if err != nil {
		return nil, err
	}
	att, err := a.bridge.Attest(ev, []bridge.ValidatorSigner{{Label: "self", Attestor: a.attestor}})
	if err != nil {
		return nil, err
	}
	if len(att.Signatures) != 1 {
		return nil, fmt.Errorf("bridge: expected one signature from this node, got %d",
			len(att.Signatures))
	}
	return &marketapi.LockAttestation{
		Recipient:    "0x" + hex.EncodeToString(ev.Recipient[:]),
		ERC20Amount:  ev.ERC20Amount(),
		NativeAmount: ev.NativeAmount,
		Signature:    att.Signatures[0],
		Attestor:     a.attestor.AddressHex(),
	}, nil
}

// lockAttestorFor returns the signer for GetLockAttestation, or a nil INTERFACE
// when this node cannot attest. Both halves are required: the bridge holds the
// lock records and the attestor holds the key, and a node with one but not the
// other must refuse rather than half-answer.
func lockAttestorFor(b *bridge.Bridge, att *bridge.Attestor) marketapi.LockAttestor {
	if b == nil || att == nil {
		return nil
	}
	return lockAttestorAdapter{bridge: b, attestor: att}
}

// redactRPCURL keeps the scheme and host of an endpoint and drops everything
// after it.
//
// Provider URLs carry the credential in the PATH - an Alchemy or Infura
// endpoint is https://<host>/v2/<api key> - so printing one at startup writes
// that key into the node log verbatim, and node logs get shipped to
// aggregators, tailed in screen shares, and pasted into bug reports. The host
// is the part an operator actually needs to see to know which provider they are
// pointed at.
func redactRPCURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		// Unparseable, so nothing can be assumed about where the secret is.
		return "(redacted)"
	}
	out := u.Scheme + "://" + u.Host
	if u.Path != "" && u.Path != "/" {
		out += "/..."
	}
	if u.RawQuery != "" {
		out += "?..."
	}
	if u.User != nil {
		// Userinfo carries credentials too.
		out = u.Scheme + "://(redacted)@" + u.Host + "/..."
	}
	return out
}
