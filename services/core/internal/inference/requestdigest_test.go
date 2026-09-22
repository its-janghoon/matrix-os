package inference

import (
	"math"
	"testing"
)

// The two fields that joined the digest in v0.5.8, and the reason they had to.
//
// The authorization's whole job is to say "I am the buyer and I am asking for THIS
// work". MaxTokens is what the meter counts, so a node that could raise it on a
// signed request decided how much of the reservation was spent; the reservation
// capped the loss, but the buyer had signed for one job and paid for a larger one.

func reqWith(maxTokens int, temperature float64) InferenceRequest {
	return InferenceRequest{
		Model:       "m",
		Messages:    []Message{{Role: RoleUser, Content: "x"}},
		MaxTokens:   maxTokens,
		Temperature: temperature,
	}
}

func TestTheDigestCoversMaxTokens(t *testing.T) {
	a := requestDigest(reqWith(100, 0))
	b := requestDigest(reqWith(4096, 0))
	if a == b {
		t.Fatal("two different token budgets hash the same, so a node can raise one on a signed request")
	}
}

func TestTheDigestCoversTemperature(t *testing.T) {
	a := requestDigest(reqWith(100, 0.7))
	b := requestDigest(reqWith(100, 0.8))
	if a == b {
		t.Fatal("two different temperatures hash the same")
	}
}

// Hashed as IEEE-754 bits rather than formatted, and this is the case that makes
// the difference visible: negative zero and positive zero are equal as numbers and
// distinct as bits. A decimal rendering would flatten them into one digest.
//
// It lives here rather than in the cross-language vectors because JSON cannot
// carry it - Go writes negative zero as `0`, so the file would claim a value it
// does not hold. The vectors cover the fields; this covers the encoding.
func TestTemperatureIsHashedAsBitsNotAsText(t *testing.T) {
	positive := requestDigest(reqWith(1, 0))
	negative := requestDigest(reqWith(1, math.Copysign(0, -1)))
	if positive == negative {
		t.Error("positive and negative zero hash the same, so temperature is being formatted rather than hashed as bits")
	}
}

// The legacy digest must NOT cover them - that is what makes it the old rule. If
// this ever passes, the compatibility arm has stopped being a compatibility arm.
func TestTheLegacyDigestIgnoresTheNewFields(t *testing.T) {
	a := legacyRequestDigest(reqWith(100, 0.1))
	b := legacyRequestDigest(reqWith(4096, 0.9))
	if a != b {
		t.Fatal("the legacy digest covers the new fields, so it is not the rule the deployed clients signed")
	}
}

// And the two rules must differ, or the transition is a no-op that only looks like
// one.
func TestTheTwoDigestsDiffer(t *testing.T) {
	req := reqWith(4096, 0.7)
	if requestDigest(req) == legacyRequestDigest(req) {
		t.Fatal("the current and legacy digests are identical for a request with bounds set")
	}
	// With no bounds set they are still different, because the current rule hashes
	// the zero values rather than omitting them. Stated rather than assumed: a
	// reader could reasonably expect the suffix to be skipped when empty, and it is
	// not - omitting it would make "no budget" and "a budget of zero" the same
	// signature.
	bare := InferenceRequest{Model: "m", Messages: []Message{{Role: RoleUser, Content: "x"}}}
	if requestDigest(bare) == legacyRequestDigest(bare) {
		t.Error("with no bounds set the two digests match, which means the suffix is being omitted rather than hashed as zero")
	}
}
