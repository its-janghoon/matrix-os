package p2p

import (
	"context"
	"fmt"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

// Host represents a p2p network host
type Host struct {
	host host.Host
}

// Config represents p2p host configuration
type Config struct {
	ListenAddr string
	// Add more config options as needed
}

// New creates a new p2p host
func New(ctx context.Context, cfg *Config) (*Host, error) {
	// Parse the listen address (format: "0.0.0.0:9000" or just IP)
	// Extract IP and port
	var listenAddr multiaddr.Multiaddr
	var err error

	if cfg.ListenAddr == "" {
		// Default to all interfaces on random port
		listenAddr, err = multiaddr.NewMultiaddr("/ip4/0.0.0.0/tcp/0")
	} else {
		// Try to parse as multiaddr first
		listenAddr, err = multiaddr.NewMultiaddr(cfg.ListenAddr)
		if err != nil {
			// If not a multiaddr, try to parse as IP:port
			listenAddr, err = multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/%s/tcp/0", cfg.ListenAddr))
		}
	}

	if err != nil {
		return nil, fmt.Errorf("invalid listen address: %w", err)
	}

	// Create libp2p host
	h, err := libp2p.New(
		libp2p.ListenAddrs(listenAddr),
		libp2p.EnableRelay(),
		libp2p.EnableAutoRelayWithPeerSource(nil),
		libp2p.NATPortMap(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create libp2p host: %w", err)
	}

	return &Host{
		host: h,
	}, nil
}

// Connect attempts to connect to a peer
func (h *Host) Connect(ctx context.Context, addr string) error {
	// Parse the peer address
	peerAddr, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		return fmt.Errorf("invalid peer address: %w", err)
	}

	// Extract the peer ID from the address
	info, err := peer.AddrInfoFromP2pAddr(peerAddr)
	if err != nil {
		return fmt.Errorf("failed to parse peer info: %w", err)
	}

	// Connect to the peer
	if err := h.host.Connect(ctx, *info); err != nil {
		return fmt.Errorf("failed to connect to peer: %w", err)
	}

	return nil
}

// GetHost returns the underlying libp2p host
func (h *Host) GetHost() host.Host {
	return h.host
}

// GetPeerID returns the peer ID of this host
func (h *Host) GetPeerID() peer.ID {
	return h.host.ID()
}

// GetAddrs returns the addresses this host is listening on
func (h *Host) GetAddrs() []multiaddr.Multiaddr {
	return h.host.Addrs()
}

// Close shuts down the p2p host
func (h *Host) Close() error {
	return h.host.Close()
}
