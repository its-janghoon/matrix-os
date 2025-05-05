package matrix

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Matrix represents a simulation environment
type Matrix struct {
	ID      string
	rules   []Rule
	rulesMu sync.RWMutex
	agents  map[string]*Agent
	agentMu sync.RWMutex
	metrics MetricsCollector
}

// Rule represents a simulation rule
type Rule struct {
	ID       string
	Priority int
	Evaluate func(context.Context, *Matrix) ([]Event, error)
}

// Agent represents an agent in the matrix
type Agent struct {
	ID      string
	Type    string
	State   map[string]interface{}
	stateMu sync.RWMutex
}

// Event represents a matrix event
type Event struct {
	Type      string
	Timestamp time.Time
	AgentID   string
	Data      map[string]interface{}
}

// MetricsCollector handles matrix metrics
type MetricsCollector interface {
	RecordEvent(Event)
	GetMetrics() map[string]float64
}

// New creates a new Matrix instance
func New(id string, metrics MetricsCollector) *Matrix {
	return &Matrix{
		ID:      id,
		rules:   make([]Rule, 0),
		agents:  make(map[string]*Agent),
		metrics: metrics,
	}
}

// AddRule adds a new rule to the matrix
func (m *Matrix) AddRule(rule Rule) {
	m.rulesMu.Lock()
	defer m.rulesMu.Unlock()
	m.rules = append(m.rules, rule)
}

// AddAgent adds a new agent to the matrix
func (m *Matrix) AddAgent(agent *Agent) error {
	m.agentMu.Lock()
	defer m.agentMu.Unlock()

	if _, exists := m.agents[agent.ID]; exists {
		return fmt.Errorf("agent with ID %s already exists", agent.ID)
	}

	m.agents[agent.ID] = agent
	return nil
}

// Step advances the matrix simulation by one step
func (m *Matrix) Step(ctx context.Context) error {
	m.rulesMu.RLock()
	rules := make([]Rule, len(m.rules))
	copy(rules, m.rules)
	m.rulesMu.RUnlock()

	// Evaluate rules in priority order
	for _, rule := range rules {
		events, err := rule.Evaluate(ctx, m)
		if err != nil {
			return fmt.Errorf("rule %s evaluation failed: %w", rule.ID, err)
		}

		// Record events
		for _, event := range events {
			m.metrics.RecordEvent(event)
		}
	}

	return nil
}

// GetAgent returns an agent by ID
func (m *Matrix) GetAgent(id string) (*Agent, bool) {
	m.agentMu.RLock()
	defer m.agentMu.RUnlock()
	agent, exists := m.agents[id]
	return agent, exists
}

// GetMetrics returns current matrix metrics
func (m *Matrix) GetMetrics() map[string]float64 {
	return m.metrics.GetMetrics()
}
