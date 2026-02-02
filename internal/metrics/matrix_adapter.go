package metrics

import (
	"github.com/ecirlabs/matrix-core/internal/matrix"
)

// MatrixMetricsAdapter adapts the metrics collector to the matrix MetricsCollector interface
type MatrixMetricsAdapter struct {
	collector *Collector
	matrixID  string
}

// NewMatrixMetricsAdapter creates a new adapter for a specific matrix
func NewMatrixMetricsAdapter(collector *Collector, matrixID string) *MatrixMetricsAdapter {
	return &MatrixMetricsAdapter{
		collector: collector,
		matrixID:  matrixID,
	}
}

// RecordEvent records a matrix event
func (a *MatrixMetricsAdapter) RecordEvent(event matrix.Event) {
	a.collector.RecordMatrixEvent(a.matrixID, event.Type)
}

// GetMetrics returns current metrics for the matrix
func (a *MatrixMetricsAdapter) GetMetrics() map[string]float64 {
	// Return empty map for now - can be extended to return actual metrics
	return make(map[string]float64)
}
