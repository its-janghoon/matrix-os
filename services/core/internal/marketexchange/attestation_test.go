package marketexchange

import (
	"errors"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/token"
)

func attestation(t *testing.T, maintainer *token.Account, node, provider string, now time.Time) *OperatorAttestation {
	t.Helper()
	att := &OperatorAttestation{
		NodeID:     node,
		ProviderID: provider,
		Operator:   "Matrix OS",
		IssuedAt:   now.UnixNano(),
		ExpiresAt:  now.Add(14 * 24 * time.Hour).UnixNano(),
	}
	if err := att.Sign(maintainer); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return att
}

// TestOnlyTheMaintainerTheChainNamesCanVouch
//
// This is the whole difference between a badge and a field a seller fills in.
// The signature is checked against the maintainer the READER's chain names -
// consensus state, agreed by every node, rotatable only by the current
// maintainer's own signature. A seller can put any bytes in its announcement;
// what it cannot do is produce that signature.
func TestOnlyTheMaintainerTheChainNamesCanVouch(t *testing.T) {
	now := time.Now().UTC()
	maintainer := mustAccount(t)
	impostor := mustAccount(t)

	att := attestation(t, maintainer, "node-1", "prov-1", now)
	if err := att.VerifyFor("node-1", "prov-1", maintainer.AccountID(), now); err != nil {
		t.Fatalf("the maintainer's own attestation does not verify: %v", err)
	}

	// Signed by somebody else, however well-formed.
	forged := attestation(t, impostor, "node-1", "prov-1", now)
	if err := forged.VerifyFor("node-1", "prov-1", maintainer.AccountID(), now); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("an attestation signed by a stranger was accepted: %v", err)
	}

	// And a genuine one is worthless on a chain that has rotated away from that
	// key, which is the property that makes rotation safe.
	rotated := mustAccount(t)
	if err := att.VerifyFor("node-1", "prov-1", rotated.AccountID(), now); !errors.Is(err, ErrInvalidMessage) {
		t.Fatal("an attestation kept vouching after the chain named a different maintainer")
	}
}

// TestAnAttestationCannotBeLiftedOntoAnotherListing
//
// Without binding, an attestation is a bearer token: anyone who saw one - and
// they travel in public gossip - could paste it into their own announcement and
// wear somebody else's badge.
func TestAnAttestationCannotBeLiftedOntoAnotherListing(t *testing.T) {
	now := time.Now().UTC()
	maintainer := mustAccount(t)
	att := attestation(t, maintainer, "the-real-node", "the-real-provider", now)

	if err := att.VerifyFor("somebody-elses-node", "the-real-provider", maintainer.AccountID(), now); !errors.Is(err, ErrInvalidMessage) {
		t.Fatal("an attestation verified for a node it was not issued to")
	}
	if err := att.VerifyFor("the-real-node", "somebody-elses-provider", maintainer.AccountID(), now); !errors.Is(err, ErrInvalidMessage) {
		t.Fatal("an attestation verified for a provider it was not issued for")
	}
}

// TestEveryClaimIsSigned. The operator NAME especially: a badge that says who
// vouched is worth something only if the who cannot be edited, or a seller would
// relabel a genuine attestation as its own brand.
func TestEveryClaimIsSigned(t *testing.T) {
	now := time.Now().UTC()
	maintainer := mustAccount(t)

	for _, tc := range []struct {
		name   string
		mutate func(*OperatorAttestation)
	}{
		{"the operator it names", func(a *OperatorAttestation) { a.Operator = "Somebody Else Entirely" }},
		{"the node", func(a *OperatorAttestation) { a.NodeID = "another-node" }},
		{"the provider", func(a *OperatorAttestation) { a.ProviderID = "another-provider" }},
		{"when it expires", func(a *OperatorAttestation) { a.ExpiresAt += int64(time.Hour) }},
		{"when it was issued", func(a *OperatorAttestation) { a.IssuedAt -= int64(time.Hour) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			att := attestation(t, maintainer, "node-1", "prov-1", now)
			tc.mutate(att)
			err := att.VerifyFor(att.NodeID, att.ProviderID, maintainer.AccountID(), now)
			if err == nil {
				t.Fatalf("%s was changed and the attestation still verified", tc.name)
			}
		})
	}
}

