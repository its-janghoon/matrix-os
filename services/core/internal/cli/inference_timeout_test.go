package cli

import (
	"testing"
	"time"
)

// The buyer's deadline caps the whole call, the provider's request_timeout
// included. A default sized for reading a balance therefore decided how long a
// model was allowed to think, and on a real GPU a reasoning model exceeded it
// every time - reported as "read provider stream: context deadline exceeded",
// which names the provider's stream and reads as the provider's fault.
func TestARunGetsLongerThanAReadWhenTheBuyerSaysNothing(t *testing.T) {
	got := inferenceTimeout(false, defaultTimeout)
	if got == defaultTimeout {
		t.Fatalf("a run took the read default %s; the buyer's silence must not "+
			"cap the provider's own request_timeout", got)
	}
	if got != defaultInferenceTimeout {
		t.Fatalf("inferenceTimeout(silent) = %s, want %s", got, defaultInferenceTimeout)
	}
}

// Only silence is replaced. An explicit --timeout is obeyed exactly, in both
// directions: a caller who asks for 5s is saying they would rather fail fast
// than wait, and quietly lengthening it would make that unsayable - the same
// reason an explicitly empty --api-key is not filled in from the environment.
func TestAnExplicitTimeoutIsObeyedEvenWhenItIsShorterThanTheRunNeeds(t *testing.T) {
	for _, explicit := range []time.Duration{5 * time.Second, 30 * time.Minute} {
		if got := inferenceTimeout(true, explicit); got != explicit {
			t.Fatalf("inferenceTimeout(explicit %s) = %s, want it obeyed exactly", explicit, got)
		}
	}
}
