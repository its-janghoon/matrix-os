package token

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/ecirlabs/matrix-core/internal/ethsig"
)

// A signature in this protocol authorises exactly one payment of an exact
// amount. That is the right default and it has a cost nobody chose: a buyer who
// holds their own key approves every message, twice - once to authorise the run
// before a seller spends GPU time, once to pay for it after, because the amount
// does not exist until the tokens are counted.
//
// The alternative the protocol offered was custody: a node holds the buyer's
// private key and signs for them. That buys a quiet UI with the worst risk
// there is, which is why every comparable system - session keys under ERC-4337
// and then EIP-7702, x402's signed payment authorizations - exists to avoid it.
//
// A SpendingAuthorization is the third answer. The wallet signs ONCE and names
// a subordinate key that may spend up to a cap, for a bounded time, on
// inference and nothing else. The delegate is an ed25519 public key rather than
// an address on purpose: the non-extractable key a browser can generate becomes
// a bounded hand on the account the buyer already has, instead of a second
// account that needs its own funding and dies with the site's storage.
//
// WHAT THIS TYPE IS AND IS NOT. It is the signed statement and the rules that
// read it: whether a signature is genuine, and whether a proposed draw is
// inside the bounds. It holds no state, so it cannot know what has already been
// spent - the caller passes that in, because the running total belongs to
// consensus and not to a struct anyone can construct.
type SpendingAuthorization struct {
	// Buyer is the account being spent FROM: an ed25519 public key (32 bytes)
	// or an Ethereum address (20 bytes), exactly as Transaction.From.
	Buyer []byte
	// Delegate is the ed25519 public key allowed to sign draws against this
	// authorization. It is a key and not an account id because the delegate
	// never holds anything - it spends the buyer's balance under these bounds.
	Delegate []byte
	// Cap is the total, in native base units, that may be drawn in aggregate.
	Cap uint64
	// PerJobCap is the most any single draw may take. It bounds the damage one
	// runaway job or one hostile seller can do inside the total.
	PerJobCap uint64
	// MaxPricePerUnit refuses a seller dearer than this. See the type-string
	// comment in eth.go: without it the scope is not a bound.
	MaxPricePerUnit uint64
	// Expiry is the unix second at and after which this authorization is dead.
	// Absolute, never a sliding window: one nobody revokes still dies.
	Expiry int64
	// Nonce orders authorizations for the same (buyer, delegate). A higher one
	// supersedes a lower one, so re-signing with Cap zero is revocation - which
	// is a thing the buyer does with their own wallet and needs nothing from
	// the seller or from us.
	Nonce uint64
	// Signature is the buyer's, over SigningBytes for an ed25519 buyer or over
	// EthDigest for an Ethereum one.
	Signature []byte
}

var (
	// ErrInvalidAuthorization is returned when the statement itself is
	// malformed, before any signature or bound is considered.
	ErrInvalidAuthorization = errors.New("token: invalid spending authorization")
	// ErrAuthorizationExpired is returned when the block's time is at or past
	// Expiry.
	ErrAuthorizationExpired = errors.New("token: spending authorization has expired")
	// ErrCapExceeded is returned when a draw would take the running total past
	// Cap. It is a refusal and never a clamp: a partly-paid job is worse than a
	// refused one, because the buyer has paid for something they did not get.
	ErrCapExceeded = errors.New("token: draw exceeds the authorized cap")
	// ErrPerJobCapExceeded is returned when one draw is larger than PerJobCap.
	ErrPerJobCapExceeded = errors.New("token: draw exceeds the per-job cap")
	// ErrPriceAboveCeiling is returned when the seller's unit price is above
	// MaxPricePerUnit.
	ErrPriceAboveCeiling = errors.New("token: seller price is above the authorized ceiling")
)

// BuyerIsEth reports whether the buyer is an Ethereum-controlled account, by
// the same length test Transaction.SenderIsEth uses.
func (a *SpendingAuthorization) BuyerIsEth() bool {
	return len(a.Buyer) == ethsig.AddressLen
}

// AccountID is the ledger account this authorization spends from.
func (a *SpendingAuthorization) AccountID() (string, error) {
	if a.BuyerIsEth() {
		addr, err := ethsig.AddressFromBytes(a.Buyer)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrInvalidAuthorization, err)
		}
		return EthAccountID(addr), nil
	}
	if len(a.Buyer) != ed25519.PublicKeySize {
		return "", fmt.Errorf("%w: buyer is %d bytes, want %d (ed25519) or %d (ethereum)",
			ErrInvalidAuthorization, len(a.Buyer), ed25519.PublicKeySize, ethsig.AddressLen)
	}
	return AccountIDFromPublicKey(a.Buyer), nil
}

