package inference

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// This file answers one question a marketplace provider could not answer about
// itself: is the thing behind me actually able to serve?
//
// The node advertised the capacity from its config whether or not the model
// server was up. A crashed runner therefore produced a provider that kept
// winning routing decisions, taking reservations, and failing them - and a
// failure after the buyer has been quoted is worse than not being on the market
// at all, because it holds capacity and burns the buyer's request.

// DefaultProbeTimeout bounds one readiness probe. It is deliberately short and
// deliberately NOT the backend's RequestTimeout: a provider serving long
// completions may allow ten minutes for real work, and waiting ten minutes to
// learn that a dead server is dead would defeat the check.
const DefaultProbeTimeout = 10 * time.Second

// DefaultOpenAIProbePath is the readiness path for OpenAI-compatible upstreams.
// It is /v1/models because that is the one read endpoint the OpenAI-compatible
// contract guarantees, so it works against a hosted vendor as well as a local
// server.
//
// A local server usually offers something better. vLLM, SGLang and TGI all
// expose /health, which reports on the inference engine rather than only on the
// HTTP layer in front of it, and an operator running one of those should point
// health_check_path at it. The distinction is real: a wedged engine can still
// answer /v1/models from a list it built at startup.
const DefaultOpenAIProbePath = "/v1/models"

// DefaultLocalHTTPProbePath is the readiness path for an Ollama-style runner.
const DefaultLocalHTTPProbePath = "/api/tags"

// ProbeableBackend is the optional interface a Backend implements when its
// readiness can be checked without running an inference. It is optional for the
// same reason StreamingBackend is: the echo backend has no upstream to be down,
// and a backend that cannot be probed should be left alone rather than guessed
// about.
type ProbeableBackend interface {
	// Probe returns nil when the upstream is answering. Any error means it is
	// not, and the caller should take the provider off the market until a later
	// probe succeeds.
	Probe(ctx context.Context) error
}

// probeHTTP performs the shared GET-and-check. A 2xx is ready; anything else,
// including a transport error, is not.
func probeHTTP(ctx context.Context, client *http.Client, url, bearer string) error {
	ctx, cancel := context.WithTimeout(ctx, DefaultProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("inference: build readiness probe: %w", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("inference: readiness probe to %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Drain a little so the connection can be reused rather than torn down on
	// every probe.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: readiness probe to %s returned %d", ErrProviderStatus, url, resp.StatusCode)
	}
	return nil
}

// Probe checks that the OpenAI-compatible upstream is answering.
func (b *OpenAIBackend) Probe(ctx context.Context) error {
	return probeHTTP(ctx, b.client, b.baseURL+b.probePath, b.apiKey)
}

// Probe checks that the local runner is answering.
func (b *LocalHTTPBackend) Probe(ctx context.Context) error {
	return probeHTTP(ctx, b.client, b.baseURL+b.probePath, "")
}

// normalizeProbePath resolves a configured probe path against a per-kind
// default, and makes it absolute so a config that omits the leading slash does
// not silently produce a URL with the path glued onto the host.
func normalizeProbePath(configured, fallback string) string {
	path := strings.TrimSpace(configured)
	if path == "" {
		path = fallback
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}
