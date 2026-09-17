package cli

import (
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/inference"
)

// THE BUYER'S CEILING MUST NOT BE TIGHTER THAN THE NODE'S.
//
// Both sides run the same arithmetic over the same text; the node's copy runs on
// the SELLER's machine, which is why the buyer runs it again. But a buyer who
// computes it over LESS text than the node computed it over refuses an invoice
// the node considers honest - and from the buyer's chair that reads as the
// seller cheating, which is the one conclusion this check must never produce by
// mistake.
//
// It did. The first live escrowed sale on this chain streamed its answer and was
// then refused, because the CLI passed "" for the model's working: reasoning
// never travels as a delta, it arrives on the final frame, and the node bills
// for it.
func TestTheBuyersCeilingIsNotTighterThanTheNodes(t *testing.T) {
	req := inference.InferenceRequest{Prompt: "what is a marketplace for?"}
	completion := "A place where strangers can trade without trusting each other."
	reasoning := strings.Repeat("the model thinking out loud. ", 20)
	const price = 1000

	// What the node would charge for exactly this exchange, at its own ceiling.
	nodeCeiling := inference.MaxUnitsFor(req, completion, reasoning)
	nodeAsks := nodeCeiling * price

	if err := checkEscrowBill(req, completion, reasoning, nodeAsks, nodeAsks*10, price); err != nil {
		t.Fatalf("the buyer refused a bill at the node's own ceiling: %v", err)
	}

	// And dropping the working is exactly the mistake: the same bill is refused.
	if err := checkEscrowBill(req, completion, "", nodeAsks, nodeAsks*10, price); err == nil {
		t.Fatal("checking without the working accepted the bill, so this test proves nothing; " +
			"the reasoning must be long enough to move the ceiling")
	}
}

// The ceiling still bites. A bill the answer cannot account for is refused even
// with the working counted, which is the whole point of computing it here.
func TestAnInflatedBillIsStillRefusedWithTheWorkingCounted(t *testing.T) {
	req := inference.InferenceRequest{Prompt: "hi"}
	const price = 1000
	ceiling := inference.MaxUnitsFor(req, "hello", "thinking")

	err := checkEscrowBill(req, "hello", "thinking", (ceiling+1)*price, (ceiling+100)*price, price)
	if err == nil {
		t.Fatal("a bill one unit over the ceiling was accepted")
	}
	// The refusal carries the arithmetic, because two sides disagreeing about a
	// ceiling are usually holding different text rather than arguing about maths.
	for _, want := range []string{"bytes of prompt", "of answer", "model's working"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say what it measured (%q missing): %v", want, err)
		}
	}
}

// A settlement over the reservation is refused before the ceiling is consulted:
// consensus refuses it too, and saying "the answer cannot account for it" would
// name the wrong reason.
func TestASettlementOverTheReservationIsRefusedOnItsOwnTerms(t *testing.T) {
	req := inference.InferenceRequest{Prompt: "hi"}
	err := checkEscrowBill(req, "hello", "", 2000, 1000, 1)
	if err == nil || !strings.Contains(err.Error(), "more than the 1000 reserved") {
		t.Fatalf("want a refusal naming the reservation, got %v", err)
	}
}
