package inference

import (
	"context"
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// greedyBackend answers with a short completion and reports whatever usage it
// likes. It is what a dishonest provider is: the model server reporting the
// token count is the seller's own, and the count is what settles.
type greedyBackend struct {
	completion  string
	claimTokens int
}

func (g greedyBackend) Name() string { return "greedy" }

func (g greedyBackend) Infer(ctx context.Context, req InferenceRequest) (InferenceResponse, error) {
	usage := Usage{TotalTokens: g.claimTokens}
	return InferenceResponse{
		Completion: g.completion,
		Model:      req.Model,
		Usage:      usage,
		Units:      UnitsFor(usage),
	}, nil
}

// serviceWithBackend builds a service whose provider runs b.
func serviceWithBackend(t *testing.T, b Backend, settler Settler, pricePerUnit, buyerCredits uint64) (*Service, string, string) {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mkt, err := market.NewMarket(store)
	if err != nil {
		t.Fatalf("market.NewMarket: %v", err)
	}
	buyer, _ := token.GenerateAccount()
	provider, _ := token.GenerateAccount()
	buyerID, providerID := buyer.AccountID(), provider.AccountID()
	if err := mkt.Ledger().Credit(buyerID, buyerCredits); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if err := mkt.RegisterProvider(market.Provider{ID: providerID, Capacity: 1_000_000, PricePerUnit: pricePerUnit}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	registry := NewRegistry()
	if err := registry.Register(providerID, b); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}
	svc, err := NewService(Config{
		Market:   mkt,
		Registry: registry,
		Settler:  settler,
		Accounts: memAccounts{m: map[string]*token.Account{buyerID: buyer}},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, buyerID, providerID
}

// TestAProviderCannotBillTheWholeReservationForAShortAnswer
//
// THE THEFT. The billable token count comes from the PROVIDER's own model
// server, and the only thing bounding it was the buyer's reservation.
// Reservations are generous by design - a request with no max_tokens reserves
// room for a long answer that may never come - so a provider could answer "hi"
// with "hello" and report the entire reservation. Every check passed: the report
// was under the reservation, and the reservation had been affordability-checked.
// Nothing compared the bill to the answer.
//
// Here the buyer reserves 2000 units for a three-word exchange and the provider
// claims all 2000. What it can honestly have cost is bounded by the text that
// crossed the wire, and that bound is what settles.
func TestAProviderCannotBillTheWholeReservationForAShortAnswer(t *testing.T) {
	const reserved = 2000
	greedy := greedyBackend{completion: "hello", claimTokens: reserved}
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := serviceWithBackend(t, greedy, fs, 1, 1_000_000)

	job, err := svc.SubmitInferenceJob(buyer, provider, InferenceRequest{Prompt: "hi", Model: "m"}, reserved)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}
	settled, err := svc.FulfillJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("FulfillJob: %v", err)
	}

	if settled.Units >= reserved {
		t.Fatalf("billed %d units for a five-character answer; the whole reservation was taken", settled.Units)
	}
	// The ceiling for two bytes of prompt and five of completion is the floor,
	// which is what an exchange this small should cost at most.
	if settled.Units > maxUnitsFloor {
		t.Fatalf("billed %d units, above the %d ceiling for this much text", settled.Units, maxUnitsFloor)
	}
	// And the buyer can still see what was CLAIMED, next to what was charged.
	// Units and Usage answer different questions and both are recorded, which is
	// what makes the difference auditable rather than invisible.
	if settled.Usage.TotalTokens != reserved {
		t.Fatalf("the provider's claim of %d was not recorded; got %d",
			reserved, settled.Usage.TotalTokens)
	}
}

// TestAnHonestBillIsUntouched. The ceiling is four times looser than real
// tokenisation on purpose: it exists to stop a bill two orders of magnitude past
// the work, not to argue with a backend about rounding. A long, genuine exchange
// must settle at exactly what the backend reported.
func TestAnHonestBillIsUntouched(t *testing.T) {
	// A realistic ratio: roughly four bytes per token.
	prompt := strings.Repeat("the quick brown fox jumps over the lazy dog. ", 100) // ~4400 bytes
	completion := strings.Repeat("a considered reply of some length. ", 100)       // ~3400 bytes
	const honestTokens = (4400 + 3400) / 4

	honest := greedyBackend{completion: completion, claimTokens: honestTokens}
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := serviceWithBackend(t, honest, fs, 1, 10_000_000)

	job, err := svc.SubmitInferenceJob(buyer, provider, InferenceRequest{Prompt: prompt, Model: "m"}, honestTokens*2)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}
	settled, err := svc.FulfillJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("FulfillJob: %v", err)
	}
	if settled.Units != honestTokens {
		t.Fatalf("an honest bill of %d settled as %d; the ceiling is refusing real work",
			honestTokens, settled.Units)
	}
}

// TestTheCeilingIsAnUpperBoundNotAnEstimate. It must never sit below what a
// tokeniser could genuinely produce, or honest providers lose money on every
// job. One token per byte is the limit of any byte-level BPE, so the bound holds
// for text of any script.
func TestTheCeilingIsAnUpperBoundNotAnEstimate(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  InferenceRequest
		out  string
	}{
		{"ascii", InferenceRequest{Prompt: strings.Repeat("word ", 200)}, strings.Repeat("reply ", 200)},
		{"cjk, three bytes a character", InferenceRequest{Prompt: strings.Repeat("안녕하세요", 200)}, strings.Repeat("반갑습니다", 200)},
		{"emoji, four bytes a character", InferenceRequest{Prompt: strings.Repeat("🙂", 200)}, strings.Repeat("🙃", 200)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ceiling := MaxUnitsFor(tc.req, tc.out)
			// The strictest real tokeniser still needs at least one token per
			// byte-pair; one token per BYTE is above anything achievable.
			bytes := len(tc.req.Prompt) + len(tc.out)
			if ceiling < uint64(bytes) {
				t.Fatalf("ceiling %d is below the byte count %d, so an honest bill could be cut",
					ceiling, bytes)
			}
		})
	}

	// And a tiny exchange gets the floor rather than a bound so tight it argues
	// with a backend over template overhead.
	if got := MaxUnitsFor(InferenceRequest{Prompt: "hi"}, "yo"); got != maxUnitsFloor {
		t.Fatalf("MaxUnitsFor on a tiny exchange = %d, want the floor %d", got, maxUnitsFloor)
	}
}
