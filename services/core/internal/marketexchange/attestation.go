package marketexchange

import (
	"crypto/ed25519"
	"fmt"
	"time"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// Saying that a seller is operated by somebody in particular.
//
// WHY THIS EXISTS, and why it is the one badge worth having. A new network's
// directory is mostly strangers, and a buyer has no reason to send a first
// prompt to any of them: the settled history that would tell them apart does not
// exist yet, and a stake proves capital rather than competence. Somebody has to
// go first, and the honest way for the people running the network to do that is
// to run sellers themselves and SAY SO.
//
// WHAT IT IS NOT, because this is where a badge usually goes wrong. It is not a
// quality rating, an endorsement, or a claim that these sellers are better. It
// is an identity claim and nothing more: "the account this chain names as its
// maintainer says it operates this node". A reader who does not trust that
// account learns nothing from it, which is the correct outcome and the reason it
// is safe to ship.
//
// WHY IT CANNOT BE FORGED, which is the whole difference between this and a
// field a seller fills in. The attestation is signed by the MAINTAINER account,
// and a reader checks that signature against the maintainer their OWN chain says
// is in force - consensus state, agreed by every node, rotatable only by the
// current maintainer's own signature. A seller can put any bytes in its
// announcement; what it cannot do is produce that signature. Nothing here is
// taken on the seller's word, which is the same rule the stake and the settled
// history already follow.
//
// WHY IT BINDS A NODE, not just a provider. The attestation names the announcing
// node, and a verifier refuses one whose node does not match the announcement
// carrying it. Without that binding an attestation is a bearer token: anyone who
// saw one could paste it into their own listing and wear somebody else's badge.
//
// WHY IT EXPIRES. A box is decommissioned, sold, or repurposed, and an
// attestation with no end date outlives the arrangement it describes. Re-signing
// on a schedule is work the maintainer can automate; a permanent badge on a
// machine somebody else now owns is not recoverable at all.

// operatorAttestationDomain separates this signature from every other signed
// structure in the repo.
const operatorAttestationDomain = "matrix/market/operator-attestation/v1"

// MaxOperatorAttestationValidity bounds how far ahead one may be dated.
//
// A year would make re-signing a thing nobody does, which is the same as a badge
// that never expires. Thirty days is short enough that a stale arrangement
// lapses on its own and long enough that signing is a monthly chore.
const MaxOperatorAttestationValidity = 30 * 24 * time.Hour

// OperatorAttestation is the maintainer's signed statement that it operates a
// seller.
type OperatorAttestation struct {
	// NodeID is the announcing node this attestation is for. A verifier refuses
	// one that does not match the announcement carrying it, so it cannot be
	// lifted onto somebody else's listing.
	NodeID string `json:"node_id"`
	// ProviderID is the payout account being vouched for, so an attestation
	// covers one of a node's sellers rather than everything it ever lists.
	ProviderID string `json:"provider_id"`
	// Operator is a short human name for whoever runs it, shown to a reader.
	// It is inside the signature, so a seller cannot relabel somebody else's
	// attestation as its own brand.
	Operator string `json:"operator"`
	// IssuedAt and ExpiresAt are Unix nanoseconds.
	IssuedAt  int64 `json:"issued_at"`
	ExpiresAt int64 `json:"expires_at"`
	// PublicKey is the maintainer key that signed. The account id derived from
	// it is what a verifier compares against the maintainer in force.
	PublicKey ed25519.PublicKey `json:"public_key"`
	Signature []byte            `json:"signature"`
}

// signingBytes is the canonical payload the signature covers.
func (a *OperatorAttestation) signingBytes() []byte {
	buf := make([]byte, 0, 256)
	buf = appendLenPrefixed(buf, []byte(operatorAttestationDomain))
	buf = appendLenPrefixed(buf, []byte(a.NodeID))
	buf = appendLenPrefixed(buf, []byte(a.ProviderID))
	buf = appendLenPrefixed(buf, []byte(a.Operator))
	buf = appendUint64(buf, uint64(a.IssuedAt))
	buf = appendUint64(buf, uint64(a.ExpiresAt))
	buf = appendLenPrefixed(buf, a.PublicKey)
	return buf
}

// AttestorID is the account id of the key that signed, which is what a verifier
// compares against the maintainer the chain names.
func (a *OperatorAttestation) AttestorID() string {
	if len(a.PublicKey) != ed25519.PublicKeySize {
		return ""
	}
	return token.AccountIDFromPublicKey(a.PublicKey)
}

// Sign signs the attestation with the maintainer's key.
func (a *OperatorAttestation) Sign(maintainer *token.Account) error {
	if maintainer == nil || len(maintainer.PrivateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: a maintainer signing key is required", ErrInvalidMessage)
	}
	a.PublicKey = maintainer.PublicKey
	a.Signature = ed25519.Sign(maintainer.PrivateKey, a.signingBytes())
	return nil
}

// VerifyFor checks an attestation against the announcement carrying it and the
// maintainer the reader's own chain says is in force.
//
// Every argument is something the READER supplies, not the seller: the node id
// comes from the announcement's own signature, and the maintainer id from
// consensus state. That is what makes this a check rather than a claim.
//
// An empty maintainerID means the reader's chain names no maintainer, and every
// attestation is then refused. That is the right default: a network with nobody
// to vouch has no verified sellers, rather than everybody being verified by an
// account that does not exist.
func (a *OperatorAttestation) VerifyFor(announcingNodeID, providerID, maintainerID string, now time.Time) error {
	if a == nil {
		return fmt.Errorf("%w: no attestation", ErrInvalidMessage)
	}
	if maintainerID == "" {
		return fmt.Errorf("%w: this chain names no maintainer, so nothing can be attested", ErrInvalidMessage)
	}
	if len(a.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: attestor key must be %d bytes", ErrInvalidMessage, ed25519.PublicKeySize)
	}
	if a.AttestorID() != maintainerID {
		return fmt.Errorf("%w: signed by %s, which is not the maintainer this chain names (%s)",
			ErrInvalidMessage, a.AttestorID(), maintainerID)
	}
	// Bound to the announcement carrying it. Without this an attestation is a
	// bearer token anyone who saw one could paste into their own listing.
	if a.NodeID != announcingNodeID {
		return fmt.Errorf("%w: attests node %s but travelled with %s", ErrInvalidMessage, a.NodeID, announcingNodeID)
	}
	if a.ProviderID != providerID {
		return fmt.Errorf("%w: attests provider %s but travelled with %s", ErrInvalidMessage, a.ProviderID, providerID)
	}
	if len(a.Signature) == 0 {
		return ErrUnsignedMessage
	}
	if !ed25519.Verify(a.PublicKey, a.signingBytes(), a.Signature) {
		return ErrInvalidSignature
	}

	issued := time.Unix(0, a.IssuedAt).UTC()
	expires := time.Unix(0, a.ExpiresAt).UTC()
	if !expires.After(issued) {
		return fmt.Errorf("%w: attestation expires before it was issued", ErrInvalidMessage)
	}
	if expires.Sub(issued) > MaxOperatorAttestationValidity {
		return fmt.Errorf("%w: attestation is valid for longer than %s", ErrInvalidMessage, MaxOperatorAttestationValidity)
	}
	if !expires.After(now) {
		return fmt.Errorf("%w: attestation expired at %s", ErrInvalidMessage, expires.Format(time.RFC3339))
	}
	if issued.After(now.Add(MaxQuoteClockSkewForAttestation)) {
		return fmt.Errorf("%w: attestation is dated in the future", ErrInvalidMessage)
	}
	return nil
}

// MaxQuoteClockSkewForAttestation is the future tolerance, matching what an
// announcement already allows so one clock does not pass a quote and fail its
// attestation.
const MaxQuoteClockSkewForAttestation = 2 * time.Minute
