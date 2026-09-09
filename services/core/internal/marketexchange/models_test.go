package marketexchange

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// TestModelsAreCoveredByTheAnnouncementSignature proves the model list cannot be
// edited in flight. Models decide routing, so an unsigned list would let any
// relaying peer advertise capabilities on another provider's behalf.
func TestModelsAreCoveredByTheAnnouncementSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	now := time.Date(2025, time.June, 7, 8, 9, 10, 11, time.UTC)
	ann := ProviderAnnouncement{
		ProviderID:        token.AccountIDFromPublicKey(pub),
		PublicKey:         pub,
		Capacity:          10,
		PricePerUnit:      3,
		CostPerUnit:       2,
		MarkupBasisPoints: 100,
		QuoteID:           "models-quote",
		QuoteVersion:      1,
		ObservedAt:        now,
		ValidUntil:        now.Add(time.Hour),
		Available:         10,
		PeerID:            "peer",
		Models:            []string{"llama-3.3-70b"},
		Timestamp:         now.UnixNano(),
	}
	if err := ann.Sign(priv); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := ann.VerifyAt(now); err != nil {
		t.Fatalf("a freshly signed announcement should verify: %v", err)
	}

	tampered := ann
	tampered.Models = []string{"llama-3.3-70b", "gpt-4o"}
	if err := tampered.VerifyAt(now); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("adding a model gave %v, want ErrInvalidSignature", err)
	}

	dropped := ann
	dropped.Models = nil
	if err := dropped.VerifyAt(now); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("dropping the model list gave %v, want ErrInvalidSignature", err)
	}
}

// TestAnEmptyModelListAndOneEmptyModelDoNotShareAPayload is why the count is
// length-prefixed alongside the entries: without it both would serialize to the
// same bytes and one signature would cover both.
func TestAnEmptyModelListAndOneEmptyModelDoNotShareAPayload(t *testing.T) {
	base := ProviderAnnouncement{ProviderID: "p", PeerID: "peer", Timestamp: 1}
	none := base
	none.Models = nil
	blank := base
	blank.Models = []string{""}

	if string(none.signingBytes()) == string(blank.signingBytes()) {
		t.Fatal("an empty list and a single empty model must not sign to the same payload")
	}
}

// TestProviderAnnouncementSigningDomainIsExplicitV2 locks the incompatible
// announcement protocol change to both a v2 gossip topic and a signed v2 domain.
func TestProviderAnnouncementSigningDomainIsExplicitV2(t *testing.T) {
	payload := (&ProviderAnnouncement{}).signingBytes()
	if len(payload) < 4 {
		t.Fatal("signing payload is missing its length-prefixed domain")
	}
	domainLen := int(binary.BigEndian.Uint32(payload[:4]))
	if len(payload) < 4+domainLen {
		t.Fatal("signing payload contains a truncated domain")
	}
	if got := string(payload[4 : 4+domainLen]); got != providerAnnouncementSigningDomain {
		t.Fatalf("signed domain = %q, want %q", got, providerAnnouncementSigningDomain)
	}
	if providerAnnouncementSigningDomain != "matrix/market/provider-announcement/v2" {
		t.Fatalf("announcement signing domain = %q, want explicit v2", providerAnnouncementSigningDomain)
	}
	if TopicAnnounce != "matrix/market/announce/v2" {
		t.Fatalf("announcement topic = %q, want explicit v2", TopicAnnounce)
	}
}

// TestJobRequestSigningDomainIsExplicitV2 locks both halves of the incompatible
// request protocol version: rendezvous topic and signed domain.
func TestJobRequestSigningDomainIsExplicitV2(t *testing.T) {
	payload := (&JobRequest{}).signingBytes()
	if len(payload) < 4 {
		t.Fatal("signing payload is missing its length-prefixed domain")
	}
	domainLen := int(binary.BigEndian.Uint32(payload[:4]))
	if len(payload) < 4+domainLen {
		t.Fatal("signing payload contains a truncated domain")
	}
	if got := string(payload[4 : 4+domainLen]); got != jobRequestSigningDomain {
		t.Fatalf("signed domain = %q, want %q", got, jobRequestSigningDomain)
	}
	if jobRequestSigningDomain != "matrix/market/job-request/v2" {
		t.Fatalf("job request signing domain = %q, want explicit v2", jobRequestSigningDomain)
	}
	if TopicJobs != "matrix/market/jobs/v2" {
		t.Fatalf("job topic = %q, want explicit v2", TopicJobs)
	}
}
