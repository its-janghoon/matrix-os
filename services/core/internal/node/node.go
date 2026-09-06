package node

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/ecirlabs/matrix-core/internal/admin"
	"github.com/ecirlabs/matrix-core/internal/agent"
	"github.com/ecirlabs/matrix-core/internal/consensus"
	"github.com/ecirlabs/matrix-core/internal/inference"
	"github.com/ecirlabs/matrix-core/internal/inferenceapi"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/marketapi"
	"github.com/ecirlabs/matrix-core/internal/marketexchange"
	"github.com/ecirlabs/matrix-core/internal/matrix"
	"github.com/ecirlabs/matrix-core/internal/metrics"
	"github.com/ecirlabs/matrix-core/internal/p2p"
	"github.com/ecirlabs/matrix-core/internal/soul"
	"github.com/ecirlabs/matrix-core/internal/token"
	"github.com/ecirlabs/matrix-core/internal/transport"
	"gopkg.in/yaml.v3"
)

// Config represents the node configuration
type Config struct {
	Network struct {
		ListenAddr     string   `yaml:"listen_addr"`
		BootstrapPeers []string `yaml:"bootstrap_peers"`
	} `yaml:"network"`
	Storage struct {
		Engine string `yaml:"engine"`
		Path   string `yaml:"path"`
	} `yaml:"storage"`
	Security struct {
		EnableACLs          bool `yaml:"enable_acls"`
		AllowUnsignedAgents bool `yaml:"allow_unsigned_agents"`
	} `yaml:"security"`
	Admin struct {
		Addr string `yaml:"addr"`
	} `yaml:"admin"`
	Market struct {
		Addr string `yaml:"addr"`
	} `yaml:"market"`
	Inference struct {
		// Addr is the TCP listen address for the inference gRPC API
		// (matrix.inference.v1.InferenceService). It runs as a parallel gRPC
		// server to the market API, gated by the same EnableACLs auth. Default
		// 0.0.0.0:9092.
		Addr string `yaml:"addr"`
		// EchoProvider, when non-empty, registers the GPU-free deterministic echo
		// backend for this provider ID at startup so a fresh node can fulfill
		// inference jobs locally without a GPU or a model server. It is the local
		// demo provider; a real deployment registers a local-http or provider-API
		// backend instead. When empty, no backend is auto-registered and providers
		// must be registered out of band via GetInference().Registry().
		EchoProvider string `yaml:"echo_provider"`
	} `yaml:"inference"`
	Consensus struct {
		// Validators is the fixed validator set as hex-encoded account IDs
		// (ed25519 public keys). This node's own consensus identity is always
		// added to the set, so an empty list yields a functioning single-validator
		// consensus suitable for a solo/dev node. Every node configured with the
		// same list derives the identical round-robin leader schedule.
		Validators []string `yaml:"validators"`
	} `yaml:"consensus"`
	Genesis GenesisConfig `yaml:"genesis"`
}

// GenesisConfig describes the one-time native MATRIX genesis this node applies
// on first Start. It seeds the initial supply honestly: named allocations credit
// fixed accounts and reward_pool seeds the reserved reward pool
// (token.RewardPoolAccount) that later funds buyer/provider balances via
// FundFromRewardPool. Genesis is applied exactly once and persisted in the KV
// store (see Treasury.ApplyGenesis), so it is idempotent across restarts. The
// sum of allocations plus reward_pool must not exceed NativeMaxSupply (1e18
// native base units).
type GenesisConfig struct {
	// Allocations assigns fixed initial balances (native base units) to named
	// accounts at genesis.
	Allocations []GenesisAllocationConfig `yaml:"allocations"`
	// RewardPool is the amount, in native base units, allocated to the reserved
	// reward pool at genesis. Zero allocates no reward pool.
	RewardPool uint64 `yaml:"reward_pool"`
}

// GenesisAllocationConfig is a single named genesis allocation: Amount native
// base units credited to Account.
type GenesisAllocationConfig struct {
	// Account is the account ID to credit at genesis.
	Account string `yaml:"account"`
	// Amount is the balance to credit, in native base units.
	Amount uint64 `yaml:"amount"`
}

