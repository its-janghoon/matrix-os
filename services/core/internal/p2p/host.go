package p2p

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/libp2p/go-libp2p"
	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
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
	// Identity is the host's libp2p private key. When nil, libp2p generates an
	// EPHEMERAL one, which changes the node's peer id on every start and
	// invalidates every other node's bootstrap_peers entry - so a real
	// deployment passes a persisted key (see LoadOrCreatePeerKey).
	Identity libp2pcrypto.PrivKey
}

// New creates a new p2p host
func New(ctx context.Context, cfg *Config) (*Host, error) {
	// Resolve the listen address into a valid multiaddr. This accepts both the
	// libp2p multiaddr form (e.g. "/ip4/127.0.0.1/tcp/9000") and the plain
	// "host:port" form (e.g. "0.0.0.0:9000", the shape node.Config's default
	// listen_addr uses); see listenMultiaddr for the conversion rules.
	listenAddr, err := listenMultiaddr(cfg.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid listen address %q: %w", cfg.ListenAddr, err)
	}

	// Create libp2p host.
	//
	// NOTE: auto-relay must NOT be enabled with a nil peer source. libp2p's
	// relay finder panics at construction time when EnableAutoRelay* is set
	// without either a peer-source function or a static relay list ("Need a
	// Peer Source fn or a list of static relays"), which previously prevented
	// the node daemon from starting at all. Circuit-relay transport support is
	// still enabled via EnableRelay so the host can dial/accept relayed
	// connections; only the (misconfigured) automatic relay discovery is
	// dropped. NAT port mapping is retained for real deployments.
	opts := []libp2p.Option{
		libp2p.ListenAddrs(listenAddr),
		libp2p.EnableRelay(),
		libp2p.NATPortMap(),
	}
	if cfg.Identity != nil {
		// Without this the peer id is different on every start. See
		// LoadOrCreatePeerKey for what that breaks.
		opts = append(opts, libp2p.Identity(cfg.Identity))
	}

	h, err := libp2p.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create libp2p host: %w", err)
	}

	return &Host{
		host: h,
	}, nil
}

// listenMultiaddr converts a configured listen address into a libp2p multiaddr.
// It accepts three forms:
//
//   - empty string -> "/ip4/0.0.0.0/tcp/0" (all interfaces, random port)
//   - a full multiaddr (starts with "/"), e.g. "/ip4/127.0.0.1/tcp/9000",
//     which is parsed as-is
//   - a plain "host:port" (e.g. "0.0.0.0:9000"), which is split with
//     net.SplitHostPort and rebuilt into a /ip4|/ip6|/dns .../tcp/<port>
//     multiaddr
//
// The plain host:port branch is what makes node.Config's default listen_addr
// ("0.0.0.0:9000") boot: feeding host:port straight into a multiaddr template
// (e.g. "/ip4/0.0.0.0:9000/tcp/0") is invalid, so it is parsed here first.
func listenMultiaddr(listen string) (multiaddr.Multiaddr, error) {
	listen = strings.TrimSpace(listen)
	if listen == "" {
		// Default to all interfaces on a random port.
		return multiaddr.NewMultiaddr("/ip4/0.0.0.0/tcp/0")
	}

	// A full multiaddr is used verbatim.
	if strings.HasPrefix(listen, "/") {
		return multiaddr.NewMultiaddr(listen)
	}

	// Otherwise treat it as host:port and build the equivalent multiaddr.
	host, portStr, err := net.SplitHostPort(listen)
	if err != nil {
		return nil, fmt.Errorf("expected a multiaddr or host:port: %w", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port %q", portStr)
	}
	if host == "" {
		// A bare ":9000" listens on all IPv4 interfaces.
		host = "0.0.0.0"
	}

	var proto string
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() != nil {
			proto = "ip4"
		} else {
			proto = "ip6"
		}
	} else {
		// A hostname (not a literal IP) maps to the dns multiaddr protocol.
		proto = "dns"
	}
	return multiaddr.NewMultiaddr(fmt.Sprintf("/%s/%s/tcp/%d", proto, host, port))
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