// TestAnAttestationExpires. A box is decommissioned, sold or repurposed, and a
// permanent badge on a machine somebody else now owns is not recoverable.
func TestAnAttestationExpires(t *testing.T) {
	now := time.Now().UTC()
	maintainer := mustAccount(t)
	att := attestation(t, maintainer, "node-1", "prov-1", now)

	stillGood := now.Add(13 * 24 * time.Hour)
	if err := att.VerifyFor("node-1", "prov-1", maintainer.AccountID(), stillGood); err != nil {
		t.Fatalf("refused before its expiry: %v", err)
	}
	lapsed := now.Add(15 * 24 * time.Hour)
	if err := att.VerifyFor("node-1", "prov-1", maintainer.AccountID(), lapsed); !errors.Is(err, ErrInvalidMessage) {
		t.Fatal("an expired attestation still vouched")
	}
}

// TestAnAttestationCannotBePermanent. Signing one for a decade is the same as
// never expiring, so the protocol refuses it rather than trusting the issuer to
// pick a sensible number.
func TestAnAttestationCannotBePermanent(t *testing.T) {
	now := time.Now().UTC()
	maintainer := mustAccount(t)

	att := &OperatorAttestation{
		NodeID: "node-1", ProviderID: "prov-1", Operator: "Matrix OS",
		IssuedAt:  now.UnixNano(),
		ExpiresAt: now.Add(10 * 365 * 24 * time.Hour).UnixNano(),
	}
	if err := att.Sign(maintainer); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := att.VerifyFor("node-1", "prov-1", maintainer.AccountID(), now); !errors.Is(err, ErrInvalidMessage) {
		t.Fatal("a decade-long attestation was accepted")
	}
}

// TestAChainWithNoMaintainerVouchesForNobody. The safe default: a network that
// names no maintainer has no verified sellers, rather than everyone being
// verified by an account that does not exist.
func TestAChainWithNoMaintainerVouchesForNobody(t *testing.T) {
	now := time.Now().UTC()
	att := attestation(t, mustAccount(t), "node-1", "prov-1", now)
	if err := att.VerifyFor("node-1", "prov-1", "", now); !errors.Is(err, ErrInvalidMessage) {
		t.Fatal("an attestation verified on a chain that names no maintainer")
	}
}

// TestTheAnnouncementSignatureCoversTheAttestation
//
// A relaying peer must be able neither to attach a badge nor to strip one. Both
// are the same defect: an announcement whose meaning a third party can change in
// flight.
func TestTheAnnouncementSignatureCoversTheAttestation(t *testing.T) {
	ex := directoryExchange(t)
	node := mustAccount(t)
	maintainer := mustAccount(t)
	now := ex.now()

	ann := validProviderAnnouncement(node, now)
	ann.Attestation = attestation(t, maintainer, node.AccountID(), ann.ProviderID, now)
	if err := ann.Sign(node.PrivateKey); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := ann.VerifyAt(now); err != nil {
		t.Fatalf("a freshly signed announcement does not verify: %v", err)
	}

	// Stripped in flight.
	stripped := ann
	stripped.Attestation = nil
	if err := stripped.VerifyAt(now); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("a peer removed the attestation and the announcement still verified: %v", err)
	}

	// Attached in flight, to an announcement that carried none.
	bare := validProviderAnnouncement(node, now)
	if err := bare.Sign(node.PrivateKey); err != nil {
		t.Fatalf("sign: %v", err)
	}
	bare.Attestation = attestation(t, maintainer, node.AccountID(), bare.ProviderID, now)
	if err := bare.VerifyAt(now); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("a peer attached an attestation and the announcement still verified: %v", err)
	}
}

// TestADiscoveredSellerCarriesItsAttestationUnverified
//
// The registry keeps what arrived and judges nothing. Verifying needs the
// maintainer the READER's chain names, and a stored verdict would also outlive a
// rotation it should not survive.
func TestADiscoveredSellerCarriesItsAttestationUnverified(t *testing.T) {
	ex := directoryExchange(t)
	node := mustAccount(t)
	maintainer := mustAccount(t)

	announce(t, ex, node, func(a *ProviderAnnouncement) {
		a.Attestation = attestation(t, maintainer, node.AccountID(), a.ProviderID, ex.now())
	})

	listed := ex.ListRemoteProviders()
	if len(listed) != 1 || listed[0].Attestation == nil {
		t.Fatal("the attestation did not survive the registry")
	}
	if listed[0].Attestation.Operator != "Matrix OS" {
		t.Fatalf("operator = %q", listed[0].Attestation.Operator)
	}
}
