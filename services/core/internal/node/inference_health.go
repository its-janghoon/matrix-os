package node

import (
	"context"
	"fmt"
	"time"

	"github.com/ecirlabs/matrix-core/internal/inference"
)

// This file makes a provider's order-book listing follow the health of the thing
// that actually serves it.
//
// Before this, the node advertised whatever capacity the config declared and
// never looked at the model server again. A crashed or restarting runner
// produced a provider that kept winning routing decisions, took reservations it
// could not honour, and failed them one by one. From a buyer's side that is
// worse than the provider being absent: they were quoted, they waited, and the
// capacity was held while it happened.

// defaultInferenceHealthCheckInterval is how often a configured backend is
// probed when the operator sets no interval. It is short enough that a dead
// backend leaves the market in well under a minute and long enough that the
// probe traffic is not a load of its own.
const defaultInferenceHealthCheckInterval = 30 * time.Second

// inferenceHealthCheck is one supervised backend: the provider id whose
// order-book listing it controls, the backend to probe, and how often.
type inferenceHealthCheck struct {
	providerID string
	backend    inference.ProbeableBackend
	interval   time.Duration
}

// startInferenceHealthChecks launches one supervisor per probeable backend. It
// is called from Start, after the node context exists, rather than from
// registration, so that installing backends stays a pure function that tests can
// call without starting goroutines.
func (n *Node) startInferenceHealthChecks() {
	for _, hc := range n.inferenceHealthChecks {
		go n.superviseInferenceBackend(n.ctx, hc)
	}
}

// superviseInferenceBackend probes one backend on an interval and suspends or
// resumes its provider to match.
//
// FAIL CLOSED, ON THE FIRST FAILURE. There is no consecutive-failure threshold
// on purpose, because the two outcomes are not symmetric. Suspending a provider
// that was briefly unreachable costs it the routing decisions of one interval,
// and it returns on the next successful probe. Leaving a dead provider on the
// market costs a buyer a reservation, a wait, and a failed request - and costs
// the provider the same capacity anyway. When in doubt, be off the market.
//
// Suspension never cancels work already reserved. A probe cannot distinguish a
// backend that has died from one that was busy finishing a real completion, and
// killing live jobs on that evidence would be the more expensive mistake.
func (n *Node) superviseInferenceBackend(ctx context.Context, hc inferenceHealthCheck) {
	ticker := time.NewTicker(hc.interval)
	defer ticker.Stop()

	// Probe once immediately. Waiting a full interval before the first check
	// means a node that starts while its model server is still loading weights
	// advertises capacity it cannot serve for that whole interval.
	n.checkInferenceBackend(ctx, hc)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.checkInferenceBackend(ctx, hc)
		}
	}
}

// checkInferenceBackend runs one probe and applies the result to the order book.
// It logs only on a transition, so a healthy provider is silent and a change of
// state is not buried in a repeated line.
func (n *Node) checkInferenceBackend(ctx context.Context, hc inferenceHealthCheck) {
	probeErr := hc.backend.Probe(ctx)
	// A cancelled node context is a shutdown, not a sick backend. Suspending on
	// the way down would persist a suspension the next start has to clear.
	if ctx.Err() != nil {
		return
	}

	changed, err := n.market.SetProviderSuspended(hc.providerID, probeErr != nil)
	if err != nil {
		fmt.Printf("Inference: health check for provider %q could not update the order book: %v\n",
			hc.providerID, err)
		return
	}
	if !changed {
		return
	}
	if probeErr != nil {
		fmt.Printf("Inference: provider %q SUSPENDED - its backend is not answering: %v\n",
			hc.providerID, probeErr)
		return
	}
	fmt.Printf("Inference: provider %q is serving again and is back on the market.\n", hc.providerID)
}

// inferenceHealthCheckInterval resolves the configured interval for one backend,
// and reports whether health checking is enabled for it at all.
//
// Enabled by default: a provider that did not think about this should get the
// protection rather than the old silent-failure behaviour. The switch exists
// because `kind: openai` also points at PAID third-party vendors, where a probe
// every interval is a request somebody is billed for and counts against a rate
// limit.
func inferenceHealthCheckInterval(cfg InferenceBackendConfig) (time.Duration, bool) {
	if cfg.HealthCheck != nil && !*cfg.HealthCheck {
		return 0, false
	}
	if cfg.HealthCheckInterval > 0 {
		return cfg.HealthCheckInterval, true
	}
	return defaultInferenceHealthCheckInterval, true
}
