package inference

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/token"
)

func authFor(t *testing.T, acct *token.Account, provider, model string, req InferenceRequest, at time.Time) *RunAuthorization {
	t.Helper()
	auth := &RunAuthorization{
		PublicKey: acct.PublicKey,
		Provider:  provider,
		Model:     model,
		Timestamp: at.UnixNano(),
	}
	if err := auth.Sign(req, acct.PrivateKey); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return auth
}

// TestARunAuthorizationProvesControlOfTheBuyerAccount is the whole point: this
// is what makes RunInferenceJob safe to serve without an API key.
func TestARunAuthorizationProvesControlOfTheBuyerAccount(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)
	req := InferenceRequest{Prompt: "hello", Model: "m"}

	auth := authFor(t, buyer, provider, "m", req, time.Now())
	if err := svc.VerifyRunAuthorization(buyer.AccountID(), req, auth); err != nil {
		t.Fatalf("an honest authorization should verify: %v", err)
	}
}

// TestAnAttackerCannotRunAgainstSomebodyElsesAccount is the griefing vector this
// closes. Without it, `buyer` is just a string: name a funded account, have the
// provider work, never sign. The victim's balance is untouched but the provider
// worked for free and its capacity is held until the request expires.
func TestAnAttackerCannotRunAgainstSomebodyElsesAccount(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, victim, provider := clientSignedService(t, fs, 3, 1_000_000)
	attacker, _ := token.GenerateAccount()
	req := InferenceRequest{Prompt: "free work please", Model: "m"}

	// The attacker signs perfectly well - with its own key.
	auth := authFor(t, attacker, provider, "m", req, time.Now())

	err := svc.VerifyRunAuthorization(victim.AccountID(), req, auth)
	if !errors.Is(err, ErrRunUnauthorized) {
		t.Fatalf("err = %v, want ErrRunUnauthorized", err)
	}
}

func TestAMissingAuthorizationIsRefused(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, _ := clientSignedService(t, fs, 3, 1_000_000)

	if err := svc.VerifyRunAuthorization(buyer.AccountID(), InferenceRequest{Prompt: "hi"}, nil); !errors.Is(err, ErrRunUnauthorized) {
		t.Fatalf("err = %v, want ErrRunUnauthorized", err)
	}
}

// TestAnAuthorizationIsBoundToItsPrompt: otherwise one signature would be a
// standing licence to run anything at all against the buyer's account.
func TestAnAuthorizationIsBoundToItsPrompt(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)

	asked := InferenceRequest{Prompt: "a short question", Model: "m"}
	auth := authFor(t, buyer, provider, "m", asked, time.Now())

	substituted := InferenceRequest{Prompt: "an enormously expensive question", Model: "m"}
	if err := svc.VerifyRunAuthorization(buyer.AccountID(), substituted, auth); !errors.Is(err, ErrRunUnauthorized) {
		t.Fatalf("err = %v, want the substituted prompt refused", err)
	}
}

// TestAnAuthorizationIsBoundToItsProviderAndModel: a signature for a cheap
// provider must not be spendable at an expensive one.
func TestAnAuthorizationIsBoundToItsProviderAndModel(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)
	req := InferenceRequest{Prompt: "hello", Model: "m"}

	auth := authFor(t, buyer, provider, "m", req, time.Now())

	movedProvider := *auth
	movedProvider.Provider = "somebody-else"
	if err := svc.VerifyRunAuthorization(buyer.AccountID(), req, &movedProvider); !errors.Is(err, ErrRunUnauthorized) {
		t.Fatalf("err = %v, want a different provider refused", err)
	}

	movedModel := *auth
	movedModel.Model = "a-dearer-model"
	if err := svc.VerifyRunAuthorization(buyer.AccountID(), req, &movedModel); !errors.Is(err, ErrRunUnauthorized) {
		t.Fatalf("err = %v, want a different model refused", err)
	}
}

