package marketexchange

import (
	"context"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
	"github.com/ecirlabs/matrix-core/internal/transport"
)

// The window is short and the clock is the real one, deliberately.
//
// Market stamps and validates quotes against the wall clock and the Exchange
// takes an injectable one, so a test that advanced a fake clock would have the
// two disagreeing by hours - the order book would read its own renewal as
// observed in the future and drop it, which is an artefact of the harness and
// not of the code. Running both on one clock and shortening the window instead
// tests the real paths. The renewal rule is proportional (half the window,
// restated at the same length), so a window measured in milliseconds exercises
// exactly what a window measured in days does, and quote_ttl is configurable, so
// a short one is a real configuration rather than an invented one.
const (
	testQuoteWindow  = 600 * time.Millisecond
	testAnnounceTick = 40 * time.Millisecond
)

type sellerAndBuyer struct {
	sellerNode  *token.Account
	payoutID    string
	market      *market.Market
	seller      *Exchange
	buyer       *Exchange
	transport   *fakeTransport
	announceErr error
}

// announceTick runs one turn of the announce loop in the order announceOnce
// runs it, and delivers whatever reached the topic to the listening node.
func (s *sellerAndBuyer) announceTick(t *testing.T) {
	t.Helper()
	s.market.RenewProviderQuotes(time.Now().UTC())
	for _, p := range s.market.ListProviders() {
		if p.Suspended || p.Available == 0 {
			continue
		}
		// Swallowed exactly as announceOnce swallows it. That silence is the
		// symptom: nothing is logged and the seller simply stops appearing.
		s.announceErr = s.seller.AnnounceProvider(context.Background(), s.sellerNode, p)
	}
	if data := s.transport.lastPublished(TopicAnnounce); data != nil {
		s.buyer.handleAnnouncement(transport.Message{Topic: TopicAnnounce, Payload: data})
	}
}

func newSellerAndBuyer(t *testing.T, window time.Duration, providerTTL time.Duration) *sellerAndBuyer {
	t.Helper()
	sellerNode := mustAccount(t)
	payout := mustAccount(t)

	_, sellerMarket := newSettled(t)
	now := time.Now().UTC()
	if err := sellerMarket.RegisterProvider(market.Provider{
		ID:           payout.AccountID(),
		Capacity:     1000,
		PricePerUnit: 7,
		Models:       []string{"qwen3-32b"},
		ObservedAt:   now,
		ValidUntil:   now.Add(window),
	}); err != nil {
		t.Fatalf("register the provider: %v", err)
	}

	sellerSettled, _ := newSettled(t)
	seller, err := New(Config{
		Transport: newFakeTransport(),
		Settled:   sellerSettled,
		Market:    sellerMarket,
		Endpoint:  "https://gpu.example.com",
		PeerID:    "peer-seller",
	})
	if err != nil {
		t.Fatalf("new seller exchange: %v", err)
	}

	buyerSettled, _ := newSettled(t)
	buyer, err := New(Config{
		Transport:   newFakeTransport(),
		Settled:     buyerSettled,
		PeerID:      "peer-buyer",
		ProviderTTL: providerTTL,
	})
	if err != nil {
		t.Fatalf("new buyer exchange: %v", err)
	}

	return &sellerAndBuyer{
		sellerNode: sellerNode,
		payoutID:   payout.AccountID(),
		market:     sellerMarket,
		seller:     seller,
		buyer:      buyer,
		transport:  seller.transport.(*fakeTransport),
	}
}

// A seller that stays up past its quote window must stay visible to the network.
//
// This is the whole failure, end to end and on both sides of the wire. A quote's
// validity window was stamped once, at registration, and nothing moved it. Past
// it the seller's own order book stopped returning the provider - so the announce
// loop, which walks that order book, had nothing left to announce, and every
// listener aged the seller out. The GPU box was healthy throughout: funded,
// answering, announcing on schedule. It just stopped existing to everybody,
// including itself, one quote window after it last restarted - which is why every
// rollout hid it, and why the public marketplace read as empty rather than as
// broken.
func TestASellerStaysVisiblePastTheWindowItsQuoteWasStampedWith(t *testing.T) {
	s := newSellerAndBuyer(t, testQuoteWindow, time.Minute)

	s.announceTick(t)
	if got := len(s.buyer.ListRemoteProviders()); got != 1 {
		t.Fatalf("the buyer heard %d sellers at the start, want 1", got)
	}

	// Several windows of an ordinary, healthy node: announcing every interval,
	// nobody touching it, no restart.
	deadline := time.Now().Add(4 * testQuoteWindow)
	ticks := 0
	for time.Now().Before(deadline) {
		time.Sleep(testAnnounceTick)
		s.announceTick(t)
		ticks++
		if got := len(s.buyer.ListRemoteProviders()); got != 1 {
			t.Fatalf("the buyer stopped seeing the seller on tick %d (it heard %d); last announce said: %v",
				ticks, got, s.announceErr)
		}
	}

	// And it is not merely listed: the offer is usable, on the same terms.
	rp, ok := s.buyer.LookupRemoteProvider(s.payoutID)
	if !ok {
		t.Fatal("the seller is listed but cannot be looked up for routing")
	}
	if rp.PricePerUnit != 7 {
		t.Errorf("price drifted across %d renewing announcements: %d, want 7", ticks, rp.PricePerUnit)
	}
	if !rp.ValidUntil.After(time.Now().UTC()) {
		t.Errorf("the buyer holds an expired quote: valid until %s", rp.ValidUntil)
	}
	if !rp.ServesModel("qwen3-32b") {
		t.Error("the model advertisement did not survive renewal, so nothing routes to it")
	}
}

// Renewal must not hold a dead seller in the directory. Suspension is the health
// check's word that the backend behind it stopped answering, and going quiet is
// the honest signal - the same one a crashed node sends. This is the one case the
// old behaviour got right, and the fix must not take it away.
func TestASuspendedSellerStillFallsOutOfTheDirectory(t *testing.T) {
	s := newSellerAndBuyer(t, testQuoteWindow, 200*time.Millisecond)

	s.announceTick(t)
	if got := len(s.buyer.ListRemoteProviders()); got != 1 {
		t.Fatalf("the buyer heard %d sellers, want 1", got)
	}

	// The model server stops answering and the health check suspends it.
	if _, err := s.market.SetProviderSuspended(s.payoutID, true); err != nil {
		t.Fatalf("SetProviderSuspended: %v", err)
	}

	// Past the window, and past the buyer's receipt TTL.
	deadline := time.Now().Add(testQuoteWindow + 300*time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(testAnnounceTick)
		if got := s.market.RenewProviderQuotes(time.Now().UTC()); len(got) != 0 {
			t.Fatalf("renewed %v, but the backend behind it is not answering", got)
		}
	}
	if got := len(s.buyer.ListRemoteProviders()); got != 0 {
		t.Fatalf("the buyer still lists %d sellers whose backend is down", got)
	}
}
