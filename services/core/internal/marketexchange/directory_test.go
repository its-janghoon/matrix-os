package marketexchange

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
	"github.com/ecirlabs/matrix-core/internal/transport"
)

// announce signs and delivers one announcement, as a received gossip message.
func announce(t *testing.T, ex *Exchange, node *token.Account, mutate func(*ProviderAnnouncement)) {
	t.Helper()
	ann := validProviderAnnouncement(node, ex.now())
	if mutate != nil {
		mutate(&ann)
	}
	if err := ann.Sign(node.PrivateKey); err != nil {
		t.Fatalf("sign: %v", err)
	}
	payload, err := marshalJSON(ann)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	ex.handleAnnouncement(transport.Message{Topic: TopicAnnounce, Payload: payload})
}

func directoryExchange(t *testing.T) *Exchange {
	t.Helper()
	settled, _ := newSettled(t)
	ex, err := New(Config{Transport: newFakeTransport(), Settled: settled, PeerID: "peer-self"})
	if err != nil {
		t.Fatalf("new exchange: %v", err)
	}
	return ex
}

// TestTheEndpointIsInsideTheSignature
//
// The endpoint is the one field in an announcement that a buyer's client
// CONNECTS TO. Outside the signature, any peer relaying the message could
// rewrite it and point somebody else's traffic at a host of its choosing - the
// buyer would still see the real provider's id, price and models, and send its
// prompt somewhere else entirely.
func TestTheEndpointIsInsideTheSignature(t *testing.T) {
	ex := directoryExchange(t)
	node := mustAccount(t)

	ann := validProviderAnnouncement(node, ex.now())
	ann.Endpoint = "https://honest.example:9093"
	if err := ann.Sign(node.PrivateKey); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := ann.VerifyAt(ex.now()); err != nil {
		t.Fatalf("a freshly signed announcement should verify: %v", err)
	}

	ann.Endpoint = "https://attacker.example:9093"
	if err := ann.VerifyAt(ex.now()); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("a rewritten endpoint was accepted, err = %v", err)
	}
}

// TestAnEndpointABuyerShouldNotDialIsRefused. Every other field that fails to
// validate costs a bad quote; a bad endpoint costs a request sent somewhere the
// buyer never intended, so the rules are narrow on purpose.
func TestAnEndpointABuyerShouldNotDialIsRefused(t *testing.T) {
	for _, tc := range []struct{ name, endpoint string }{
		{"a file url turns the directory into a local file reader", "file:///etc/passwd"},
		{"an ftp url is whatever the client library happens to support", "ftp://host/x"},
		{"a scheme-less string has no host to reach", "provider-a.example:9093"},
		{"credentials would authenticate the buyer as someone else", "https://user:pass@host:9093"},
		{"a path silently changes where the request lands", "https://host:9093/v1/chat/completions"},
		{"a query is not part of a base url", "https://host:9093?key=x"},
		{"no host is nothing to dial", "https://"},
		{"an oversized endpoint is payload, not an address", "https://" + strings.Repeat("a", MaxEndpointLen) + ".example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateEndpoint(tc.endpoint); !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("ValidateEndpoint(%q) = %v, want ErrInvalidMessage", tc.endpoint, err)
			}
		})
	}

	// Empty is valid and means "no HTTP address", which is the honest state of a
	// node selling compute units. Refusing it would keep that node out of the
	// directory entirely.
	if err := ValidateEndpoint(""); err != nil {
		t.Fatalf("an empty endpoint was refused: %v", err)
	}
	for _, good := range []string{"http://host:9093", "https://host", "https://host:9093/"} {
		if err := ValidateEndpoint(good); err != nil {
			t.Fatalf("ValidateEndpoint(%q) = %v, want nil", good, err)
		}
	}
}

// TestAPayoutAccountNeedNotBeTheSigner is the case v2 could not express, and the
// reason nothing ever announced on a real network.
//
// The GPU runbook tells an operator to list under the wallet address their
// revenue should land in. No ed25519 key derives an `eth:0x` account id, so
// under v2's rule - provider id must equal the signer's account id - such a
// provider could not be announced at all.
func TestAPayoutAccountNeedNotBeTheSigner(t *testing.T) {
	ex := directoryExchange(t)
	node := mustAccount(t)
	const wallet = "eth:0x00000000000000000000000000000000000000ab"

	announce(t, ex, node, func(a *ProviderAnnouncement) { a.ProviderID = wallet })

	rp, ok := ex.LookupRemoteProvider(wallet)
	if !ok {
		t.Fatal("a provider paid to a wallet address is not discoverable")
	}
	if rp.NodeID != node.AccountID() {
		t.Fatalf("announcing node = %q, want %q", rp.NodeID, node.AccountID())
	}
	if rp.ID != wallet {
		t.Fatalf("payout account = %q, want %q", rp.ID, wallet)
	}
}