// Node represents a Matrix node instance
type Node struct {
	ctx              context.Context
	cancel           context.CancelFunc
	config           *Config
	p2pHost          *p2p.Host
	transport        *transport.Transport
	eventBus         *transport.EventBus
	kvStore          *kv.Store
	market           *market.Market
	tokenChain       *token.Chain
	treasury         *token.Treasury
	exchange         *marketexchange.Exchange
	consensus        *consensus.Engine
	consensusAccount *token.Account
	metrics          *metrics.Collector
	adminServer      *admin.Server
	marketServer     *marketapi.Server
	inferenceSvc     *inference.Service
	inferenceServer  *inferenceapi.Server
	inferenceAccts   *walletAccounts
	agents           map[string]*agent.Agent
	agentsMu         sync.RWMutex
	souls            map[string]*soul.Soul
	soulsMu          sync.RWMutex
	matrices         map[string]*matrix.Matrix
	matricesMu       sync.RWMutex
}

// Initialize creates a new node configuration
func Initialize(configPath string) error {
	// Create default configuration
	config := &Config{}
	// Write a full libp2p multiaddr (not a bare host:port) so a freshly
	// initialized node boots directly from this config. The p2p layer also
	// accepts the plain host:port form, but the multiaddr form is unambiguous
	// and documents exactly what the node listens on. Loopback keeps a dev node
	// self-contained; operators can widen this to /ip4/0.0.0.0/tcp/9000.
	config.Network.ListenAddr = "/ip4/127.0.0.1/tcp/9000"
	config.Storage.Engine = "pebble"
	config.Storage.Path = "./data"
	config.Security.EnableACLs = true
	config.Security.AllowUnsignedAgents = false
	config.Admin.Addr = "0.0.0.0:9090"
	config.Market.Addr = "0.0.0.0:9091"
	config.Inference.Addr = "0.0.0.0:9092"
	// Register the GPU-free deterministic echo backend for a demo provider so a
	// freshly-initialized node can fulfill inference jobs locally without a GPU
	// or a model server. Operators swap this for a local-http / provider-API
	// backend in production; see internal/inference/registry.go.
	config.Inference.EchoProvider = "demo-inference-provider"
	// Seed a default genesis so a freshly-initialized node establishes real
	// native MATRIX supply on first start. The reward pool holds the full native
	// cap (1e18 base units == 1,000,000,000 whole MATRIX at 9 decimals) so
	// operators can fund buyer/provider accounts out of it via `matrix fund`
	// without any coins being minted past the cap. No named allocations by
	// default (operators add their own). This stays at/under the cap: reward pool
	// == NativeMaxSupply and allocations are empty.
	config.Genesis.RewardPool = token.NativeMaxSupply
	config.Genesis.Allocations = nil

	// Create config directory if it doesn't exist
	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Write config file
	f, err := os.Create(configPath)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	defer f.Close()

	encoder := yaml.NewEncoder(f)
	encoder.SetIndent(2)
	if err := encoder.Encode(config); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// New creates a new Node instance
func New(ctx context.Context, configPath string) (*Node, error) {
	// Load configuration
	config := &Config{}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	if err := yaml.Unmarshal(configData, config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Set defaults if not specified
	if config.Admin.Addr == "" {
		config.Admin.Addr = "0.0.0.0:9090"
	}
	if config.Market.Addr == "" {
		config.Market.Addr = "0.0.0.0:9091"
	}
	if config.Inference.Addr == "" {
		config.Inference.Addr = "0.0.0.0:9092"
	}
	if config.Storage.Path == "" {
		config.Storage.Path = "./data"
	}

	nodeCtx, cancel := context.WithCancel(ctx)

	return &Node{
		ctx:      nodeCtx,
		cancel:   cancel,
		config:   config,
		agents:   make(map[string]*agent.Agent),
		souls:    make(map[string]*soul.Soul),
		matrices: make(map[string]*matrix.Matrix),
	}, nil
}

// Start initializes and starts all node components
func (n *Node) Start() error {
	// Initialize metrics collector
	n.metrics = metrics.New()

	// Initialize event bus
	n.eventBus = transport.NewEventBus()

	// Initialize KV store
	kvStore, err := kv.New(kv.Config{Path: n.config.Storage.Path})
	if err != nil {
		return fmt.Errorf("failed to initialize KV store: %w", err)
	}
	n.kvStore = kvStore

	// Initialize compute marketplace on top of the shared KV store. The market
	// is pure persistence/logic with no goroutines or sockets of its own; the
	// node is the integration point that observes it via metrics.
	mkt, err := market.NewMarket(n.kvStore)
	if err != nil {
		return fmt.Errorf("failed to initialize marketplace: %w", err)
	}
	n.market = mkt
	// The node observes the market through a metrics observer so internal/market
	// never imports internal/metrics. Register the observer, then sync the
	// initial gauges to whatever state was rehydrated from the KV store.
	n.market.SetObserver(newMarketMetricsObserver(n.metrics))
	n.market.SyncMetrics()

	// Initialize the token settlement chain over the same shared KV store. Like
	// the market it is pure persistence/logic with no goroutines or sockets of
	// its own; it persists under the token/* key prefix, disjoint from market/*.
	// It provides the verified, signed, hash-chained settlement path that the
	// marketplace and remote layers use to move compute credits.
	n.tokenChain = token.NewChain(n.kvStore)

	// Initialize the native MATRIX treasury over the market ledger and shared KV
	// store, exactly as the compute-settlement tests do. The treasury owns the
	// honest issuance path: a one-time, cap-enforced genesis allocation plus the
	// reward pool that later funds buyer/provider balances. It is constructed
	// here (right after market + tokenChain) so genesis is applied before any
	// settlement or funding can occur, and so GetTreasury() is available to the
	// market API funding RPC below.
	n.treasury = token.NewTreasury(n.market.Ledger(), n.kvStore)

	// Apply genesis exactly once. ApplyGenesis is idempotent (it records a
	// persisted marker in the KV store via GenesisApplied and is a no-op on a
	// store that already has genesis), so calling it unconditionally on every
	// Start cannot double-credit across restarts. It enforces NativeMaxSupply and
	// writes nothing if the requested supply would exceed the cap.
	allocations := make([]token.GenesisAllocation, 0, len(n.config.Genesis.Allocations))
	for _, a := range n.config.Genesis.Allocations {
		allocations = append(allocations, token.GenesisAllocation{Account: a.Account, Amount: a.Amount})
	}
	alreadyApplied, err := n.treasury.GenesisApplied()
	if err != nil {
		return fmt.Errorf("failed to read genesis state: %w", err)
	}
	if err := n.treasury.ApplyGenesis(allocations, n.config.Genesis.RewardPool); err != nil {
		return fmt.Errorf("failed to apply genesis: %w", err)
	}
	if alreadyApplied {
		fmt.Printf("Genesis already applied; skipping (idempotent).\n")
	} else {
		fmt.Printf("Genesis applied: %d named allocation(s), reward pool %d native base units.\n",
			len(allocations), n.config.Genesis.RewardPool)
	}

	// Initialize P2P host
	p2pHost, err := p2p.New(n.ctx, &p2p.Config{
		ListenAddr: n.config.Network.ListenAddr,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize P2P host: %w", err)
	}
	n.p2pHost = p2pHost

	// Initialize transport
	trans, err := transport.New(n.ctx, transport.Config{
		Host: p2pHost.GetHost(),
	})
	if err != nil {
		return fmt.Errorf("failed to initialize transport: %w", err)
	}
	n.transport = trans

	// Initialize the P2P marketplace exchange on top of the gossip transport and
	// the signed-settlement path. The exchange announces local provider capacity
	// over gossip, discovers remote providers from received announcements, and
	// applies received signed settlements into the local ledger through the token
	// chain (the same verified path SettledLedger enforces for local settlement).
	// It owns background receive loops that terminate when n.ctx is cancelled, so
	// no explicit stop is required beyond cancelling the node context in Stop().
	settled := token.NewSettledLedger(n.market.Ledger(), n.tokenChain)
	exchange, err := marketexchange.New(marketexchange.Config{
		Transport: n.transport,
		Settled:   settled,
		PeerID:    n.p2pHost.GetPeerID().String(),
	})
	if err != nil {
		return fmt.Errorf("failed to initialize marketplace exchange: %w", err)
	}
	if err := exchange.Start(n.ctx); err != nil {
		return fmt.Errorf("failed to start marketplace exchange: %w", err)
	}
	n.exchange = exchange

	// Initialize the global consensus engine over the same gossip transport. It is
	// a fast leader-based BFT ledger: the leader for each round batches signed
	// transfers into a block, validators vote, and on a >2/3 quorum every node
	// commits the block to a hash-linked chain (persisted under consensus/*) and
	// deterministically applies its ordered transactions to the shared market
	// ledger, so all nodes converge to the identical balances. The node's stable
	// consensus identity is persisted so its place in the fixed validator set /
	// round-robin leader schedule survives restarts.
	//
	// SETTLEMENT AUTHORITY (accurate as shipped): consensus is the authoritative
	// native-MATRIX ledger for BOTH marketplace settlement flows. Both the
	// inference marketplace (internal/inference.Service) and the compute
	// marketplace (node.ComputeSettlementCoordinator.SettleAndCompleteJob,
	// built via n.ComputeSettlementCoordinator) submit
	// their buyer -> provider payment as a signed transfer into this engine and
	// confirm it committed AND applied before marking the job done. So the
	// default/production settlement path for compute and inference is consensus,
	// charging the buyer exactly once through the committed, globally-agreed log.
	//
	// Two other writers still touch n.market.Ledger(), and their scope is now
	// explicit rather than "deferred":
	//   1. marketexchange (below) applies signed settlements received over gossip
	//      via token.SettledLedger — the deliberately per-node PAIRWISE path for
	//      cross-node exchange announcements. It is not consensus-ordered.
	//   2. marketapi.SubmitSignedTransfer (below) settles directly through
	//      token.SettledLedger / the token chain — a signed, nonce-checked direct
	//      transfer primitive.
	// These remain distinct primitives (a transfer settled on one is not in the
	// others' dedup/ordering), but they are no longer the path the compute or
	// inference marketplace flows take: those go through consensus (3, this
	// engine). market.CompleteJob's direct ledger.Transfer is likewise retained
	// only for local/test single-node use, not the default flow. Deployments that
	// require a single authoritative ledger drive settlement through consensus,
	// which the marketplace flows now do by default.
	consensusAccount, err := consensus.LoadOrCreateValidatorAccount(n.kvStore)
	if err != nil {
		return fmt.Errorf("failed to load consensus identity: %w", err)
	}
	n.consensusAccount = consensusAccount
	validatorSet, err := consensus.ValidatorSetFromConfig(consensusAccount.PublicKey, n.config.Consensus.Validators)
	if err != nil {
		return fmt.Errorf("failed to build validator set: %w", err)
	}
	consensusEngine, err := consensus.New(consensus.Config{
		Transport:  n.transport,
		Validators: validatorSet,
		Chain:      consensus.NewBlockChain(n.kvStore),
		Ledger:     n.market.Ledger(),
		Self:       consensusAccount,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize consensus engine: %w", err)
	}
	if err := consensusEngine.Start(n.ctx); err != nil {
		return fmt.Errorf("failed to start consensus engine: %w", err)
	}
	n.consensus = consensusEngine

	// Connect to bootstrap peers
	for _, peerAddr := range n.config.Network.BootstrapPeers {
		if err := n.p2pHost.Connect(n.ctx, peerAddr); err != nil {
			// Log but don't fail on bootstrap peer connection errors
			fmt.Printf("Warning: failed to connect to bootstrap peer %s: %v\n", peerAddr, err)
		}
	}

	// Initialize admin server with authentication if enabled
	var apiKeys []*admin.APIKey
	if n.config.Security.EnableACLs {
		// In production, load API keys from secure storage (e.g., HashiCorp Vault)
		// For now, load from environment variable MATRIX_ADMIN_API_KEY
		defaultKey := os.Getenv("MATRIX_ADMIN_API_KEY")
		if defaultKey != "" {
			apiKeys = append(apiKeys, &admin.APIKey{
				Key:  defaultKey,
				Role: admin.RoleAdmin,
				Name: "default-admin",
			})
		}
		// If no keys provided and auth is required, log a warning
		if len(apiKeys) == 0 {
			fmt.Printf("Warning: EnableACLs is true but no API keys configured. Admin server will require auth but no keys are valid.\n")
		}
	}

	adminServer, err := admin.NewServer(admin.Config{
		Addr:        n.config.Admin.Addr,
		RequireAuth: n.config.Security.EnableACLs,
		APIKeys:     apiKeys,
	})
	if err != nil {
		return fmt.Errorf("failed to create admin server: %w", err)
	}
	n.adminServer = adminServer

	// Start admin server
	if err := n.adminServer.Start(n.ctx); err != nil {
		return fmt.Errorf("failed to start admin server: %w", err)
	}

	// Initialize and start the external marketplace gRPC API. It runs as a
	// parallel gRPC server to the admin server (its own configurable addr) so
	// the market surface can bind independently. It is backed by the same real
	// subsystems the node runs: the market order book/ledger, the signed
	// settlement path over the token chain, and the P2P exchange for remote
	// providers.
	//
	// Auth policy (deliberate): the market API mirrors the admin server and gates
	// on the same EnableACLs flag, which defaults to true (see Initialize). When
	// ACLs are on it reuses the admin authenticator so the same API keys gate
	// every non-health RPC, including the state-mutating SubmitSignedTransfer.
	// Disabling ACLs is an explicit operator choice to run the node open (e.g. a
	// local single-tenant dev node); even then SubmitSignedTransfer is not a theft
	// vector because every transfer must carry a valid ed25519 signature from the
	// sender and the sender's correct next nonce, so an unauthenticated client can
	// only move credits it already holds the signing key for. Operators exposing a
	// node to untrusted networks should keep ACLs enabled.
	var marketAuth *admin.Authenticator
	if n.config.Security.EnableACLs {
		marketAuth = n.adminServer.GetAuthenticator()
	}
	marketSettled := token.NewSettledLedger(n.market.Ledger(), n.tokenChain)
	marketServer, err := marketapi.NewServer(marketapi.Config{
		Addr:     n.config.Market.Addr,
		Auth:     marketAuth,
		Market:   n.market,
		Settled:  marketSettled,
		Chain:    n.tokenChain,
		Exchange: n.exchange,
		Funder:   n.treasury,
	})
	if err != nil {
		return fmt.Errorf("failed to create market API server: %w", err)
	}
	n.marketServer = marketServer
	if err := n.marketServer.Start(n.ctx); err != nil {
		return fmt.Errorf("failed to start market API server: %w", err)
	}

	// Initialize and start the inference gRPC API (matrix.inference.v1). It is
	// the LLM-inference counterpart to the market API and runs as a third
	// parallel gRPC server on its own configurable address. It is backed by the
	// same real subsystems the node runs: inference jobs reserve capacity through
	// n.market, run on a provider's registered inference Backend, and settle
	// buyer -> provider through the SAME consensus engine the compute marketplace
	// uses (n.consensus satisfies inference.Settler via SubmitAccountTransfer +
	// WaitForSettlement), so there is one authoritative native-MATRIX ledger for
	// both compute and inference.
	//
	// ACCOUNTS RESOLVER (honest choice): settling an inference job requires the
	// buyer's private key to sign the consensus transfer, so the Service is given
	// an Accounts resolver rather than assuming custody. For a dev/local node the
	// node resolves buyer signing keys from the local wallet directory
	// (~/.matrix by default): a node fulfilling inference on behalf of a buyer it
	// holds the key for. This is exactly the single-operator model the quickstart
	// uses. A multi-tenant deployment supplies its own custodial resolver via
	// GetInference() before serving; the wallet resolver only knows keys that
	// exist as wallet files under its directory, so it never fabricates custody.
	n.inferenceAccts = newWalletAccounts()
	inferenceRegistry := inference.NewRegistry()
	inferenceSvc, err := inference.NewService(inference.Config{
		Market:   n.market,
		Registry: inferenceRegistry,
		Settler:  n.consensus,
		Accounts: n.inferenceAccts,
	})
	if err != nil {
		return fmt.Errorf("failed to create inference service: %w", err)
	}
	n.inferenceSvc = inferenceSvc

	// Register the GPU-free echo backend for the configured demo provider so the
	// node can fulfill inference jobs locally out of the box. This mirrors what
	// the compute marketplace exposes: a provider must also be registered on the
	// order book (via the market API / `matrix provider register`) to reserve
	// capacity; registering the backend here only says "this provider fulfills
	// inference with the echo backend".
	if n.config.Inference.EchoProvider != "" {
		if err := inferenceRegistry.Register(n.config.Inference.EchoProvider, inference.NewEchoBackend()); err != nil {
			return fmt.Errorf("failed to register echo inference backend: %w", err)
		}
		fmt.Printf("Inference: registered echo backend for demo provider %q.\n", n.config.Inference.EchoProvider)
	}

	var inferenceAuth *admin.Authenticator
	if n.config.Security.EnableACLs {
		inferenceAuth = n.adminServer.GetAuthenticator()
	}
	inferenceServer, err := inferenceapi.NewServer(inferenceapi.Config{
		Addr:      n.config.Inference.Addr,
		Auth:      inferenceAuth,
		Inference: n.inferenceSvc,
	})
	if err != nil {
		return fmt.Errorf("failed to create inference API server: %w", err)
	}
	n.inferenceServer = inferenceServer
	if err := n.inferenceServer.Start(n.ctx); err != nil {
		return fmt.Errorf("failed to start inference API server: %w", err)
	}

	// Update metrics
	n.metrics.RecordPeerCount(len(n.p2pHost.GetHost().Network().Peers()))

	return nil
}

// Stop gracefully shuts down all node components
func (n *Node) Stop() error {
	var errs []error

	// Stop all agents
	n.agentsMu.Lock()
	for id, a := range n.agents {
		if err := a.Stop(n.ctx); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop agent %s: %w", id, err))
		}
	}
	n.agentsMu.Unlock()

	// Stop inference API server
	if n.inferenceServer != nil {
		if err := n.inferenceServer.Stop(n.ctx); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop inference API server: %w", err))
		}
	}

	// Stop market API server
	if n.marketServer != nil {
		if err := n.marketServer.Stop(n.ctx); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop market API server: %w", err))
		}
	}

	// Stop admin server
	if n.adminServer != nil {
		if err := n.adminServer.Stop(n.ctx); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop admin server: %w", err))
		}
	}

	// Stop the marketplace exchange before tearing down the transport it reads
	// from. Cancelling the node context (below) closes the transport's
	// subscription channels, which ends the exchange's receive loops; we cancel
	// first, then Wait, so the loops observe cancellation and exit cleanly.
	if n.exchange != nil || n.consensus != nil {
		// Cancelling the node context closes the transport subscription channels,
		// which ends both the exchange and consensus receive/driver loops. Cancel
		// once, then Wait on each so their goroutines observe cancellation and exit
		// cleanly before the transport is torn down.
		n.cancel()
		if n.exchange != nil {
			n.exchange.Wait()
		}
		if n.consensus != nil {
			n.consensus.Wait()
		}
	}

	// Close transport
	if n.transport != nil {
		if err := n.transport.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close transport: %w", err))
		}
	}

	// Close event bus
	if n.eventBus != nil {
		n.eventBus.Close()
	}

	// Close P2P host
	if n.p2pHost != nil {
		if err := n.p2pHost.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close P2P host: %w", err))
		}
	}

	// The marketplace has no background goroutines or sockets to tear down; it
	// persists through the shared kvStore, which is closed just below.

	// Close KV store
	if n.kvStore != nil {
		if err := n.kvStore.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close KV store: %w", err))
		}
	}

	// Cancel context
	if n.cancel != nil {
		n.cancel()
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors during shutdown: %v", errs)
	}

	return nil
}

