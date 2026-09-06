package node

import (
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/metrics"
)

// marketMetricsObserver adapts the metrics.Collector to the market.Observer
// interface. It lives in the node package so internal/market never imports
// internal/metrics: the market defines the observation contract and the node,
// which already owns both subsystems, implements it. Every callback simply
// forwards marketplace activity to the corresponding Prometheus recorder.
type marketMetricsObserver struct {
	metrics *metrics.Collector
}

// newMarketMetricsObserver returns an observer that drives marketplace metrics
// through the given collector.
func newMarketMetricsObserver(m *metrics.Collector) *marketMetricsObserver {
	return &marketMetricsObserver{metrics: m}
}

// Ensure the adapter satisfies the market observation contract.
var _ market.Observer = (*marketMetricsObserver)(nil)

// ProviderCountChanged updates the registered-provider gauge.
func (o *marketMetricsObserver) ProviderCountChanged(count int) {
	o.metrics.RecordProviderCount(count)
}

// ActiveJobsChanged updates the active-job gauge.
func (o *marketMetricsObserver) ActiveJobsChanged(count int) {
	o.metrics.RecordActiveJobs(count)
}

// JobCompleted increments the completed-job counter and adds the settled
// credits to the transferred-credits counter.
func (o *marketMetricsObserver) JobCompleted(credits uint64) {
	o.metrics.IncJobsCompleted()
	o.metrics.AddCreditsTransferred(credits)
}
