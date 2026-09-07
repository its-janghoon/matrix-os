package transport

import (
	"context"
	"fmt"
	"sync"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Transport handles message routing and pub/sub
type Transport struct {
	host      host.Host
	pubsub    *pubsub.PubSub
	topics    map[string]*pubsub.Topic
	subs      map[string]*pubsub.Subscription
	topicMu   sync.RWMutex
	validator Validator
}

// Message represents a transport message
type Message struct {
	From    peer.ID
	Topic   string
	Payload []byte
}

// Validator decides whether a gossip message may be accepted, BEFORE pubsub
// relays it. Returning an error rejects the message: it is not delivered to this
// node's handler, it is NOT forwarded to this node's mesh peers, and gossipsub
// lowers the sending peer's score.
//
// WHY THIS HOOK EXISTS. Without a validator, gossipsub accepts any message
// published to a subscribed topic by any connected peer and relays it to the
// mesh before the application ever looks at it. So an unauthenticated peer that
// can dial one node had every node in the mesh spend bandwidth on its bytes,
// and had every node decode them - see consensus.GossipGuard for what that
// decode cost. Validation has to happen here, not in the handler, because by
// the time the handler runs the relay has already happened.
type Validator func(topic string, from peer.ID, payload []byte) error

// Config represents transport configuration
type Config struct {
	Host host.Host
	// Validator, when set, gates every message on every topic this Transport
	// subscribes to. Nil means accept everything, which is the old behaviour and
	// is only safe on a trusted network.
	Validator Validator
}

// New creates a new Transport instance
func New(ctx context.Context, cfg Config) (*Transport, error) {
	// Create pubsub service
	ps, err := pubsub.NewGossipSub(ctx, cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("failed to create pubsub: %w", err)
	}

	return &Transport{
		host:      cfg.Host,
		pubsub:    ps,
		topics:    make(map[string]*pubsub.Topic),
		subs:      make(map[string]*pubsub.Subscription),
		validator: cfg.Validator,
	}, nil
}

// Subscribe joins a topic and returns a message channel
func (t *Transport) Subscribe(ctx context.Context, topic string) (<-chan Message, error) {
	t.topicMu.Lock()
	defer t.topicMu.Unlock()

	// Join topic if not already joined
	tp, exists := t.topics[topic]
	if !exists {
		// The validator is registered BEFORE joining, so no message can arrive
		// on this topic unvalidated. Registering after Join leaves a window in
		// which gossipsub would relay whatever showed up first.
		if t.validator != nil {
			validate := t.validator
			err := t.pubsub.RegisterTopicValidator(topic,
				func(_ context.Context, from peer.ID, msg *pubsub.Message) pubsub.ValidationResult {
					if err := validate(topic, from, msg.Data); err != nil {
						// Reject, not Ignore: Ignore drops the message for this
						// node and says nothing about the sender, while Reject
						// also penalizes its score, which is what eventually
						// prunes a peer that keeps sending garbage.
						return pubsub.ValidationReject
					}
					return pubsub.ValidationAccept
				})
			if err != nil {
				return nil, fmt.Errorf("failed to register validator for topic %s: %w", topic, err)
			}
		}
		var err error
		tp, err = t.pubsub.Join(topic)
		if err != nil {
			return nil, fmt.Errorf("failed to join topic %s: %w", topic, err)
		}
		t.topics[topic] = tp
	}

	// Subscribe if not already subscribed
	sub, exists := t.subs[topic]
	if !exists {
		var err error
		sub, err = tp.Subscribe()
		if err != nil {
			return nil, fmt.Errorf("failed to subscribe to topic %s: %w", topic, err)
		}
		t.subs[topic] = sub
	}

	// Create message channel
	ch := make(chan Message)

	// Start message handling goroutine
	go func() {
		defer close(ch)
		for {
			msg, err := sub.Next(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}

			select {
			case <-ctx.Done():
				return
			case ch <- Message{
				From:    msg.ReceivedFrom,
				Topic:   topic,
				Payload: msg.Data,
			}:
			}
		}
	}()

	return ch, nil
}

// Publish sends a message to a topic
func (t *Transport) Publish(ctx context.Context, topic string, data []byte) error {
	t.topicMu.RLock()
	tp, exists := t.topics[topic]
	t.topicMu.RUnlock()

	if !exists {
		return fmt.Errorf("not subscribed to topic %s", topic)
	}

	return tp.Publish(ctx, data)
}

// Close shuts down the transport
func (t *Transport) Close() error {
	t.topicMu.Lock()
	defer t.topicMu.Unlock()

	// Unsubscribe from all topics
	for _, sub := range t.subs {
		sub.Cancel()
	}

	// Close all topics
	for _, topic := range t.topics {
		topic.Close()
	}

	return nil
}
