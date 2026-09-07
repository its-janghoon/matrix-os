package transport

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

// This package had no tests. That mattered once it gained a security-relevant
// hook: a validator that is silently never registered looks exactly like one
// that accepts everything, and the difference is whether an unauthenticated
// peer's bytes get relayed to the whole mesh.

func newHost(t *testing.T) host.Host {
	t.Helper()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("libp2p.New: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

// connect dials b from a and waits until both see the connection, so a
// published message has somewhere to go.
func connect(t *testing.T, a, b host.Host) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Connect(ctx, peer.AddrInfo{ID: b.ID(), Addrs: b.Addrs()}); err != nil {
		t.Fatalf("connect: %v", err)
	}
}

// TestARejectedMessageIsNotDelivered is the property the validator exists for.
// A message the guard refuses must not reach the subscriber - and because
// gossipsub applies the validator before forwarding, not reaching the subscriber
// is the same decision as not relaying it to the mesh.
func TestARejectedMessageIsNotDelivered(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	publisher := newHost(t)
	subscriber := newHost(t)
	connect(t, publisher, subscriber)

	pub, err := New(ctx, Config{Host: publisher})
	if err != nil {
		t.Fatalf("publisher transport: %v", err)
	}

	rejected := errors.New("refused by the guard")
	var seen []string
	sub, err := New(ctx, Config{
		Host: subscriber,
		Validator: func(_ string, _ peer.ID, payload []byte) error {
			seen = append(seen, string(payload))
			if string(payload) == "bad" {
				return rejected
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("subscriber transport: %v", err)
	}

	const topic = "matrix.test/guarded"
	ch, err := sub.Subscribe(ctx, topic)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	// The publisher must join the topic and the mesh must form before a publish
	// reaches anyone.
	if _, err := pub.Subscribe(ctx, topic); err != nil {
		t.Fatalf("publisher Subscribe: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	if err := pub.Publish(ctx, topic, []byte("bad")); err != nil {
		t.Fatalf("Publish(bad): %v", err)
	}
	if err := pub.Publish(ctx, topic, []byte("good")); err != nil {
		t.Fatalf("Publish(good): %v", err)
	}

	// Only the accepted message may arrive, and it must arrive: a validator
	// that rejected everything would pass a "bad is dropped" assertion while
	// breaking the network.
	select {
	case msg := <-ch:
		if string(msg.Payload) != "good" {
			t.Fatalf("received %q; a message the validator rejected was delivered", msg.Payload)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("nothing arrived within 10s; the validator saw %v", seen)
	}

	// And nothing else follows.
	select {
	case msg := <-ch:
		t.Fatalf("a second message arrived: %q", msg.Payload)
	case <-time.After(500 * time.Millisecond):
	}
}

// TestTheValidatorSeesEveryMessage. A hook that is registered but not consulted
// is the failure this test exists to catch: it is indistinguishable from
// "accepts everything" in behaviour, and the whole point is that an
// unauthenticated peer's bytes are inspected before the mesh carries them.
func TestTheValidatorSeesEveryMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	publisher := newHost(t)
	subscriber := newHost(t)
	connect(t, publisher, subscriber)

	pub, err := New(ctx, Config{Host: publisher})
	if err != nil {
		t.Fatalf("publisher transport: %v", err)
	}

	calls := make(chan []byte, 8)
	sub, err := New(ctx, Config{
		Host: subscriber,
		Validator: func(_ string, _ peer.ID, payload []byte) error {
			calls <- append([]byte(nil), payload...)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("subscriber transport: %v", err)
	}

	const topic = "matrix.test/seen"
	if _, err := sub.Subscribe(ctx, topic); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if _, err := pub.Subscribe(ctx, topic); err != nil {
		t.Fatalf("publisher Subscribe: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	if err := pub.Publish(ctx, topic, []byte("inspect me")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case got := <-calls:
		if string(got) != "inspect me" {
			t.Fatalf("the validator saw %q", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the validator was never called; it is registered but not consulted, which is " +
			"indistinguishable from accepting everything")
	}
}

// TestNoValidatorStillWorks pins that the hook is optional, so an in-process or
// trusted-network caller is unaffected.
func TestNoValidatorStillWorks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	publisher := newHost(t)
	subscriber := newHost(t)
	connect(t, publisher, subscriber)

	pub, err := New(ctx, Config{Host: publisher})
	if err != nil {
		t.Fatalf("publisher transport: %v", err)
	}
	sub, err := New(ctx, Config{Host: subscriber})
	if err != nil {
		t.Fatalf("subscriber transport: %v", err)
	}

	const topic = "matrix.test/unguarded"
	ch, err := sub.Subscribe(ctx, topic)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if _, err := pub.Subscribe(ctx, topic); err != nil {
		t.Fatalf("publisher Subscribe: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	if err := pub.Publish(ctx, topic, []byte("anything")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case msg := <-ch:
		if string(msg.Payload) != "anything" {
			t.Fatalf("received %q", msg.Payload)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("nothing arrived with no validator configured")
	}
}