// GetP2PHost returns the P2P host
func (n *Node) GetP2PHost() *p2p.Host {
	return n.p2pHost
}

// GetTransport returns the transport layer
func (n *Node) GetTransport() *transport.Transport {
	return n.transport
}

// GetEventBus returns the event bus
func (n *Node) GetEventBus() *transport.EventBus {
	return n.eventBus
}

// GetKVStore returns the KV store
func (n *Node) GetKVStore() *kv.Store {
	return n.kvStore
}

// GetMetrics returns the metrics collector
func (n *Node) GetMetrics() *metrics.Collector {
	return n.metrics
}

// GetMarket returns the compute marketplace
func (n *Node) GetMarket() *market.Market {
	return n.market
}

// GetTokenChain returns the token settlement chain: the append-only, hash-chained
// log of ed25519-signed transactions backing signed compute-credit settlement.
func (n *Node) GetTokenChain() *token.Chain {
	return n.tokenChain
}

// GetTreasury returns the native MATRIX treasury: the honest issuance path that
// owns the one-time, cap-enforced genesis allocation and the reward pool that
// funds buyer/provider balances via FundFromRewardPool. It is constructed and
// has genesis applied during Start.
func (n *Node) GetTreasury() *token.Treasury {
	return n.treasury
}

// GetExchange returns the P2P marketplace exchange, which announces local
// capacity over gossip, discovers remote providers, and applies signed
// settlements received from the network into the local ledger.
func (n *Node) GetExchange() *marketexchange.Exchange {
	return n.exchange
}

