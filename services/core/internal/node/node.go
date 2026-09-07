package node

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ecirlabs/matrix-core/internal/admin"
	"github.com/ecirlabs/matrix-core/internal/agent"
	"github.com/ecirlabs/matrix-core/internal/bridge"
	"github.com/ecirlabs/matrix-core/internal/connectapi"
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
	inferencev1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/inference/v1"
	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
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
		// APIKeys are the keys that authenticate against the admin, market,
		// inference and HTTP surfaces when EnableACLs is set.
		//
		// They live in the config because the alternative was a single
		// MATRIX_ADMIN_API_KEY environment variable that nothing wrote and
		// nothing documented: a freshly initialized node has ACLs on, so every
		// RPC answered "authentication required" and no key existed to satisfy
		// it. `matrixd -init` now writes one generated key here, which is what
		// makes a first run work. MATRIX_ADMIN_API_KEY still works and is added
		// on top, for deployments that keep secrets out of files.
		APIKeys []APIKeyConfig `yaml:"api_keys"`
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
	// Bridge configures the opt-in lock-and-mint bridge to wrapped MATRIX on
	// Ethereum, including the always-on burn->unlock watcher. With no contract
	// configured the subsystem stays off and the node behaves exactly as a node
	// without a bridge. See bridge_watch.go for the fields and the authority
	// model.
	Bridge BridgeConfig `yaml:"bridge"`
	// Connect exposes the same market and inference services over plain HTTP
	// (the Connect protocol's unary JSON form) so a browser can call them. Raw
	// gRPC needs HTTP/2 trailers, which no browser can produce, so without this
	// surface a web app or a dApp front end cannot reach the node at all.
	Connect struct {
		// Addr is the TCP listen address. Default 0.0.0.0:9093. Set to "off" to
		// disable the endpoint entirely. (Only "off" - not a bare "-", which YAML
		// reads as the start of a list.)
		Addr string `yaml:"addr"`
		// AllowedOrigins lists the browser origins allowed to call it.
		//
		// Empty means NO browser may call the endpoint, and that is the default
		// for a config that does not mention it. Deny-by-default is deliberate:
		// a wide-open CORS policy on a daemon listening on localhost lets any
		// page the operator happens to visit drive their node, which on a node
		// running without ACLs means submitting jobs and spending their MATRIX.
		//
		// It costs non-browser callers nothing - curl, the Go client and the SDK
		// under Node send no Origin header and need no CORS - so this only ever
		// gates pages. `matrixd -init` writes the Console's dev origins, which is
		// the narrow thing that actually needs to work. "*" allows any origin and
		// belongs in development only.
		AllowedOrigins []string `yaml:"allowed_origins"`
	} `yaml:"connect"`
	Consensus struct {
		// Validators is the fixed validator set as hex-encoded account IDs
		// (ed25519 public keys). This node's own consensus identity is always
		// added to the set, so an empty list yields a functioning single-validator
		// consensus suitable for a solo/dev node. Every node configured with the
		// same list derives the identical round-robin leader schedule.
		Validators []string `yaml:"validators"`
		// EpochLength is how many committed blocks make an epoch. A validator-set
		// change carried by a committed block takes effect at the next height that
		// is a multiple of this number, so every node applies it at the SAME
		// height and no node's leader schedule diverges from its peers'. It must be
		// identical on every node; zero means consensus.DefaultEpochLength.
		EpochLength uint64 `yaml:"epoch_length"`
		// ApprovedChanges is this operator's local allow-list of validator-set
		// changes, as the change strings the engine prints ("add:<hex pubkey>" or
		// "remove:<account id>"). A change is only committed if a quorum of
		// validators votes for the block carrying it, and a node whose operator has
		// not listed the change refuses to vote for that block. So membership needs
		// agreement out of band rather than one node's say-so, which is the point:
		// a stranger cannot join the set, and a validator cannot be ejected, unless
		// the operators of a quorum have each said yes here.
		//
		// Leaving this empty means this node approves nothing, which is the safe
		// default for a running network.
		ApprovedChanges []string `yaml:"approved_changes"`
		// EjectEquivocators, unset or true, has this node vote to remove a
		// validator it holds proof equivocated - two votes for different blocks at
		// one height, round and phase, both signed by that validator's own key -
		// and offer that removal itself. No entry in approved_changes is needed,
		// because there is no judgement left to make: the evidence proves itself
		// and every node checks it rather than trusting a peer, so honest nodes all
		// reach the same conclusion. An honest validator cannot produce such a
		// pair.
		//
		// Set it false on a network where an operator would rather investigate an
		// offence than have the network eject the offender. Detection, recording
		// and gossip are unaffected either way.
		EjectEquivocators *bool `yaml:"eject_equivocators"`
		// Stake configures bonded stake: voting power becomes an account's bonded
		// native MATRIX, admission requires a minimum bond, and a proven offence
		// takes the offender's bond instead of only its place.
		Stake StakeConfig `yaml:"stake"`
		// FeeBasisPoints is the protocol fee taken from every value transfer a
		// committed block carries, in hundredths of a percent, and paid to the
		// validator set pro rata by voting power. Zero charges nothing.
		//
		// It is what makes validating pay for itself: bonded stake gives a
		// validator something to lose and nothing to earn, so without a fee a
		// bond is a pure cost and no third party would post one. Turn the two on
		// together.
		//
		// The build refuses a rate above 100 (one percent), so a mistyped 1000
		// fails at startup rather than taking ten times the intended cut. EVERY
		// node in a network must agree on the rate: a node charging differently
		// would compute different balances from the same block, which is a fork.
		FeeBasisPoints uint32 `yaml:"fee_basis_points"`
		// Rewards configures the provider emission: what the genesis pool pays
		// out per block to the accounts that supply compute.
		Rewards RewardsConfig `yaml:"rewards"`
	} `yaml:"consensus"`
	Genesis GenesisConfig `yaml:"genesis"`
}

