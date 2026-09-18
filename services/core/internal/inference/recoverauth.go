package inference

import (
	"crypto/ed25519"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// Proving you may collect a settlement you are no longer streaming.
//
// StreamEscrowedInferenceJob takes the authorization the RESERVATION was opened
// with, compared rather than re-verified, because a RunAuthorization is
// single-use and verifying it twice would refuse the legitimate caller as a
// replay. That works while the reservation and the stream are the same process.
//
// RECOVERY is the case where they are not: a client that crashed between funding
// and settling, a tab that was closed, an operator coming back the next morning
// to a settlement their own ceiling check refused. None of them still holds that
// authorization, and asking them to keep one on disk would be asking them to
// persist a credential so a later process can replay it.
//
// So recovery takes a FRESH proof instead, signed now, over the job id. What it
// has to establish is narrow: that the caller holds the key that will sign the
// settlement. Anyone who can produce that can already settle the job, so handing
// them the settlement and the text discloses nothing they could not obtain by
// paying for it - which is the whole of what this gate is for.
//
// It is NOT a second way to authorise work. Nothing runs, nothing is charged,
// and calling it twice returns the same answer.

// RecoverAuthorizationWindow is how far a recovery proof's timestamp may be from
// the node's clock. Short, because there is no reason for one to travel: it is
// signed by the process making the call.
const RecoverAuthorizationWindow = 2 * time.Minute

// recoverAuthDomain separates these bytes from every other signature this
// codebase accepts. Without it, a signature over a job id could in principle be
// presented as a signature over some other structure that serialized the same
// way - and the point of a narrow credential is lost the moment it can be
// mistaken for a broad one.
const recoverAuthDomain = "matrix/inference/recover-authorization/v1"

// RecoverAuthorization is a fresh signature over one job id by the key that will
// sign that job's settlement.
type RecoverAuthorization struct {
	// PublicKey is the ed25519 key of the account that settles: the buyer, or a
	// budget's delegate.
	//
	// ED25519 ONLY, and that is a stated limit rather than an oversight. An
	// Ethereum wallet signs typed data and never arbitrary bytes, so accepting
	// one here means a new EIP-712 type - and a MetaMask buyer does not need this
	// call, because the browser client recovers in-flight while it still holds
	// the reservation's own authorization. A wallet on disk is the case this
	// exists for, and a wallet on disk is ed25519.
	PublicKey ed25519.PublicKey
	// Timestamp is unix nanoseconds at signing time.
	Timestamp int64
	// Signature is over SigningBytes.
	Signature []byte
}

// SigningBytes is the canonical, length-prefixed payload that is signed.
func (a *RecoverAuthorization) SigningBytes(jobID string) []byte {
	buf := make([]byte, 0, 128)
	buf = appendLenPrefixed(buf, []byte(recoverAuthDomain))
	buf = appendLenPrefixed(buf, a.PublicKey)
	buf = appendLenPrefixed(buf, []byte(jobID))
	buf = binary.BigEndian.AppendUint64(buf, uint64(a.Timestamp))
	return buf
}

// Sign signs the proof for jobID with priv, which must correspond to PublicKey.
func (a *RecoverAuthorization) Sign(jobID string, priv ed25519.PrivateKey) error {
	if len(a.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key must be %d bytes", ErrRunUnauthorized, ed25519.PublicKeySize)
	}
	a.Signature = ed25519.Sign(priv, a.SigningBytes(jobID))
	return nil
}

// verifyRecoverAuthorization checks that auth was signed now, by the key that
// will sign this job's settlement.
//
// That key is SpenderFor(payer): the buyer's own for an ordinary job, and a
// budget's delegate when the buyer is a budget - the same answer consensus uses
// to decide who may settle, so a caller this accepts is exactly a caller who
// could settle anyway.
func verifyRecoverAuthorization(jobID, payer string, auth *RecoverAuthorization, now time.Time) error {
	if auth == nil {
		return fmt.Errorf("%w: no recovery proof supplied", ErrRunUnauthorized)
	}
	if len(auth.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: a recovery proof is signed by an ed25519 key; got %d bytes",
			ErrRunUnauthorized, len(auth.PublicKey))
	}

	signed := time.Unix(0, auth.Timestamp)
	if delta := now.Sub(signed); delta > RecoverAuthorizationWindow || delta < -RecoverAuthorizationWindow {
		return fmt.Errorf("%w: the recovery proof was signed at %s, outside the %s window",
			ErrRunUnauthorized, signed.UTC().Format(time.RFC3339), RecoverAuthorizationWindow)
	}

	spender, _, err := token.SpenderFor(payer)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRunUnauthorized, err)
	}
	if got := token.AccountIDFromPublicKey(auth.PublicKey); got != spender {
		return fmt.Errorf("%w: signed by %s, but only %s can settle this job",
			ErrRunUnauthorized, got, spender)
	}
	if !ed25519.Verify(auth.PublicKey, auth.SigningBytes(jobID), auth.Signature) {
		return fmt.Errorf("%w: the recovery proof does not verify", ErrRunUnauthorized)
	}
	// NO REPLAY SET, deliberately. A replayed proof re-reads a settlement that
	// was already readable to its holder: nothing runs, no money moves, and the
	// answer is the same. The timestamp window is what stops one being useful
	// forever; a seen-set would only refuse a caller retrying after a dropped
	// connection.
	return nil
}