// GetConsensus returns the global consensus engine: the fast leader-based BFT
// ledger that gives every node an agreed-upon ordered log of settlements. It is
// the authoritative native-MATRIX ledger for the marketplace settlement flows:
// BOTH compute (node.ComputeSettlementCoordinator) and inference
// (inference.Service) settle buyer -> provider through this engine. The marketexchange pairwise path
// and the marketapi token-chain path remain distinct direct primitives on the
// same ledger; see the wiring notes in Start for the authority model.
func (n *Node) GetConsensus() *consensus.Engine {
	return n.consensus
}

// ComputeSettlementCoordinator builds a consensus-backed compute-settlement
// coordinator over this node's market and running consensus engine, using the
// given Accounts resolver to sign buyer transfers. It is the compute-marketplace
// counterpart to the inference Service: a job completed through
// SettleAndCompleteJob pays the provider in native MATRIX through consensus and
// is charged exactly once. It returns an error if the consensus engine is not
// running. The caller supplies the account resolver because the node does not
// assume custody of buyer signing keys.
func (n *Node) ComputeSettlementCoordinator(accounts Accounts) (*ComputeSettlementCoordinator, error) {
	if n.consensus == nil {
		return nil, fmt.Errorf("consensus engine is not running")
	}
	return NewComputeSettlementCoordinator(n.market, n.consensus, accounts)
}

