package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Peer metrics
	peerCount = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "matrix_peer_count",
		Help: "Number of connected peers",
	})

	// Soul metrics
	soulCount = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "matrix_soul_count",
		Help: "Number of active souls",
	})

	soulMemorySize = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "matrix_soul_memory_size",
		Help: "Size of soul memory in bytes",
	}, []string{"soul_id"})

	// Matrix metrics
	matrixCount = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "matrix_count",
		Help: "Number of active matrices",
	})

	matrixEventCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "matrix_event_count",
		Help: "Number of matrix events by type",
	}, []string{"matrix_id", "event_type"})

	// Agent metrics
	agentCount = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "matrix_agent_count",
		Help: "Number of active agents",
	})

	agentMemoryUsage = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "matrix_agent_memory_usage",
		Help: "Memory usage by agent in bytes",
	}, []string{"agent_id"})

	// Message metrics
	messageCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "matrix_message_count",
		Help: "Number of messages by topic",
	}, []string{"topic"})
)

// Collector provides methods to record metrics
type Collector struct{}

// New creates a new metrics collector
func New() *Collector {
	return &Collector{}
}

// RecordPeerCount updates the peer count metric
func (c *Collector) RecordPeerCount(count int) {
	peerCount.Set(float64(count))
}

// RecordSoulCount updates the soul count metric
func (c *Collector) RecordSoulCount(count int) {
	soulCount.Set(float64(count))
}

// RecordSoulMemory updates the soul memory size metric
func (c *Collector) RecordSoulMemory(soulID string, size int64) {
	soulMemorySize.WithLabelValues(soulID).Set(float64(size))
}

// RecordMatrixCount updates the matrix count metric
func (c *Collector) RecordMatrixCount(count int) {
	matrixCount.Set(float64(count))
}

// RecordMatrixEvent increments the matrix event counter
func (c *Collector) RecordMatrixEvent(matrixID, eventType string) {
	matrixEventCount.WithLabelValues(matrixID, eventType).Inc()
}

// RecordAgentCount updates the agent count metric
func (c *Collector) RecordAgentCount(count int) {
	agentCount.Set(float64(count))
}

// RecordAgentMemory updates the agent memory usage metric
func (c *Collector) RecordAgentMemory(agentID string, usage int64) {
	agentMemoryUsage.WithLabelValues(agentID).Set(float64(usage))
}

// RecordMessage increments the message counter for a topic
func (c *Collector) RecordMessage(topic string) {
	messageCount.WithLabelValues(topic).Inc()
}