// StakeConfig configures bonded stake for consensus.
//
// Membership used to be agreement between operators and nothing else, so a
// validator caught equivocating lost its place and nothing more, and a quorum
// measured in HEADS could be bought for the price of N identities. A bond
// answers both: voting power is bonded stake, so a quorum costs two thirds of
// everything bonded however many identities it is spread across, and an
// offence that is provable is answered by taking the bond.
type StakeConfig struct {
	// Enabled turns bonded stake on. Off, the network runs as it did: every
	// validator has power 1, every quorum is a headcount, and an ejected
	// validator loses only its place. That is coherent for operators who know
	// each other; it is not safe for a set anyone may join.
	Enabled bool `yaml:"enabled"`
	// MinBond is the stake an account must have bonded, in native base units,
	// before the network may admit it as a validator. Null means the default
	// (1e15, a thousandth of the supply cap); zero explicitly means no minimum,
	// which is only appropriate on a network not using stake for security.
	MinBond *uint64 `yaml:"min_bond"`
	// UnbondingPeriod is how many blocks after leaving the validator set an
	// account must wait before it may withdraw its bond. Zero means the default.
	//
	// The delay is why a bond deters anything. Without it a validator
	// equivocates, is ejected, and withdraws before the network has committed
	// the slash - so the bond it was supposed to lose is already spent. It has
	// to be long enough for evidence to be gossiped, voted on and committed.
	UnbondingPeriod uint64 `yaml:"unbonding_period"`
	// Bond is how much of its own native MATRIX this node should keep bonded, in
	// base units. The node bonds the shortfall itself and keeps topping it up,
	// which is how an operator stakes: bonding has to be signed by the
	// validator's own key, and that key lives inside the node.
	//
	// The node's consensus account has to hold the coins first. Its id is
	// printed at startup; fund it with `matrix fund` or a transfer.
	Bond uint64 `yaml:"bond"`
}

// effectiveMinBond and effectiveUnbonding resolve what the engine will actually
// use, so the startup line reports the number in force rather than the raw
// (possibly null or zero) config value.
func effectiveMinBond(cfg StakeConfig) uint64 {
	if cfg.MinBond != nil {
		return *cfg.MinBond
	}
	return consensus.DefaultMinBond
}

func effectiveHalfLife(cfg RewardsConfig) uint64 {
	if cfg.HalfLife > 0 {
		return cfg.HalfLife
	}
	return consensus.DefaultProviderEmissionHalfLife
}

func effectiveUnbonding(cfg StakeConfig) uint64 {
	if cfg.UnbondingPeriod > 0 {
		return cfg.UnbondingPeriod
	}
	return consensus.DefaultUnbondingPeriod
}