// TestAnAuthorizationCannotBeReplayed: the same signature twice would be two
// runs for one authorization, which is exactly the free work being prevented.
func TestAnAuthorizationCannotBeReplayed(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)
	req := InferenceRequest{Prompt: "hello", Model: "m"}

	auth := authFor(t, buyer, provider, "m", req, time.Now())
	if err := svc.VerifyRunAuthorization(buyer.AccountID(), req, auth); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if err := svc.VerifyRunAuthorization(buyer.AccountID(), req, auth); !errors.Is(err, ErrRunUnauthorized) {
		t.Fatalf("second use: err = %v, want it refused as a replay", err)
	}
}

// TestAStaleAuthorizationIsRefused bounds replay even before the seen-set: an
// authorization captured last week must not be usable today.
func TestAStaleAuthorizationIsRefused(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)
	req := InferenceRequest{Prompt: "hello", Model: "m"}

	old := authFor(t, buyer, provider, "m", req, time.Now().Add(-time.Hour))
	if err := svc.VerifyRunAuthorization(buyer.AccountID(), req, old); !errors.Is(err, ErrRunUnauthorized) {
		t.Fatalf("err = %v, want a stale authorization refused", err)
	}

	// And one from the future, which is the same problem with the sign flipped.
	future := authFor(t, buyer, provider, "m", req, time.Now().Add(time.Hour))
	if err := svc.VerifyRunAuthorization(buyer.AccountID(), req, future); !errors.Is(err, ErrRunUnauthorized) {
		t.Fatalf("err = %v, want a future-dated authorization refused", err)
	}
}

func TestAMalformedAuthorizationIsRefused(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)
	req := InferenceRequest{Prompt: "hello", Model: "m"}
	good := authFor(t, buyer, provider, "m", req, time.Now())

	shortKey := *good
	shortKey.PublicKey = good.PublicKey[:16]
	if err := svc.VerifyRunAuthorization(buyer.AccountID(), req, &shortKey); !errors.Is(err, ErrRunUnauthorized) {
		t.Errorf("short key: err = %v, want refused", err)
	}

	shortSig := *good
	shortSig.Signature = good.Signature[:32]
	if err := svc.VerifyRunAuthorization(buyer.AccountID(), req, &shortSig); !errors.Is(err, ErrRunUnauthorized) {
		t.Errorf("short signature: err = %v, want refused", err)
	}

	tampered := *good
	tampered.Signature = append([]byte(nil), good.Signature...)
	tampered.Signature[0] ^= 0xff
	if err := svc.VerifyRunAuthorization(buyer.AccountID(), req, &tampered); !errors.Is(err, ErrRunUnauthorized) {
		t.Errorf("tampered signature: err = %v, want refused", err)
	}
}

// TestThePromptDigestIgnoresHowThePromptWasSent: a caller may send `prompt` or
// an equivalent one-message transcript, and a signature over one must cover the
// other, or an honest client picking the wrong field gets a confusing refusal.
func TestThePromptDigestIgnoresHowThePromptWasSent(t *testing.T) {
	asPrompt := InferenceRequest{Prompt: "hello", Model: "m"}
	asMessages := InferenceRequest{Messages: []Message{{Role: RoleUser, Content: "hello"}}, Model: "m"}

	if requestDigest(asPrompt) != requestDigest(asMessages) {
		t.Fatal("the same conversation sent two ways must digest the same")
	}
}

// TestTheDigestSeparatesFields: without length prefixes, two different
// transcripts could hash the same and one signature would cover both.
func TestTheDigestSeparatesFields(t *testing.T) {
	a := InferenceRequest{Messages: []Message{{Role: RoleUser, Content: "ab"}}}
	b := InferenceRequest{Messages: []Message{{Role: RoleUser, Content: "a"}, {Role: RoleUser, Content: "b"}}}
	if requestDigest(a) == requestDigest(b) {
		t.Fatal(`"ab" and "a"+"b" must not digest the same`)
	}
}

