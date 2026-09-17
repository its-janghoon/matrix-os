package consensus

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// A reservation, from the deposit that lets the answer stream to the refund that
// makes it fair.
//
// The unit tests beside this one check that a reservation has exactly one
// spelling. These check that four nodes agree about what settling one DOES,
// which is the property the whole activation exists to produce: the split is a
// rule applied from the committed block, not a refund the seller chooses to make.

const testJobID = "3f2b7c11-4e5a-4c8d-9b0f-1a2b3c4d5e6f"

// escrowActive schedules the activation at height 1, the earliest a schedule can
// name: normalizeUpgrades puts the genesis version at height 0, so a second
// version there would be two versions activating at one height.
func escrowActive(c *Config) {
	c.ProtocolUpgrades = append(c.ProtocolUpgrades,
		ProtocolUpgrade{Height: 1, Version: ProtocolVersionInferenceEscrow})
}

func reservationFor(payer, provider string, reserved uint64, expiry time.Time) InferEscrow {
	return InferEscrow{
		JobID:    testJobID,
		Reserved: reserved,
		Expiry:   uint64(expiry.Unix()),
		Provider: provider,
		Payer:    payer,
	}
}

// The shape the proposal exists for: pay the cap up front, settle for the real
// number, and have consensus hand back the difference.
func TestTheReservationIsPaidUpFrontAndTheChangeComesBack(t *testing.T) {
	nodes, stop := newCluster(t, 4, escrowActive)
	defer stop()

	payer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate payer: %v", err)
	}
	provider, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate provider: %v", err)
	}

	const funded = 1_000_000
	const reserved = 200_000
	const actual = 37_000
	mintAll(t, nodes, payer.AccountID(), funded)

	res := reservationFor(payer.AccountID(), provider.AccountID(), reserved, time.Now().Add(time.Hour))

	// The deposit. After this the provider is guaranteed the most this job can
	// cost, which is what leaves the completion with nothing to protect.
	submitAll(t, nodes, signedTransfer(t, payer, res.Account(), reserved, 1))
	waitFor(t, 20*time.Second, "the reservation to be funded on every node", func() bool {
		return everyNodeHas(nodes, res.Account(), reserved)
	})
	if bal, _ := nodes[0].ledger.Balance(payer.AccountID()); bal != funded-reserved {
		t.Fatalf("payer should be down the whole reservation, has %d", bal)
	}

	// The settlement names the actual. The payer signs it, which is the point:
	// the only thing that ever bounded a bill from the buyer's side is their
	// power to name a smaller number than the cap.
	submitAll(t, nodes, signedTransfer(t, payer, res.SettleRecipient(), actual, 2))
	waitFor(t, 20*time.Second, "the provider to be paid on every node", func() bool {
		return everyNodeHas(nodes, provider.AccountID(), actual)
	})

	// The escrow is emptied, not left holding the change.
	waitFor(t, 20*time.Second, "the escrow to be empty on every node", func() bool {
		return everyNodeHas(nodes, res.Account(), 0)
	})
	waitFor(t, 20*time.Second, "the refund to land on every node", func() bool {
		return everyNodeHas(nodes, payer.AccountID(), funded-actual)
	})
}

// What stops a buyer reading the answer and never settling, and therefore what
// lets the provider stream before being paid the real number.
func TestAProviderClaimsOnlyAfterTheExpiry(t *testing.T) {
	nodes, stop := newCluster(t, 4, escrowActive)
	defer stop()

	payer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate payer: %v", err)
	}
	provider, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate provider: %v", err)
	}

	const funded = 500_000
	const reserved = 120_000
	mintAll(t, nodes, payer.AccountID(), funded)

	// Already expired, so the claim is allowed the moment it lands. The refusal
	// case is checked below against a reservation that has not.
	past := reservationFor(payer.AccountID(), provider.AccountID(), reserved, time.Now().Add(-time.Hour))
	future := reservationFor(payer.AccountID(), provider.AccountID(), reserved, time.Now().Add(time.Hour))
	future.JobID = "9c1d2e3f-4a5b-4c6d-8e9f-0a1b2c3d4e5f"

	submitAll(t, nodes, signedTransfer(t, payer, future.Account(), reserved, 1))
	waitFor(t, 20*time.Second, "the unexpired reservation to be funded", func() bool {
		return everyNodeHas(nodes, future.Account(), reserved)
	})

	// A claim before the expiry must move nothing. Taking the cap out from under
	// a buyer who still has the right to name the actual would make the expiry
	// decorative.
	submitAll(t, nodes, signedTransfer(t, provider, future.ClaimRecipient(), 0, 1))
	time.Sleep(6 * time.Second)
	if !everyNodeHas(nodes, future.Account(), reserved) {
		t.Fatal("an early claim moved money out of an unexpired reservation")
	}
	if bal, _ := nodes[0].ledger.Balance(provider.AccountID()); bal != 0 {
		t.Fatalf("provider was paid %d by an early claim", bal)
	}

	// Funding one that has already expired does nothing either: a reservation the
	// provider could take in the same block is a gift, not an escrow.
	submitAll(t, nodes, signedTransfer(t, payer, past.Account(), reserved, 2))
	time.Sleep(6 * time.Second)
	if !everyNodeHas(nodes, past.Account(), 0) {
		t.Fatal("an already-expired reservation accepted a deposit")
	}
}