// RewardsConfig configures the provider emission from the genesis pool.
//
// The token policy allocates a share of the pool to compute providers. This is
// the mechanism for it, and its shape is forced by one fact: consensus cannot
// see work. A job lives in the marketplace, whose provider list and job records
// are per-node state no quorum ever ordered, so the chain knows only that native
// MATRIX moved from one account to another.
//
// That rules out paying a provider a percentage of what it was paid, because a
// percentage of a transfer is a money pump: send coins to an account you also
// control, collect the percentage, send them back, repeat. So the emission is a
// FIXED per-block budget, SHARED among the registered providers a block paid,
// pro rata by how much. Faking volume can move a share of the budget; it cannot
// increase it, so the pool empties on schedule and not faster.
type RewardsConfig struct {
	// PerBlock is what the pool pays out per committed block, in native base
	// units, shared among the registered providers credited in that block. Zero
	// pays nothing and leaves the pool untouched.
	PerBlock uint64 `yaml:"per_block"`
	// HalfLife is how many blocks halve the emission. Zero means the default
	// (1,000,000 blocks). The schedule is a right shift, so it reaches exactly
	// zero rather than trailing off asymptotically.
	//
	// To size these: the total ever paid is about 1.44 * PerBlock * HalfLife, so
	// a target spend T over a half-life H wants PerBlock around T / (1.44 * H).
	HalfLife uint64 `yaml:"half_life"`
	// ApprovedProviders is this operator's allow-list of registry changes, as
	// "add:<account id>" / "remove:<account id>". A registration needs a QUORUM
	// of operators to have listed it, the same as admitting a validator: who
	// earns from the pool is not something the protocol can decide, and without
	// a registry the emission would pay whoever happened to receive a transfer.
	ApprovedProviders []string `yaml:"approved_providers"`
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

// APIKeyConfig is one credential the node accepts. Role is "admin",
// "operator" or "viewer"; an unset role is treated as admin, which is what a
// single-operator dev node wants.
type APIKeyConfig struct {
	Key  string `yaml:"key"`
	Role string `yaml:"role"`
	Name string `yaml:"name"`
}

// GenesisAllocationConfig is a single named genesis allocation: Amount native
// base units credited to Account.
type GenesisAllocationConfig struct {
	// Account is the account ID to credit at genesis.
	Account string `yaml:"account"`
	// Amount is the balance to credit, in native base units.
	Amount uint64 `yaml:"amount"`
}

// generateAPIKey returns a 32-byte random key, hex encoded. It uses crypto/rand
// because this value is the whole of a node's authentication.
func generateAPIKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// roleFromConfig maps a configured role name to an admin role, defaulting to
// admin for an unset value: a single-operator node that bothered to write a key
// means it to work.
func roleFromConfig(name string) admin.Role {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "admin":
		return admin.RoleAdmin
	case "operator":
		return admin.RoleOperator
	case "viewer":
		return admin.RoleViewer
	default:
		return admin.RoleViewer
	}
}

// connectAuth adapts the admin authenticator to the small interface the
// Connect endpoint takes. It returns nil when there is no authenticator, which
// is how "ACLs disabled" reaches the endpoint: exactly the rule the gRPC
// servers apply, expressed once.
//
// The adapter exists because the endpoint deliberately does not import the
// admin package - it needs one question answered ("is this caller
// authenticated?"), not the whole role model.
func connectAuth(auth *admin.Authenticator) connectapi.Authenticator {
	if auth == nil {
		return nil
	}
	return authenticatorFunc(func(ctx context.Context) (string, error) {
		role, err := auth.Authenticate(ctx)
		return string(role), err
	})
}

type authenticatorFunc func(ctx context.Context) (string, error)

func (f authenticatorFunc) Authenticate(ctx context.Context) (string, error) { return f(ctx) }

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
	evidence         *consensus.EvidenceStore
	metrics          *metrics.Collector
	adminServer      *admin.Server
	marketServer     *marketapi.Server
	inferenceSvc     *inference.Service
	inferenceServer  *inferenceapi.Server
	connectServer    *connectapi.Server
	signingAccts     *walletAccounts
	bridge           *bridge.Bridge
	bridgeWatcher    *bridge.Watcher
	bridgeWatchDone  chan struct{}
	agents           map[string]*agent.Agent
	agentsMu         sync.RWMutex
	souls            map[string]*soul.Soul
	soulsMu          sync.RWMutex
	matrices         map[string]*matrix.Matrix
	matricesMu       sync.RWMutex
}

