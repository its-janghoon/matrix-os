package marketexchange

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

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
	ann := ProviderAnnouncement{
		ProviderID:   token.AccountIDFromPublicKey(pub),
		PublicKey:    pub,
		Capacity:     10,
		PricePerUnit: 3,
		Available:    10,
		PeerID:       "peer",
		Models:       []string{"llama-3.3-70b"},
		Timestamp:    1,
	}
	if err := ann.Sign(priv); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := ann.Verify(); err != nil {
		t.Fatalf("a freshly signed announcement should verify: %v", err)
	}

	tampered := ann
	tampered.Models = []string{"llama-3.3-70b", "gpt-4o"}
	if err := tampered.Verify(); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("adding a model gave %v, want ErrInvalidSignature", err)
	}

	dropped := ann
	dropped.Models = nil
	if err := dropped.Verify(); !errors.Is(err, ErrInvalidSignature) {
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