// Settling twice is safe, because a client that retries a submission it never
// saw confirmed must not pay a provider twice out of one reservation.
func TestSettlingTwiceIsANoOpRatherThanASecondPayment(t *testing.T) {
	nodes, stop := newCluster(t, 4, escrowActive)
	defer stop()

	payer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate payer: %v", err)
	}
	provider, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate provider: %v", err)
	}

	const funded = 400_000
	const reserved = 100_000
	const actual = 25_000
	mintAll(t, nodes, payer.AccountID(), funded)

	res := reservationFor(payer.AccountID(), provider.AccountID(), reserved, time.Now().Add(time.Hour))
	submitAll(t, nodes, signedTransfer(t, payer, res.Account(), reserved, 1))
	waitFor(t, 20*time.Second, "the reservation to be funded", func() bool {
		return everyNodeHas(nodes, res.Account(), reserved)
	})

	submitAll(t, nodes, signedTransfer(t, payer, res.SettleRecipient(), actual, 2))
	waitFor(t, 20*time.Second, "the first settlement to land", func() bool {
		return everyNodeHas(nodes, provider.AccountID(), actual)
	})

	// A second settlement at a fresh nonce. The escrow is empty, so there is
	// nothing to pay from and nothing to refund.
	submitAll(t, nodes, signedTransfer(t, payer, res.SettleRecipient(), actual, 3))
	time.Sleep(6 * time.Second)
	if bal, _ := nodes[0].ledger.Balance(provider.AccountID()); bal != actual {
		t.Fatalf("the provider was paid twice for one reservation: %d", bal)
	}
	if bal, _ := nodes[0].ledger.Balance(payer.AccountID()); bal != funded-actual {
		t.Fatalf("the payer's balance moved on the second settlement: %d", bal)
	}
}

// The composition the decisions rest on: a budget funds the reservation, so a
// generous cap parks money the buyer already approved rather than their wallet.
// And the budget's own per-job cap still binds - otherwise opening an escrow
// would be the way around it.
func TestABudgetFundsTheReservationAndItsCapStillBinds(t *testing.T) {
	nodes, stop := newCluster(t, 4, func(c *Config) {
		budgetsActive(c)
		c.ProtocolUpgrades = append(c.ProtocolUpgrades,
			ProtocolUpgrade{Height: 2, Version: ProtocolVersionInferenceEscrow})
	})
	defer stop()

	buyer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate buyer: %v", err)
	}
	delegate, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate delegate: %v", err)
	}
	provider, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate provider: %v", err)
	}

	const funded = 1_000_000
	const budgetSize = 600_000
	mintAll(t, nodes, buyer.AccountID(), funded)

	budget := budgetFor(t, buyer, delegate.PublicKey, time.Now().Add(time.Hour).Unix())
	submitAll(t, nodes, signedTransfer(t, buyer, budget.Account(), budgetSize, 1))
	waitFor(t, 25*time.Second, "the budget to be funded", func() bool {
		return everyNodeHas(nodes, budget.Account(), budgetSize)
	})

	// Over the budget's per-job cap. Refused by the same rule that bounds a
	// draw, reached through a different door.
	tooBig := reservationFor(budget.Account(), provider.AccountID(), budget.PerJobCap+1, time.Now().Add(time.Hour))
	submitAll(t, nodes, signedTransfer(t, delegate, tooBig.Account(), tooBig.Reserved, 1))
	time.Sleep(6 * time.Second)
	if !everyNodeHas(nodes, tooBig.Account(), 0) {
		t.Fatal("a reservation above the budget's per-job cap was funded")
	}

	// Within it, and signed by the DELEGATE while the BUDGET pays.
	ok := reservationFor(budget.Account(), provider.AccountID(), budget.PerJobCap, time.Now().Add(time.Hour))
	ok.JobID = "9c1d2e3f-4a5b-4c6d-8e9f-0a1b2c3d4e5f"
	submitAll(t, nodes, signedTransfer(t, delegate, ok.Account(), ok.Reserved, 2))
	waitFor(t, 25*time.Second, "the budget-funded reservation to be funded", func() bool {
		return everyNodeHas(nodes, ok.Account(), ok.Reserved)
	})
	if bal, _ := nodes[0].ledger.Balance(budget.Account()); bal != budgetSize-ok.Reserved {
		t.Fatalf("the budget should have paid, has %d", bal)
	}
	if bal, _ := nodes[0].ledger.Balance(buyer.AccountID()); bal != funded-budgetSize {
		t.Fatalf("the wallet moved when the budget should have paid: %d", bal)
	}

	// The refund goes back to the BUDGET, not to the wallet: that is what keeps
	// it inside the amount the buyer bounded.
	const actual = 40_000
	submitAll(t, nodes, signedTransfer(t, delegate, ok.SettleRecipient(), actual, 3))
	waitFor(t, 25*time.Second, "the provider to be paid from the budget-funded escrow", func() bool {
		return everyNodeHas(nodes, provider.AccountID(), actual)
	})
	waitFor(t, 25*time.Second, "the change to return to the budget", func() bool {
		return everyNodeHas(nodes, budget.Account(), budgetSize-actual)
	})
}