// SettleThroughConsensus is the consensus-backed settlement entrypoint. It
// submits a signed transfer of amount credits from the given account to
// recipient into the global consensus engine; when a committed block includes
// the transaction, every node deterministically reflects it on the market
// ledger. This is the authoritative settlement path, and the one the compute
// (node.ComputeSettlementCoordinator) and inference marketplace flows use. The
// per-node pairwise (marketexchange) and token-chain
// (marketapi.SubmitSignedTransfer) paths remain distinct direct primitives on
// the same ledger and are not reconciled against consensus; the marketplace
// flows no longer use them. It returns the submitted signed transaction so
// callers can correlate it with the committed block.
func (n *Node) SettleThroughConsensus(from *token.Account, recipient string, amount, nonce uint64) (*token.Transaction, error) {
	if n.consensus == nil {
		return nil, fmt.Errorf("consensus engine is not running")
	}
	return n.consensus.SubmitAccountTransfer(from, recipient, amount, nonce)
}

// GetMarketAPI returns the external marketplace gRPC server, which exposes the
// order book, remote providers, signed settlement and the token chain to
// external buyers and providers over gRPC.
func (n *Node) GetMarketAPI() *marketapi.Server {
	return n.marketServer
}

// GetInference returns the inference service the node runs: it reserves capacity
// through the compute marketplace, fulfills jobs on a provider's registered
// inference Backend (a GPU-free echo backend is registered by default for the
// configured demo provider), and settles buyer -> provider through the same
// consensus engine the compute marketplace uses. Register additional provider
// backends via GetInference().Registry().
func (n *Node) GetInference() *inference.Service {
	return n.inferenceSvc
}

// GetInferenceAPI returns the external inference gRPC server
// (matrix.inference.v1.InferenceService), served on the node's Inference.Addr.
func (n *Node) GetInferenceAPI() *inferenceapi.Server {
	return n.inferenceServer
}

// RegisterInferenceAccount makes acct's signing key available to the inference
// Service so the node can sign consensus settlement transfers on behalf of that
// buyer. It complements the on-disk wallet resolver for in-process/test callers
// that hold an account in memory rather than as a wallet file. A nil account is
// a no-op.
func (n *Node) RegisterInferenceAccount(acct *token.Account) {
	if n.inferenceAccts == nil || acct == nil {
		return
	}
	n.inferenceAccts.Add(acct)
}