// Validate checks the statement is well formed, independently of its signature
// and of any draw. Every bound must be set: an authorization with no ceiling is
// not a weaker authorization, it is a blank cheque written by accident, so a
// zero is refused rather than read as "unlimited".
func (a *SpendingAuthorization) Validate() error {
	if _, err := a.AccountID(); err != nil {
		return err
	}
	if len(a.Delegate) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: delegate key is %d bytes, want %d",
			ErrInvalidAuthorization, len(a.Delegate), ed25519.PublicKeySize)
	}
	if a.Cap == 0 {
		return fmt.Errorf("%w: cap must be positive; a zero cap is revocation and "+
			"is expressed by superseding an authorization, not by signing a new one", ErrInvalidAuthorization)
	}
	if a.PerJobCap == 0 {
		return fmt.Errorf("%w: per-job cap must be positive", ErrInvalidAuthorization)
	}
	if a.PerJobCap > a.Cap {
		return fmt.Errorf("%w: per-job cap %d is larger than the total cap %d, which "+
			"means it bounds nothing", ErrInvalidAuthorization, a.PerJobCap, a.Cap)
	}
	if a.MaxPricePerUnit == 0 {
		return fmt.Errorf("%w: max price per unit must be positive; without a price "+
			"ceiling the scope is not a bound", ErrInvalidAuthorization)
	}
	if a.Expiry <= 0 {
		return fmt.Errorf("%w: expiry must be a positive unix second", ErrInvalidAuthorization)
	}
	return nil
}

// Revocation is an authorization that supersedes an earlier one and allows
// nothing. It is written as a constructor rather than left to callers because
// Validate refuses a zero Cap: revoking is superseding, and the shape of the
// superseding statement should have exactly one spelling.
//
// The nonce must be higher than the authorization being revoked; the caller
// knows which one that is and this cannot.
func Revocation(buyer, delegate []byte, nonce uint64, expiry int64) *SpendingAuthorization {
	return &SpendingAuthorization{
		Buyer:    buyer,
		Delegate: delegate,
		// The smallest legal bounds rather than zeroes, so the statement is a
		// valid authorization that happens to permit nothing useful. A draw of
		// one base unit at a price of one is not a hole worth a special case in
		// the verifier.
		Cap:             1,
		PerJobCap:       1,
		MaxPricePerUnit: 1,
		Expiry:          expiry,
		Nonce:           nonce,
	}
}

// Supersedes reports whether this authorization replaces other. Same buyer,
// same delegate, higher nonce.
func (a *SpendingAuthorization) Supersedes(other *SpendingAuthorization) bool {
	if other == nil {
		return true
	}
	if !equalBytes(a.Buyer, other.Buyer) || !equalBytes(a.Delegate, other.Delegate) {
		return false
	}
	return a.Nonce > other.Nonce
}

// Allows reports whether a draw of amount, from a seller charging
// pricePerUnit, is permitted given that spent has already been drawn.
//
// NOW COMES FROM THE BLOCK. Every node must reach the same verdict from the
// same block, so the caller passes the block's time and never time.Now(): a
// wall clock read independently on four machines is four different answers, and
// an authorization that expires on one node and not another is a fork.
func (a *SpendingAuthorization) Allows(spent, amount, pricePerUnit uint64, now time.Time) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if amount == 0 {
		return fmt.Errorf("%w: a draw of nothing", ErrInvalidAuthorization)
	}
	if !now.Before(time.Unix(a.Expiry, 0)) {
		return fmt.Errorf("%w: expired at %s, block time is %s",
			ErrAuthorizationExpired, time.Unix(a.Expiry, 0).UTC().Format(time.RFC3339),
			now.UTC().Format(time.RFC3339))
	}
	if pricePerUnit > a.MaxPricePerUnit {
		return fmt.Errorf("%w: seller charges %d per unit, ceiling is %d",
			ErrPriceAboveCeiling, pricePerUnit, a.MaxPricePerUnit)
	}
	if amount > a.PerJobCap {
		return fmt.Errorf("%w: draw of %d, per-job cap is %d",
			ErrPerJobCapExceeded, amount, a.PerJobCap)
	}
	// Subtraction rather than `spent + amount > Cap`, which overflows for a
	// large spent and silently permits the draw it was meant to refuse.
	if spent > a.Cap || amount > a.Cap-spent {
		return fmt.Errorf("%w: %d already drawn of %d, this draw is %d",
			ErrCapExceeded, spent, a.Cap, amount)
	}
	return nil
}