// TestAnAuthorizedRunActuallyRuns closes the loop: verification is a gate, not a
// replacement for the work.
func TestAnAuthorizedRunActuallyRuns(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)
	req := InferenceRequest{Prompt: "hello", Model: "m"}

	auth := authFor(t, buyer, provider, "m", req, time.Now())
	if err := svc.VerifyRunAuthorization(buyer.AccountID(), req, auth); err != nil {
		t.Fatalf("VerifyRunAuthorization: %v", err)
	}
	pr, err := svc.RunUnsettled(context.Background(), buyer.AccountID(), provider, req, 1000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}
	if _, err := svc.SettleSigned(context.Background(), pr.JobID, signedFor(t, pr, buyer)); err != nil {
		t.Fatalf("SettleSigned: %v", err)
	}
}

// TestTheReplaySetIsKeyedOnContentNotSignatureBytes
//
// The replay set used to key on sha256(auth.Signature). Signature bytes and
// signed content are not the same thing: a signature can have more than one
// valid encoding, so a re-encoded signature over the SAME authorization read as
// a brand-new one and the work ran again.
//
// Keying on the signed content removes the dependency on every signature scheme
// having exactly one canonical encoding. This test proves the key is content by
// re-signing the same authorization: ed25519 is deterministic, so the bytes are
// identical here - what matters is the second property below, that a DIFFERENT
// signature over the same content is still a replay.
func TestTheReplaySetIsKeyedOnContentNotSignatureBytes(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)
	req := InferenceRequest{Prompt: "hello", Model: "m"}
	at := time.Now()

	auth := authFor(t, buyer, provider, "m", req, at)
	if err := svc.VerifyRunAuthorization(buyer.AccountID(), req, auth); err != nil {
		t.Fatalf("first use should verify: %v", err)
	}

	// A second authorization object over identical content - same key, same
	// provider, same model, same prompt, same timestamp. It IS the same
	// authorization, whatever its signature bytes look like, and re-using it
	// must not buy a second run.
	again := authFor(t, buyer, provider, "m", req, at)
	err := svc.VerifyRunAuthorization(buyer.AccountID(), req, again)
	if !errors.Is(err, ErrRunUnauthorized) {
		t.Fatalf("a second authorization over identical content was accepted (err = %v); "+
			"one authorization would buy two runs", err)
	}

	// Tampering with only the signature bytes must also not produce a fresh
	// key. This is the shape of the malleability replay: same content, different
	// signature bytes.
	tampered := authFor(t, buyer, provider, "m", req, at)
	tampered.Signature = append([]byte(nil), tampered.Signature...)
	tampered.Signature[0] ^= 0xFF
	if err := svc.VerifyRunAuthorization(buyer.AccountID(), req, tampered); !errors.Is(err, ErrRunUnauthorized) {
		t.Fatalf("a tampered signature was accepted: %v", err)
	}
}

// TestADifferentPromptOrMomentIsNotAReplay. Keying on content must not collapse
// authorizations that are genuinely different, or an honest buyer's second
// question would be refused.
func TestADifferentPromptOrMomentIsNotAReplay(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)
	at := time.Now()

	first := InferenceRequest{Prompt: "question one", Model: "m"}
	if err := svc.VerifyRunAuthorization(buyer.AccountID(), first,
		authFor(t, buyer, provider, "m", first, at)); err != nil {
		t.Fatalf("first question: %v", err)
	}

	// A different prompt at the same instant.
	second := InferenceRequest{Prompt: "question two", Model: "m"}
	if err := svc.VerifyRunAuthorization(buyer.AccountID(), second,
		authFor(t, buyer, provider, "m", second, at)); err != nil {
		t.Fatalf("a different prompt was refused as a replay: %v", err)
	}

	// The same prompt a moment later.
	later := at.Add(time.Second)
	if err := svc.VerifyRunAuthorization(buyer.AccountID(), first,
		authFor(t, buyer, provider, "m", first, later)); err != nil {
		t.Fatalf("the same prompt at a later moment was refused as a replay: %v", err)
	}
}
