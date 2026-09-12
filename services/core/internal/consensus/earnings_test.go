package consensus

import (
	"testing"

	"github.com/ecirlabs/matrix-core/internal/kv"
)

func earningsStore(t *testing.T) *EarningsStore {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewEarningsStore(store)
}

// TestPayersCountsDistinctAccountsNotPayments is the whole reason the payer
// count exists.
//
// Total received is one self-transfer away from any number a seller likes; the
// only cost is the protocol fee. A payer count cannot be moved that way - each
// new payer is an account somebody had to fund - so it is the figure a buyer
// should weigh, and it is worthless if repeat customers inflate it.
func TestPayersCountsDistinctAccountsNotPayments(t *testing.T) {
	s := earningsStore(t)
	const seller = "eth:0x00000000000000000000000000000000000000aa"

	// One buyer, three purchases, across two blocks.
	if err := s.record(10, []payment{
		{payer: "buyer-1", payee: seller, amount: 100},
		{payer: "buyer-1", payee: seller, amount: 50},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := s.record(11, []payment{{payer: "buyer-1", payee: seller, amount: 25}}); err != nil {
		t.Fatalf("record: %v", err)
	}

	got, err := s.Get(seller)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Payers != 1 {
		t.Fatalf("Payers = %d, want 1 - repeat purchases inflated the payer count", got.Payers)
	}
	if got.Payments != 3 {
		t.Fatalf("Payments = %d, want 3", got.Payments)
	}
	if got.Received != 175 {
		t.Fatalf("Received = %d, want 175", got.Received)
	}

	// A second buyer moves the payer count, and only then.
	if err := s.record(12, []payment{{payer: "buyer-2", payee: seller, amount: 1}}); err != nil {
		t.Fatalf("record: %v", err)
	}
	got, _ = s.Get(seller)
	if got.Payers != 2 {
		t.Fatalf("Payers = %d, want 2", got.Payers)
	}
}

// TestTwoPaymentsFromOneNewPayerInOneBlockCountOnce. The per-block fold stages
// pair markers before they are written, so a payer appearing twice in the SAME
// block must not be counted twice - the store read still says "unseen" for the
// second one, since nothing has been committed yet.
func TestTwoPaymentsFromOneNewPayerInOneBlockCountOnce(t *testing.T) {
	s := earningsStore(t)
	const seller = "seller"

	if err := s.record(5, []payment{
		{payer: "fresh", payee: seller, amount: 10},
		{payer: "fresh", payee: seller, amount: 10},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	got, _ := s.Get(seller)
	if got.Payers != 1 {
		t.Fatalf("Payers = %d, want 1 - one payer was counted twice within a block", got.Payers)
	}
	if got.Payments != 2 {
		t.Fatalf("Payments = %d, want 2", got.Payments)
	}
}

// TestHeightsBracketTheActivity. A seller working for months should read
// differently from one paid twice yesterday, so the first payment's height is
// pinned and never moves.
func TestHeightsBracketTheActivity(t *testing.T) {
	s := earningsStore(t)
	const seller = "seller"

	if err := s.record(100, []payment{{payer: "a", payee: seller, amount: 1}}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := s.record(900, []payment{{payer: "b", payee: seller, amount: 1}}); err != nil {
		t.Fatalf("record: %v", err)
	}
	got, _ := s.Get(seller)
	if got.FirstHeight != 100 {
		t.Fatalf("FirstHeight = %d, want 100 - it moved with the latest payment", got.FirstHeight)
	}
	if got.LastHeight != 900 {
		t.Fatalf("LastHeight = %d, want 900", got.LastHeight)
	}
}

// TestTheTallySaysWhereItStarted. A node that upgraded into this code counts
// from the upgrade, not from genesis. A display that cannot say so is claiming
// to have counted history it never saw.
func TestTheTallySaysWhereItStarted(t *testing.T) {
	s := earningsStore(t)

	if _, started, err := s.IndexedFrom(); err != nil || started {
		t.Fatalf("a fresh store reports indexing started: started=%v err=%v", started, err)
	}

	if err := s.record(742, []payment{{payer: "a", payee: "seller", amount: 1}}); err != nil {
		t.Fatalf("record: %v", err)
	}
	from, started, err := s.IndexedFrom()
	if err != nil || !started {
		t.Fatalf("IndexedFrom after a record: started=%v err=%v", started, err)
	}
	if from != 742 {
		t.Fatalf("IndexedFrom = %d, want the first indexed height 742", from)
	}

	// And it stays put, or a long-running node would report a start that walks
	// forward and a window that shrinks to nothing.
	if err := s.record(900, []payment{{payer: "b", payee: "seller", amount: 1}}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if from, _, _ := s.IndexedFrom(); from != 742 {
		t.Fatalf("IndexedFrom = %d after a later block, want it pinned at 742", from)
	}
}

// TestAnUnpaidAccountIsZeroNotAnError. "Nobody has paid this seller" is the
// commonest answer on a young network and is not a failure; returning an error
// would drop the seller off a directory listing for having no history.
func TestAnUnpaidAccountIsZeroNotAnError(t *testing.T) {
	s := earningsStore(t)
	got, err := s.Get("never-paid")
	if err != nil {
		t.Fatalf("Get on an unknown account: %v", err)
	}
	if got != (Earnings{}) {
		t.Fatalf("got %+v, want a zero record", got)
	}
}

// TestEachSellerIsTalliedSeparately guards the obvious way to get this wrong:
// one seller's history showing up under another's name would put a stranger's
// track record in front of a buyer.
func TestEachSellerIsTalliedSeparately(t *testing.T) {
	s := earningsStore(t)
	if err := s.record(1, []payment{
		{payer: "buyer", payee: "seller-a", amount: 100},
		{payer: "buyer", payee: "seller-b", amount: 7},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	a, _ := s.Get("seller-a")
	b, _ := s.Get("seller-b")
	if a.Received != 100 || b.Received != 7 {
		t.Fatalf("seller-a = %d, seller-b = %d; want 100 and 7", a.Received, b.Received)
	}
	if a.Payers != 1 || b.Payers != 1 {
		t.Fatalf("payer counts leaked between sellers: %d and %d", a.Payers, b.Payers)
	}
}
