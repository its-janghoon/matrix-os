package node

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
}

// Node represents a Matrix node instance
type Node struct {
	ctx    context.Context
	config *Config
	// TODO: Add fields for other components (p2p, soul, matrix, etc.)
}

// Initialize creates a new node configuration
func Initialize(configPath string) error {
	// Create default configuration
	config := &Config{}
	config.Network.ListenAddr = "0.0.0.0:9000"
	config.Storage.Engine = "pebble"
	config.Storage.Path = "/var/lib/matrix/data"
	config.Security.EnableACLs = true
	config.Security.AllowUnsignedAgents = false

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

	return &Node{
		ctx:    ctx,
		config: config,
	}, nil
}

// Start initializes and starts all node components
func (n *Node) Start() error {
	// TODO: Initialize and start components:
	// - P2P networking
	// - Soul management
	// - Matrix execution
	// - WebAssembly runtime
	// - Storage system
	// - API servers
	return nil
}

// Stop gracefully shuts down all node components
func (n *Node) Stop() error {
	// TODO: Implement graceful shutdown of all components
	return nil
}
