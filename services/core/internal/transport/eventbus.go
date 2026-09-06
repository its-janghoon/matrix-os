package transport

import (
	"context"
	"sync"
)

// EventType represents the type of event
type EventType string

const (
	// EventTypeP2P represents P2P network events
	EventTypeP2P EventType = "p2p"
	// EventTypeSoul represents Soul-related events
	EventTypeSoul EventType = "soul"
	// EventTypeMatrix represents Matrix simulation events
	EventTypeMatrix EventType = "matrix"
	// EventTypeAgent represents Agent execution events
	EventTypeAgent EventType = "agent"
	// EventTypeTrainer represents training events
	EventTypeTrainer EventType = "trainer"
)

// Event represents a system event
type Event struct {
	Type      EventType
	Source    string
	Timestamp int64
	Data      map[string]interface{}
}

// EventBus provides pub/sub functionality for system events
type EventBus struct {
	subscribers map[EventType][]chan Event
	mu          sync.RWMutex
}

// NewEventBus creates a new event bus
func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[EventType][]chan Event),
	}
}

// Subscribe subscribes to events of a specific type
func (eb *EventBus) Subscribe(ctx context.Context, eventType EventType) <-chan Event {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	ch := make(chan Event, 100) // Buffered channel to avoid blocking
	eb.subscribers[eventType] = append(eb.subscribers[eventType], ch)

	// Clean up subscription when context is done
	go func() {
		<-ctx.Done()
		eb.unsubscribe(eventType, ch)
	}()

	return ch
}

// Publish publishes an event to all subscribers
func (eb *EventBus) Publish(event Event) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	subscribers := eb.subscribers[event.Type]
	for _, ch := range subscribers {
		select {
		case ch <- event:
		default:
			// Channel is full, skip to avoid blocking
		}
	}
}

// unsubscribe removes a subscriber channel
func (eb *EventBus) unsubscribe(eventType EventType, ch chan Event) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	subscribers := eb.subscribers[eventType]
	for i, sub := range subscribers {
		if sub == ch {
			close(ch)
			eb.subscribers[eventType] = append(subscribers[:i], subscribers[i+1:]...)
			break
		}
	}
}

// Close closes all subscriber channels
func (eb *EventBus) Close() {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	for _, subscribers := range eb.subscribers {
		for _, ch := range subscribers {
			close(ch)
		}
	}
	eb.subscribers = make(map[EventType][]chan Event)
}