// The market listing the demo inference provider is registered with. It exists
// so an inference job can be submitted, reserved and settled on a fresh node
// with no GPU and no model server; a real deployment registers its own provider
// at its own price. These match `matrix quickstart`'s demo provider so the two
// demos behave the same.
const (
	demoInferenceCapacity = 100
	demoInferencePrice    = 5
)

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
	// Generate one admin key, because ACLs are on and a node with no valid key
	// cannot be driven by anything - not even by `matrix` on the same machine.
	// A generated config that refuses every call is not a working default.
	key, err := generateAPIKey()
	if err != nil {
		return fmt.Errorf("failed to generate an API key: %w", err)
	}
	config.Security.APIKeys = []APIKeyConfig{{Key: key, Role: "admin", Name: "local-admin"}}
	config.Admin.Addr = "0.0.0.0:9090"
	config.Market.Addr = "0.0.0.0:9091"
	config.Inference.Addr = "0.0.0.0:9092"
	config.Connect.Addr = "0.0.0.0:9093"
	// The Console's Vite dev server, which is the browser origin that actually
	// needs this on a fresh node. Anything else is opted into explicitly.
	config.Connect.AllowedOrigins = []string{"http://127.0.0.1:5173", "http://localhost:5173"}
	// Spell out the epoch length rather than leaving it zero: every node in a
	// network must agree on it, so it belongs in the file where an operator can
	// see and copy it. Approve no set changes by default - a node that
	// pre-approved membership changes would vote to admit validators its operator
	// never agreed to.
	config.Consensus.EpochLength = consensus.DefaultEpochLength
	config.Consensus.ApprovedChanges = nil
	// Spelled out rather than left null so the knob is visible in the file an
	// operator reads. True is the default either way.
	ejectEquivocators := true
	config.Consensus.EjectEquivocators = &ejectEquivocators
	// Bonded stake OFF in a generated config. Turning it on is a decision about
	// what a validator must risk, and it cannot be made for an operator: on a
	// single-node dev network it would require that node to bond a million
	// MATRIX before it could validate anything. The section is written so the
	// knobs are visible.
	config.Consensus.Stake = StakeConfig{Enabled: false}
	// Protocol fee set to 100 basis points (1%) in a generated config, which is
	// the code cap (consensus.MaxFeeBasisPoints). This is the operator's explicit
	// monetary-policy decision for this network, recorded here so a freshly
	// initialized node charges the agreed rate; it is taken from every committed
	// value transfer and paid to the validator set pro rata by voting power. An
	// operator who wants a different rate (including 0, no fee) edits
	// consensus.fee_basis_points; the engine still refuses any value above the
	// cap at startup rather than silently clamping it.
	config.Consensus.FeeBasisPoints = consensus.MaxFeeBasisPoints
	// And no provider emission. Both are monetary policy, and a node that
	// started paying out the genesis pool because that was the default would be
	// making that policy on the operator's behalf.
	config.Consensus.Rewards = RewardsConfig{}
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
	// Leave the bridge subsystem OFF in a freshly-initialized config. Enabling it
	// requires deployment facts a generated config cannot invent: the deployed
	// WrappedMatrix address, its EVM chain id, an Ethereum RPC endpoint, and the
	// contract's deployment block. Writing the empty section documents the knobs
	// without pretending a bridge exists; an operator fills them in and sets
	// bridge.watch.enabled to run the burn->unlock relayer inside matrixd. The
	// conservative default confirmation depth applies when confirmations is left
	// null (see defaultBridgeConfirmations); set it to 0 only for a local chain
	// with instant finality, such as hardhat.
	config.Bridge = BridgeConfig{}

	// Create config directory if it doesn't exist
	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Write config file. 0600, not the usual 0644: it now holds an API key, and
	// a credential readable by every account on the machine is not a credential.
	f, err := os.OpenFile(configPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
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
	if config.Connect.Addr == "" {
		config.Connect.Addr = "0.0.0.0:9093"
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
	// Print it. The identity is generated on first start and persisted in the
	// store, and it was previously never shown anywhere: an operator could not
	// learn their own node's validator id, which made consensus.validators
	// impossible to fill in and a multi-node network impossible to configure.
	fmt.Printf("Consensus identity: %s\n", consensusAccount.AccountID())
	validatorSet, err := consensus.ValidatorSetFromConfig(consensusAccount.PublicKey, n.config.Consensus.Validators)
	if err != nil {
		return fmt.Errorf("failed to build validator set: %w", err)
	}
	// Equivocation evidence: a validator that votes two ways in one round is the
	// one Byzantine act this protocol can prove, and until this store existed the
	// proof was discarded. The engine records it, gossips it, and - unless
	// consensus.eject_equivocators is false - votes to remove the offender
	// through the chain, which takes effect at the next epoch boundary once a
	// quorum of validators holding the same evidence has committed it. No node
	// changes the set on its own authority; that would fork it away from its
	// peers.
	n.evidence = consensus.NewEvidenceStore(n.kvStore)

	// Resolve the stake configuration before building the engine, so the two
	// "zero means default" cases are decided in one place: a null min_bond takes
	// the default, an explicit 0 means no minimum at all.
	var stakeLedger *consensus.StakeLedger
	var minBond uint64
	var zeroMinBond bool
	if n.config.Consensus.FeeBasisPoints > 0 {
		fmt.Printf("Consensus: a protocol fee of %d basis points (%.2f%%) is taken from every value transfer "+
			"and paid to the validator set pro rata by voting power.\n",
			n.config.Consensus.FeeBasisPoints, float64(n.config.Consensus.FeeBasisPoints)/100)
	}
	if n.config.Consensus.Rewards.PerBlock > 0 {
		fmt.Printf("Consensus: provider rewards are ON. The genesis pool pays up to %d base units per block, "+
			"halving every %d blocks, shared among the registered providers a block pays.\n",
			n.config.Consensus.Rewards.PerBlock, effectiveHalfLife(n.config.Consensus.Rewards))
	}
	if n.config.Consensus.Stake.Enabled {
		stakeLedger = consensus.NewStakeLedger(n.market.Ledger(), n.kvStore)
		if n.config.Consensus.Stake.MinBond != nil {
			minBond = *n.config.Consensus.Stake.MinBond
			zeroMinBond = minBond == 0
		}
		fmt.Printf("Consensus: bonded stake is ON. Voting power is bonded MATRIX; "+
			"a validator must bond at least %d base units to be admitted, and waits %d blocks after leaving to withdraw.\n",
			effectiveMinBond(n.config.Consensus.Stake), effectiveUnbonding(n.config.Consensus.Stake))
		if n.config.Consensus.Stake.Bond > 0 {
			fmt.Printf("Consensus: this node will keep %d base units bonded from its own account %s.\n",
				n.config.Consensus.Stake.Bond, consensusAccount.AccountID())
		} else {
			fmt.Printf("Consensus: consensus.stake.bond is 0, so this node bonds nothing and cannot be admitted " +
				"to a staked validator set.\n")
		}
	}
	consensusEngine, err := consensus.New(consensus.Config{
		Transport:  n.transport,
		Validators: validatorSet,
		Chain:      consensus.NewBlockChain(n.kvStore),
		Ledger:     n.market.Ledger(),
		Self:       consensusAccount,
		Evidence:   n.evidence,
		// The validator set is chain state, not a startup constant: Sets persists
		// the set the committed chain arrived at, so a restart resumes the set the
		// network agreed on rather than snapping back to whatever this file says.
		// consensus.validators is therefore the GENESIS set - it seeds a fresh
		// store and is ignored once the chain has changed the set.
		Sets:               consensus.NewSetStore(n.kvStore),
		EpochLength:        n.config.Consensus.EpochLength,
		ApprovedSetChanges: n.config.Consensus.ApprovedChanges,
		EjectEquivocators:  n.config.Consensus.EjectEquivocators,
		// Bonded stake, when the operator has turned it on. A bond is a balance
		// in a reserved account on the SAME ledger everything else settles on, so
		// bonding conserves supply and bonded coins leave the spendable balance
		// without any second accounting system.
		Stake:           stakeLedger,
		MinBond:         minBond,
		ZeroMinBond:     zeroMinBond,
		UnbondingPeriod: n.config.Consensus.Stake.UnbondingPeriod,
		TargetBond:      n.config.Consensus.Stake.Bond,
		FeeBasisPoints:  n.config.Consensus.FeeBasisPoints,
		// The provider emission and the registry that decides who earns it.
		Providers:                consensus.NewProviderRegistry(n.kvStore),
		ProviderEmissionPerBlock: n.config.Consensus.Rewards.PerBlock,
		ProviderEmissionHalfLife: n.config.Consensus.Rewards.HalfLife,
		ApprovedProviders:        n.config.Consensus.Rewards.ApprovedProviders,
		OnEquivocation: func(eq *consensus.Equivocation) {
			fmt.Printf("consensus: validator %s equivocated at height %d round %d; evidence stored under "+
				"consensus/evidence/ in this node's database.\n", eq.VoterID, eq.Height, eq.Round)
		},
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
		for i, k := range n.config.Security.APIKeys {
			if k.Key == "" {
				return fmt.Errorf("security.api_keys[%d] has no key", i)
			}
			name := k.Name
			if name == "" {
				name = fmt.Sprintf("config-key-%d", i)
			}
			apiKeys = append(apiKeys, &admin.APIKey{
				Key:  k.Key,
				Role: roleFromConfig(k.Role),
				Name: name,
			})
		}
		// MATRIX_ADMIN_API_KEY is additive, for deployments that keep secrets out
		// of files entirely.
		if envKey := os.Getenv("MATRIX_ADMIN_API_KEY"); envKey != "" {
			apiKeys = append(apiKeys, &admin.APIKey{
				Key:  envKey,
				Role: admin.RoleAdmin,
				Name: "env-admin",
			})
		}
		if len(apiKeys) == 0 {
			// Worth shouting about: with ACLs on and no valid key, every RPC on
			// every surface answers "authentication required" and the node cannot
			// be driven at all - including by its own CLI.
			fmt.Printf("Warning: security.enable_acls is true but no API keys are configured, " +
				"so every RPC will refuse. Add one under security.api_keys, set " +
				"MATRIX_ADMIN_API_KEY, or run `matrixd -init` to generate a config with a key.\n")
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
	//
	// FundAccount is the ONE mutating RPC that carries no per-request signature
	// (it moves reward-pool MATRIX by account id + amount only), so the
	// signature-based safety argument above does not cover it. To keep the
	// "every mutating RPC is authorized" invariant honest, FundAccount refuses to
	// run unless the server enforces authentication: on an ACLs-off node it
	// returns FailedPrecondition rather than acting, so an open node cannot be
	// used to drain the reward pool. Reward-pool funding is therefore only exposed
	// when ACLs are enabled (marketapi.Service gates on cfg.Auth != nil).
	var marketAuth *admin.Authenticator
	if n.config.Security.EnableACLs {
		marketAuth = n.adminServer.GetAuthenticator()
	}
	marketSettled := token.NewSettledLedger(n.market.Ledger(), n.tokenChain)

	// One signing-account resolver for the whole node. Settling a job - compute
	// or inference - is a transfer FROM the buyer, so it needs the buyer's key;
	// the resolver reads the wallet files under ~/.matrix, which is the
	// single-operator model the quickstart uses, and never fabricates custody. A
	// multi-tenant deployment replaces it with its own custodial resolver.
	n.signingAccts = newWalletAccounts()

	// CompleteJob settles through consensus. It used to charge the buyer with a
	// direct market.Ledger transfer, which on more than one node is not agreed -
	// the credits move on this node's copy and nowhere else - and on any node is
	// not authorized, because no signature from the payer is involved. The
	// coordinator signs a transfer as the buyer, waits for a quorum to commit and
	// apply it, and only then finalizes the job.
	settlementCoordinator, err := NewComputeSettlementCoordinator(n.market, n.consensus, n.signingAccts)
	if err != nil {
		return fmt.Errorf("failed to build the compute settlement coordinator: %w", err)
	}
	// External, client-signed value transfers (SubmitSignedTransfer /
	// `matrix wallet transfer`) settle through the SAME consensus engine now,
	// instead of token.SettledLedger.Settle. Before this, that path appended to
	// the per-node token.Chain and moved credits on one node ordered by no
	// quorum, so balances could diverge between nodes and the transfer escaped
	// the protocol fee. Routing it through consensus makes the resulting balances
	// a deterministic function of committed blocks (every node agrees) and
	// subjects the transfer to the default-off fee like every other committed
	// transfer. The coordinator also backs the GetTransaction/ListTransactions
	// history with the committed block chain, so `matrix tx list` shows the same
	// ordered transfers on every node.
	transferCoordinator, err := NewTransferSettlementCoordinator(n.consensus)
	if err != nil {
		return fmt.Errorf("failed to build the transfer settlement coordinator: %w", err)
	}
	marketServer, err := marketapi.NewServer(marketapi.Config{
		Addr:            n.config.Market.Addr,
		Auth:            marketAuth,
		Market:          n.market,
		Settled:         marketSettled,
		Chain:           n.tokenChain,
		Exchange:        n.exchange,
		Funder:          n.treasury,
		Settler:         settlementCoordinator,
		TransferSettler: transferCoordinator,
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
	// an Accounts resolver rather than assuming custody. It is the SAME resolver
	// the compute-settlement coordinator uses (n.signingAccts, built above), so
	// custody is one decision for the whole node rather than one per marketplace.
	// A multi-tenant deployment supplies its own custodial resolver via
	// GetInference() before serving; the wallet resolver only knows keys that
	// exist as wallet files under its directory, so it never fabricates custody.
	inferenceRegistry := inference.NewRegistry()
	inferenceSvc, err := inference.NewService(inference.Config{
		Market:   n.market,
		Registry: inferenceRegistry,
		Settler:  n.consensus,
		Accounts: n.signingAccts,
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
		// An inference job is a market job: it reserves capacity from a REGISTERED
		// provider and settles at that provider's price. Registering the backend
		// alone left the one command this demo provider exists for -
		// `matrix inference submit --provider demo-inference-provider` - failing
		// with "market: provider not found" on a freshly initialized node.
		//
		// Only when it is not already there: RegisterProvider resets Available to
		// Capacity, so re-registering on every restart would forget the capacity
		// currently reserved by pending jobs.
		if _, exists := n.market.GetProvider(n.config.Inference.EchoProvider); !exists {
			if err := n.market.RegisterProvider(market.Provider{
				ID:           n.config.Inference.EchoProvider,
				Capacity:     demoInferenceCapacity,
				PricePerUnit: demoInferencePrice,
			}); err != nil {
				return fmt.Errorf("failed to register the demo inference provider on the market: %w", err)
			}
		}
		fmt.Printf("Inference: registered echo backend for demo provider %q (capacity %d, price %d/unit).\n",
			n.config.Inference.EchoProvider, demoInferenceCapacity, demoInferencePrice)
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

	// Serve the SAME market and inference implementations over plain HTTP, so a
	// browser can reach them. It is the same objects, not a copy: one code path
	// serves both surfaces, so they cannot drift, and the same authenticator
	// gates both when ACLs are enabled.
	if addr := n.config.Connect.Addr; addr != "" && addr != "off" {
		connectServer, err := connectapi.NewServer(addr, connectapi.Config{
			Bindings: []connectapi.Binding{
				{Desc: &marketv1.MarketService_ServiceDesc, Impl: n.marketServer.Service()},
				{Desc: &inferencev1.InferenceService_ServiceDesc, Impl: n.inferenceServer.Service()},
			},
			Auth:           connectAuth(marketAuth),
			AllowedOrigins: n.config.Connect.AllowedOrigins,
		})
		if err != nil {
			return fmt.Errorf("failed to create connect endpoint: %w", err)
		}
		n.connectServer = connectServer
		if err := n.connectServer.Start(n.ctx); err != nil {
			return fmt.Errorf("failed to start connect endpoint: %w", err)
		}
	}

	// Initialize the lock-and-mint bridge subsystem over this node's own market
	// ledger and KV store, and (when configured) run the always-on burn->unlock
	// watcher against it. This is the piece that makes the bridge a matrixd
	// subsystem rather than an out-of-process demo: because the Bridge here holds
	// BOTH halves on one ledger (Lock moves native into bridge/escrow, the
	// watcher's ProcessBurn releases it back out), escrow accounting is coherent
	// and Bridge.Reconcile is a real invariant check. cmd/bridge-watch, by
	// contrast, applies burns to a throwaway ledger it seeds, so it can only ever
	// demonstrate the decode+unlock step.
	//
	// The whole subsystem is opt-in and off by default: with no bridge.contract
	// configured, newConfiguredBridge returns nil and the node runs exactly as it
	// did before this wiring existed. A config that enables bridge.watch without
	// a contract, chain id, or rpc_url is rejected here rather than silently
	// ignored, so a deployment that believes it is relaying never comes up quiet.
	//
	// AUTHORITY (honest, unchanged by this wiring): the watcher is a per-node
	// polling relayer. Running it inside matrixd does not make the unlock
	// consensus-ordered; on a multi-validator deployment the burn is applied by
	// whichever node runs the watcher. That is the correct model for a solo/dev
	// node or a single operator-run relayer node; consensus-ordered unlock is a
	// separate design change (see bridge_watch.go).
	nodeBridge, err := newConfiguredBridge(n.market.Ledger(), n.kvStore, n.config.Bridge)
	if err != nil {
		return fmt.Errorf("failed to initialize bridge: %w", err)
	}
	n.bridge = nodeBridge
	if nodeBridge != nil {
		fmt.Printf("Bridge: enabled for WrappedMatrix %s on chain %d.\n",
			nodeBridge.Params().BridgeContract.Hex(), n.config.Bridge.ChainID)
	}

	ethClient, err := dialBridgeClient(n.config.Bridge)
	if err != nil {
		return fmt.Errorf("failed to initialize bridge watcher: %w", err)
	}
	// newConfiguredBridgeWatcher takes bridge.Applier, so pass a typed nil-safe
	// value: a nil *bridge.Bridge in an interface is non-nil, which would defeat
	// the builder's own "no bridge configured" check.
	var applier bridge.Applier
	if nodeBridge != nil {
		applier = nodeBridge
	}
	watcher, err := newConfiguredBridgeWatcher(
		applier,
		n.kvStore,
		ethClient,
		n.config.Bridge,
		func(d bridge.DecodedBurn) {
			native, convErr := token.ERC20ToNative(d.ERC20Amount)
			if convErr != nil {
				fmt.Printf("Bridge watcher: applied burn %s to %s (amount conversion failed: %v)\n",
					d.ID, d.NativeRecipient, convErr)
				return
			}
			fmt.Printf("Bridge watcher: unlocked %d native base units to %s (burn %s)\n",
				native, d.NativeRecipient, d.ID)
		},
		func(watchErr error) {
			// Non-fatal: a transient RPC failure or one malformed log. The watcher
			// retries on the next tick; surface it so the operator can see a
			// persistently broken endpoint.
			fmt.Printf("Bridge watcher warning: %v\n", watchErr)
		},
	)
	if err != nil {
		return fmt.Errorf("failed to initialize bridge watcher: %w", err)
	}
	if watcher != nil {
		n.bridgeWatcher = watcher
		n.bridgeWatchDone = runBridgeWatcher(n.ctx, watcher, func(exitErr error) {
			if exitErr != nil && !errors.Is(exitErr, context.Canceled) {
				fmt.Printf("Bridge watcher stopped: %v\n", exitErr)
			}
		})
		fmt.Printf("Bridge watcher: polling %s from block %d (%d confirmations).\n",
			n.config.Bridge.Watch.RPCURL, watcher.Cursor(), n.config.Bridge.Watch.confirmations())
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

	// Stop the bridge burn->unlock watcher first. It writes to the market ledger
	// and the KV store, both of which are torn down below, so it must be joined
	// (not merely signalled) before that happens. Cancelling the node context
	// ends its Run loop; n.cancel is idempotent, so the later cancel for the
	// exchange/consensus loops is harmless.
	if n.bridgeWatchDone != nil {
		n.cancel()
		<-n.bridgeWatchDone
	}

	// Stop inference API server
	if n.connectServer != nil {
		if err := n.connectServer.Stop(n.ctx); err != nil {
			fmt.Printf("Warning: failed to stop connect endpoint: %v\n", err)
		}
	}

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

// Equivocations returns the misbehaviour this node has proof of, oldest first.
// An operator uses it to decide who to remove from the validator set.
func (n *Node) Equivocations() ([]consensus.Equivocation, error) {
	if n.evidence == nil {
		return nil, nil
	}
	return n.evidence.All()
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

// GetBridge returns the node's lock-and-mint bridge, or nil when no bridge is
// configured (bridge.contract unset, the default). The returned Bridge holds
// both halves of the flow on this node's own ledger: Lock escrows native MATRIX
// into bridge/escrow and produces the validator attestation the Ethereum
// WrappedMatrix contract needs to mint, and ProcessBurn (driven by the watcher
// or an operator) releases escrow back on a burn. Because both halves share one
// ledger, Reconcile is a meaningful 1:1 backing check on this node.
func (n *Node) GetBridge() *bridge.Bridge {
	return n.bridge
}

// GetBridgeWatcher returns the running burn->unlock watcher, or nil when
// bridge.watch is not enabled. The watcher polls the configured Ethereum
// endpoint for WrappedMatrix Burned events and applies each exactly once to the
// bridge above; its scan cursor is persisted in the node's KV store so a restart
// resumes rather than re-scanning from start_block. It is a per-node relayer,
// not a consensus-ordered operation; see bridge_watch.go for that boundary.
func (n *Node) GetBridgeWatcher() *bridge.Watcher {
	return n.bridgeWatcher
}

// RegisterInferenceAccount makes acct's signing key available to the node so it
// can sign consensus settlement transfers on behalf of that buyer, for both
// compute jobs and inference jobs. It complements the on-disk wallet resolver
// for in-process/test callers that hold an account in memory rather than as a
// wallet file. A nil account is a no-op.
func (n *Node) RegisterInferenceAccount(acct *token.Account) {
	if n.signingAccts == nil || acct == nil {
		return
	}
	n.signingAccts.Add(acct)
}
