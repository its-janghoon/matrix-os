package node

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/ecirlabs/matrix-core/internal/admin"
	"github.com/ecirlabs/matrix-core/internal/agent"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/matrix"
	"github.com/ecirlabs/matrix-core/internal/metrics"
	"github.com/ecirlabs/matrix-core/internal/p2p"
	"github.com/ecirlabs/matrix-core/internal/soul"
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
}

// Node represents a Matrix node instance
type Node struct {
	ctx        context.Context
	cancel     context.CancelFunc
	config     *Config
	p2pHost    *p2p.Host
	transport  *transport.Transport
	eventBus   *transport.EventBus
	kvStore    *kv.Store
	metrics    *metrics.Collector
	adminServer *admin.Server
	agents     map[string]*agent.Agent
	agentsMu   sync.RWMutex
	souls      map[string]*soul.Soul
	soulsMu    sync.RWMutex
	matrices   map[string]*matrix.Matrix
	matricesMu sync.RWMutex
}

// Initialize creates a new node configuration
func Initialize(configPath string) error {
	// Create default configuration
	config := &Config{}
	config.Network.ListenAddr = "0.0.0.0:9000"
	config.Storage.Engine = "pebble"
	config.Storage.Path = "./data"
	config.Security.EnableACLs = true
	config.Security.AllowUnsignedAgents = false
	config.Admin.Addr = "0.0.0.0:9090"

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

	// Connect to bootstrap peers
	for _, peerAddr := range n.config.Network.BootstrapPeers {
		if err := n.p2pHost.Connect(n.ctx, peerAddr); err != nil {
			// Log but don't fail on bootstrap peer connection errors
			fmt.Printf("Warning: failed to connect to bootstrap peer %s: %v\n", peerAddr, err)
		}
	}

	// Initialize admin server
	n.adminServer = admin.NewServer(admin.Config{
		Addr: n.config.Admin.Addr,
	})

	// Start admin server
	if err := n.adminServer.Start(n.ctx); err != nil {
		return fmt.Errorf("failed to start admin server: %w", err)
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

	// Stop admin server
	if n.adminServer != nil {
		if err := n.adminServer.Stop(n.ctx); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop admin server: %w", err))
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