// SigningBytes is the canonical serialization an ed25519 buyer signs. The
// layout is length-prefixed for the same reason Transaction.SigningBytes is: no
// two distinct field combinations may produce the same bytes.
//
//	uint32(len(Buyer)) | Buyer | uint32(len(Delegate)) | Delegate |
//	uint64(Cap) | uint64(PerJobCap) | uint64(MaxPricePerUnit) |
//	int64(Expiry) | uint64(Nonce)
//
// All integers are big-endian. This is a consensus-relevant invariant: change
// it and every authorization ever signed stops verifying.
func (a *SpendingAuthorization) SigningBytes() []byte {
	buf := make([]byte, 0, 4+len(a.Buyer)+4+len(a.Delegate)+8*5)
	buf = appendLenPrefixed(buf, a.Buyer)
	buf = appendLenPrefixed(buf, a.Delegate)

	var scratch [8]byte
	for _, v := range []uint64{a.Cap, a.PerJobCap, a.MaxPricePerUnit, uint64(a.Expiry), a.Nonce} {
		binary.BigEndian.PutUint64(scratch[:], v)
		buf = append(buf, scratch[:]...)
	}
	return buf
}

// Sign signs with an ed25519 buyer key. An Ethereum buyer signs EthDigest in
// their wallet instead, so there is no counterpart here - this process never
// holds an Ethereum key and a helper that implied otherwise would be an
// invitation to put one somewhere.
func (a *SpendingAuthorization) Sign(priv ed25519.PrivateKey) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if a.BuyerIsEth() {
		return fmt.Errorf("%w: an ethereum buyer signs the EIP-712 digest in their wallet", ErrInvalidAuthorization)
	}
	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: private key must be %d bytes", ErrInvalidAuthorization, ed25519.PrivateKeySize)
	}
	if !priv.Public().(ed25519.PublicKey).Equal(ed25519.PublicKey(a.Buyer)) {
		return fmt.Errorf("%w: private key does not match the buyer", ErrInvalidAuthorization)
	}
	a.Signature = ed25519.Sign(priv, a.SigningBytes())
	return nil
}

// Verify checks the buyer really signed this statement, dispatching on which
// kind of account the buyer is exactly as Transaction.Verify does.
func (a *SpendingAuthorization) Verify() error {
	if err := a.Validate(); err != nil {
		return err
	}
	if len(a.Signature) == 0 {
		return ErrUnsignedTransaction
	}
	if a.BuyerIsEth() {
		digest, err := a.EthDigest()
		if err != nil {
			return err
		}
		recovered, err := ethsig.RecoverAddress(digest, a.Signature)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
		}
		declared, err := ethsig.AddressFromBytes(a.Buyer)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidAuthorization, err)
		}
		if recovered != declared {
			return fmt.Errorf("%w: signed by %s, but the buyer is %s",
				ErrInvalidSignature, recovered.Hex(), declared.Hex())
		}
		return nil
	}
	if !ed25519.Verify(a.Buyer, a.SigningBytes(), a.Signature) {
		return ErrInvalidSignature
	}
	return nil
}

// VerifyDraw checks a delegate's signature over drawBytes, having first
// established that the authorization itself is genuine and that the draw is
// within its bounds. It is one call rather than three because doing them in the
// wrong order is the mistake worth designing out: checking the bounds of a
// statement nobody signed proves nothing.
func (a *SpendingAuthorization) VerifyDraw(drawBytes, drawSignature []byte, spent, amount, pricePerUnit uint64, now time.Time) error {
	if err := a.Verify(); err != nil {
		return err
	}
	if err := a.Allows(spent, amount, pricePerUnit, now); err != nil {
		return err
	}
	if len(drawSignature) == 0 {
		return ErrUnsignedTransaction
	}
	if !ed25519.Verify(a.Delegate, drawBytes, drawSignature) {
		return fmt.Errorf("%w: the draw is not signed by the delegate this authorization names",
			ErrInvalidSignature)
	}
	return nil
}

// EthDigest returns the EIP-712 digest a wallet signs to issue this
// authorization.
func (a *SpendingAuthorization) EthDigest() ([]byte, error) {
	if !a.BuyerIsEth() {
		return nil, fmt.Errorf("%w: buyer is not an ethereum account", ErrInvalidAuthorization)
	}
	buyer, err := ethsig.AddressFromBytes(a.Buyer)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAuthorization, err)
	}
	return EthSpendingAuthorizationDigest(buyer, a.Delegate, a.Cap, a.PerJobCap, a.MaxPricePerUnit, a.Expiry, a.Nonce)
}

// equalBytes is a length-then-content comparison. It is not constant time and
// does not need to be: both sides are public.
func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