// TestTwoNodesPayingOneAccountAreTwoEntries
//
// Keyed on the payout account alone, the second node's announcement would
// REPLACE the first: an operator who adds a second GPU box would watch their
// first one disappear from every directory on the network, and a buyer would see
// one offer where two exist. Keyed on the node alone, a node's second backend
// would erase its first.
func TestTwoNodesPayingOneAccountAreTwoEntries(t *testing.T) {
	ex := directoryExchange(t)
	first, second := mustAccount(t), mustAccount(t)
	const wallet = "eth:0x00000000000000000000000000000000000000cd"

	announce(t, ex, first, func(a *ProviderAnnouncement) {
		a.ProviderID = wallet
		a.Endpoint = "https://box-one.example:9093"
		a.PricePerUnit = 9
	})
	announce(t, ex, second, func(a *ProviderAnnouncement) {
		a.ProviderID = wallet
		a.Endpoint = "https://box-two.example:9093"
		a.PricePerUnit = 4
	})

	listed := ex.ListRemoteProviders()
	if len(listed) != 2 {
		t.Fatalf("got %d entries, want 2 - one box erased the other", len(listed))
	}

	// A caller naming only the payout account gets the cheapest offer for it, and
	// deterministically: two nodes reading the same announcements must choose the
	// same one or they quote buyers differently.
	best, ok := ex.LookupRemoteProvider(wallet)
	if !ok {
		t.Fatal("the payout account is not discoverable")
	}
	if best.PricePerUnit != 4 || best.Endpoint != "https://box-two.example:9093" {
		t.Fatalf("cheapest offer = %d at %s, want 4 at box-two", best.PricePerUnit, best.Endpoint)
	}

	// And a caller naming a specific node gets that node's own terms, which is
	// what anything quoting a buyer needs: the two boxes have different prices.
	one, ok := ex.LookupOffer(first.AccountID(), wallet)
	if !ok {
		t.Fatal("the first node's own offer is not addressable")
	}
	if one.PricePerUnit != 9 {
		t.Fatalf("first node's price = %d, want its own 9", one.PricePerUnit)
	}
}

// TestLivenessIsObservedAndAccumulates
//
// This is the honest half of a reputation. A seller's own uptime figure is a
// claim that costs nothing to inflate; "I have been hearing this node for six
// days and accepted 812 of its announcements" is something the reader watched.
//
// It has to ACCUMULATE to say anything. Reset on every quote refresh - which is
// most announcements - it would only ever report that the seller is there right
// now, which the receipt timestamp already says.
func TestLivenessIsObservedAndAccumulates(t *testing.T) {
	settled, _ := newSettled(t)
	clock := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	ex, err := New(Config{
		Transport: newFakeTransport(),
		Settled:   settled,
		PeerID:    "peer-self",
		Now:       func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("new exchange: %v", err)
	}
	node := mustAccount(t)

	announce(t, ex, node, nil)
	first := ex.ListRemoteProviders()[0]
	if first.Announcements != 1 || !first.FirstSeen.Equal(clock) {
		t.Fatalf("first announcement: heard %d, first seen %s", first.Announcements, first.FirstSeen)
	}

	// Later announcements, each carrying a fresh quote, as a live node's do.
	for i := 2; i <= 4; i++ {
		clock = clock.Add(10 * time.Minute)
		version := uint64(7 + i)
		announce(t, ex, node, func(a *ProviderAnnouncement) {
			a.QuoteVersion = version
			a.QuoteID = "quote-refreshed"
			a.ObservedAt = clock.Add(-time.Minute)
			a.ValidUntil = clock.Add(time.Hour)
			a.Timestamp = clock.UnixNano()
		})
	}

	got := ex.ListRemoteProviders()[0]
	if got.Announcements != 4 {
		t.Fatalf("heard %d announcements, want 4 - the count resets on a quote refresh", got.Announcements)
	}
	if !got.FirstSeen.Equal(time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("FirstSeen = %s, want the original sighting - it moved with the refresh", got.FirstSeen)
	}
	if !got.ReceivedAt.Equal(clock) {
		t.Fatalf("ReceivedAt = %s, want the latest sighting %s", got.ReceivedAt, clock)
	}
}

// TestAnAnnouncementCarriesNoSelfReportedReliability pins a deliberate absence.
//
// Uptime, latency and throughput are exactly the fields a marketplace is asked
// for and exactly the ones a seller can type any number into. Adding them would
// put a figure in front of buyers that looks measured and is not, and buyers
// would route on it. What a buyer can rely on is measured by the observer
// (FirstSeen, Announcements) or settled on the chain, where a paid job is a
// committed transfer nobody can fabricate.
func TestAnAnnouncementCarriesNoSelfReportedReliability(t *testing.T) {
	payload := (&ProviderAnnouncement{}).signingBytes()
	for _, claim := range []string{"uptime", "latency", "throughput", "success_rate", "reputation", "score"} {
		if strings.Contains(strings.ToLower(string(payload)), claim) {
			t.Fatalf("the announcement signs a self-reported %q; it is a claim, not a fact", claim)
		}
	}
}

// TestAnnouncingRequiresAProviderToName. Signing an announcement with no payout
// account would publish an offer that no buyer could settle against, and the
// node would do the work for nothing.
func TestAnnouncingRequiresAProviderToName(t *testing.T) {
	ex := directoryExchange(t)
	node := mustAccount(t)

	err := ex.AnnounceProvider(t.Context(), node, market.Provider{
		Capacity: 10, PricePerUnit: 1, Available: 10,
		QuoteID: "q", QuoteVersion: 1,
		ObservedAt: ex.now(), ValidUntil: ex.now().Add(time.Hour),
	})
	if err == nil {
		t.Fatal("an announcement with no payout account was published")
	}
}