// Structural refusals, checked without a cluster because they are decided from
// the transaction alone - which is exactly what lets the mempool gate and the
// block check share one function.
func TestStructurallyImpossibleOperationsAreRefused(t *testing.T) {
	payer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate payer: %v", err)
	}
	stranger, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate stranger: %v", err)
	}
	provider := hex.EncodeToString(make([]byte, 32))
	res := reservationFor(payer.AccountID(), provider, 100_000, time.Now().Add(time.Hour))

	for _, tc := range []struct {
		name string
		tx   *token.Transaction
	}{
		{"a deposit that is not the whole reservation",
			signedTransfer(t, payer, res.Account(), 99_999, 1)},
		{"a deposit from somebody else",
			signedTransfer(t, stranger, res.Account(), 100_000, 1)},
		{"a settlement above the reservation",
			signedTransfer(t, payer, res.SettleRecipient(), 100_001, 1)},
		{"a settlement of nothing",
			signedTransfer(t, payer, res.SettleRecipient(), 0, 1)},
		{"a settlement signed by a stranger",
			signedTransfer(t, stranger, res.SettleRecipient(), 1_000, 1)},
		{"a claim by anyone but the provider",
			signedTransfer(t, payer, res.ClaimRecipient(), 0, 1)},
		{"a claim naming an amount",
			signedTransfer(t, payer, res.ClaimRecipient(), 1, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := verifyInferTx(tc.tx); err == nil {
				t.Fatalf("accepted %q", tc.tx.To)
			}
		})
	}

	// And the one that must be accepted, so the refusals above are not passing
	// because everything is refused.
	if err := verifyInferTx(signedTransfer(t, payer, res.Account(), 100_000, 1)); err != nil {
		t.Fatalf("a well-formed deposit was refused: %v", err)
	}
	if err := verifyInferTx(signedTransfer(t, payer, res.SettleRecipient(), 100_000, 2)); err != nil {
		t.Fatalf("a settlement at exactly the reservation was refused: %v", err)
	}
}

// The other half of the expiry, and the half the provider relies on: once it
// passes, the whole reservation is theirs.
//
// Without this the buyer could read the answer and simply never settle, so the
// seller would be back to withholding the text - which is the thing escrow
// exists to stop.
func TestAtTheExpiryTheProviderTakesTheWholeReservation(t *testing.T) {
	nodes, stop := newCluster(t, 4, escrowActive)
	defer stop()

	payer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate payer: %v", err)
	}
	provider, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate provider: %v", err)
	}

	const funded = 300_000
	const reserved = 90_000
	mintAll(t, nodes, payer.AccountID(), funded)

	// Short enough to expire during the test, long enough that the deposit
	// lands while it is still open.
	expiry := time.Now().Add(12 * time.Second)
	res := reservationFor(payer.AccountID(), provider.AccountID(), reserved, expiry)

	submitAll(t, nodes, signedTransfer(t, payer, res.Account(), reserved, 1))
	waitFor(t, 20*time.Second, "the reservation to be funded before it expires", func() bool {
		return everyNodeHas(nodes, res.Account(), reserved)
	})

	// The clock that decides is the BLOCK's, so this waits on wall time only to
	// be sure the blocks being produced carry a timestamp past the expiry.
	for time.Now().Before(expiry.Add(2 * time.Second)) {
		time.Sleep(500 * time.Millisecond)
	}

	submitAll(t, nodes, signedTransfer(t, provider, res.ClaimRecipient(), 0, 1))
	waitFor(t, 25*time.Second, "the provider to take the whole reservation", func() bool {
		return everyNodeHas(nodes, provider.AccountID(), reserved)
	})
	waitFor(t, 25*time.Second, "the escrow to be empty", func() bool {
		return everyNodeHas(nodes, res.Account(), 0)
	})
	// The payer gets nothing back. That is the point: by here the work was done
	// and delivered, and a refund would make walking away free.
	if bal, _ := nodes[0].ledger.Balance(payer.AccountID()); bal != funded-reserved {
		t.Fatalf("the payer was refunded from a claimed reservation: %d", bal)
	}
}
