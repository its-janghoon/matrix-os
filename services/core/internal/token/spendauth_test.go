package token

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"math"
	"testing"
	"time"
)

// The bounds in a SpendingAuthorization are the only thing protecting a buyer
// once per-draw approval is gone, so each one is tested for the case it exists
// to refuse - not for the happy path, which any implementation passes.

func authorization(t *testing.T, buyer ed25519.PublicKey, delegate ed25519.PublicKey) *SpendingAuthorization {
	t.Helper()
	return &SpendingAuthorization{
		Buyer:           MarshalPublicKey(buyer),
		Delegate:        MarshalPublicKey(delegate),
		Cap:             1_000_000,
		PerJobCap:       100_000,
		MaxPricePerUnit: 500,
		Expiry:          time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
		Nonce:           1,
	}
}

func keypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return pub, priv
}

func TestABuyersSignatureCoversEveryBound(t *testing.T) {
	buyerPub, buyerPriv := keypair(t)
	delegatePub, _ := keypair(t)

	signed := authorization(t, buyerPub, delegatePub)
	if err := signed.Sign(buyerPriv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := signed.Verify(); err != nil {
		t.Fatalf("a freshly signed authorization must verify: %v", err)
	}

	// Each mutation is a different way of stealing, and none of them may
	// survive the signature: raising the cap, widening a single draw, lifting
	// the price ceiling, extending the life, or swapping in another delegate.
	for name, mutate := range map[string]func(*SpendingAuthorization){
		"a bigger total":      func(a *SpendingAuthorization) { a.Cap *= 2 },
		"a bigger single job": func(a *SpendingAuthorization) { a.PerJobCap = a.Cap },
		"a dearer seller":     func(a *SpendingAuthorization) { a.MaxPricePerUnit *= 10 },
		"a longer life":       func(a *SpendingAuthorization) { a.Expiry += 86400 },
		"another delegate": func(a *SpendingAuthorization) {
			other, _ := keypair(t)
			a.Delegate = MarshalPublicKey(other)
		},
		"another buyer": func(a *SpendingAuthorization) {
			other, _ := keypair(t)
			a.Buyer = MarshalPublicKey(other)
		},
		"a later nonce": func(a *SpendingAuthorization) { a.Nonce++ },
	} {
		tampered := *signed
		mutate(&tampered)
		if err := tampered.Verify(); !errors.Is(err, ErrInvalidSignature) {
			t.Errorf("%s survived the signature: got %v, want ErrInvalidSignature", name, err)
		}
	}
}

// A blank cheque is the failure mode worth designing out, so a missing bound is
// refused rather than read as "unlimited". Somebody will eventually build one of
// these from a config file with a field spelled wrong.
func TestAMissingBoundIsRefusedRatherThanReadAsUnlimited(t *testing.T) {
	buyerPub, _ := keypair(t)
	delegatePub, _ := keypair(t)

	for name, break_ := range map[string]func(*SpendingAuthorization){
		"no total cap":     func(a *SpendingAuthorization) { a.Cap = 0 },
		"no per-job cap":   func(a *SpendingAuthorization) { a.PerJobCap = 0 },
		"no price ceiling": func(a *SpendingAuthorization) { a.MaxPricePerUnit = 0 },
		"no expiry":        func(a *SpendingAuthorization) { a.Expiry = 0 },
		"expiry in the negatives": func(a *SpendingAuthorization) {
			a.Expiry = -1
		},
		"a per-job cap that bounds nothing": func(a *SpendingAuthorization) {
			a.PerJobCap = a.Cap + 1
		},
		"a delegate that is not a key": func(a *SpendingAuthorization) {
			a.Delegate = []byte{1, 2, 3}
		},
	} {
		a := authorization(t, buyerPub, delegatePub)
		break_(a)
		if err := a.Validate(); !errors.Is(err, ErrInvalidAuthorization) {
			t.Errorf("%s was accepted: got %v, want ErrInvalidAuthorization", name, err)
		}
	}
}

func TestEachBoundRefusesTheDrawItExistsFor(t *testing.T) {
	buyerPub, _ := keypair(t)
	delegatePub, _ := keypair(t)
	a := authorization(t, buyerPub, delegatePub)
	before := time.Unix(a.Expiry-1, 0)

	if err := a.Allows(0, a.PerJobCap, a.MaxPricePerUnit, before); err != nil {
		t.Fatalf("a draw at exactly the bounds must be allowed: %v", err)
	}

	cases := []struct {
		name                        string
		spent, amount, pricePerUnit uint64
		now                         time.Time
		want                        error
	}{
		{"one unit over the per-job cap", 0, a.PerJobCap + 1, 100, before, ErrPerJobCapExceeded},
		{"one unit over the total", a.Cap - 10, 11, 100, before, ErrCapExceeded},
		{"a seller one unit too dear", 0, 1000, a.MaxPricePerUnit + 1, before, ErrPriceAboveCeiling},
		{"a draw of nothing", 0, 0, 100, before, ErrInvalidAuthorization},
		// Expiry is the moment it is dead, not the last moment it lives. A
		// boundary written the other way gives one free second, which is a
		// second in which four nodes can disagree.
		{"exactly at expiry", 0, 1000, 100, time.Unix(a.Expiry, 0), ErrAuthorizationExpired},
		{"after expiry", 0, 1000, 100, time.Unix(a.Expiry+1, 0), ErrAuthorizationExpired},
	}
	for _, tc := range cases {
		if err := a.Allows(tc.spent, tc.amount, tc.pricePerUnit, tc.now); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}

	if err := a.Allows(0, 1000, 100, time.Unix(a.Expiry-1, 0)); err != nil {
		t.Errorf("one second before expiry must still work: %v", err)
	}
}

// `spent + amount > cap` is the obvious spelling and it wraps, which turns the
// check that guards the money into one that waves it through. The guard is a
// subtraction for that reason, and this is the case that tells the difference.
func TestAnEnormousDrawCannotWrapPastTheCap(t *testing.T) {
	buyerPub, _ := keypair(t)
	delegatePub, _ := keypair(t)
	a := authorization(t, buyerPub, delegatePub)
	a.Cap = math.MaxUint64
	a.PerJobCap = math.MaxUint64

	err := a.Allows(math.MaxUint64-10, 100, 1, time.Unix(a.Expiry-1, 0))
	if !errors.Is(err, ErrCapExceeded) {
		t.Fatalf("a draw that overflows the sum was allowed: got %v, want ErrCapExceeded", err)
	}
}

func TestADrawMustBeSignedByTheDelegateTheBuyerNamed(t *testing.T) {
	buyerPub, buyerPriv := keypair(t)
	delegatePub, delegatePriv := keypair(t)
	_, impostorPriv := keypair(t)

	a := authorization(t, buyerPub, delegatePub)
	if err := a.Sign(buyerPriv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	draw := []byte("the bytes of some settlement")
	now := time.Unix(a.Expiry-1, 0)

	if err := a.VerifyDraw(draw, ed25519.Sign(delegatePriv, draw), 0, 1000, 100, now); err != nil {
		t.Fatalf("the named delegate must be able to draw: %v", err)
	}
	if err := a.VerifyDraw(draw, ed25519.Sign(impostorPriv, draw), 0, 1000, 100, now); !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("a stranger drew against someone else's authorization: %v", err)
	}
	if err := a.VerifyDraw(draw, nil, 0, 1000, 100, now); !errors.Is(err, ErrUnsignedTransaction) {
		t.Errorf("an unsigned draw was accepted: %v", err)
	}

	// The order matters: bounds are checked on a statement already known to be
	// genuine. An unsigned authorization with generous bounds proves nothing,
	// and must fail on the signature rather than pass on the bounds.
	unsigned := authorization(t, buyerPub, delegatePub)
	if err := unsigned.VerifyDraw(draw, ed25519.Sign(delegatePriv, draw), 0, 1, 1, now); !errors.Is(err, ErrUnsignedTransaction) {
		t.Errorf("an unsigned authorization was read for its bounds: %v", err)
	}
}

// Revocation is superseding, and it has to be signable - Validate refuses a
// zero cap, so a caller reaching for the obvious spelling would find it
// rejected and reach for something worse.
func TestRevokingIsSupersedingAndLeavesNothingWorthDrawing(t *testing.T) {
	buyerPub, buyerPriv := keypair(t)
	delegatePub, _ := keypair(t)

	live := authorization(t, buyerPub, delegatePub)
	dead := Revocation(live.Buyer, live.Delegate, live.Nonce+1, live.Expiry)

	if err := dead.Validate(); err != nil {
		t.Fatalf("a revocation must be a valid statement: %v", err)
	}
	if err := dead.Sign(buyerPriv); err != nil {
		t.Fatalf("a revocation must be signable: %v", err)
	}
	if !dead.Supersedes(live) {
		t.Fatal("a higher nonce must supersede")
	}
	if live.Supersedes(dead) {
		t.Fatal("a lower nonce must not supersede")
	}
	// Which bound refuses first is not the point and asserting one would make
	// this test a statement about check order. The point is that nothing a
	// seller would bother billing gets through.
	if err := dead.Allows(0, 1000, 100, time.Unix(dead.Expiry-1, 0)); err == nil {
		t.Error("a revocation still permitted a real draw")
	}
}

// Two authorizations for different delegates are independent grants, so neither
// replaces the other however the nonces fall. Reading a nonce as global would
// let a buyer who tops up one browser silently kill the budget in another.
func TestANonceOrdersOneDelegateAndNotTheAccount(t *testing.T) {
	buyerPub, _ := keypair(t)
	oneDelegate, _ := keypair(t)
	otherDelegate, _ := keypair(t)

	first := authorization(t, buyerPub, oneDelegate)
	second := authorization(t, buyerPub, otherDelegate)
	second.Nonce = first.Nonce + 5

	if second.Supersedes(first) {
		t.Fatal("an authorization replaced one belonging to a different delegate")
	}
}

func TestAnEthereumBuyerDelegatesWithTheirWallet(t *testing.T) {
	w := newEthWallet(t)
	delegatePub, delegatePriv := keypair(t)

	a := &SpendingAuthorization{
		Buyer:           w.addr[:],
		Delegate:        MarshalPublicKey(delegatePub),
		Cap:             1_000_000,
		PerJobCap:       100_000,
		MaxPricePerUnit: 500,
		Expiry:          time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
		Nonce:           1,
	}
	digest, err := a.EthDigest()
	if err != nil {
		t.Fatalf("EthDigest: %v", err)
	}
	a.Signature = w.sign(t, digest)

	if err := a.Verify(); err != nil {
		t.Fatalf("a wallet-signed authorization must verify: %v", err)
	}
	id, err := a.AccountID()
	if err != nil {
		t.Fatalf("AccountID: %v", err)
	}
	if want := EthAccountID(w.addr); id != want {
		t.Fatalf("spends from %q, want %q", id, want)
	}

	// The delegate here is an ed25519 key held in a browser, and the wallet
	// that authorised it has no ed25519 key at all. That is the whole point:
	// one wallet prompt, then a key that cannot leave the page does the rest.
	draw := []byte("a settlement the page signs")
	if err := a.VerifyDraw(draw, ed25519.Sign(delegatePriv, draw), 0, 1000, 100, time.Unix(a.Expiry-1, 0)); err != nil {
		t.Fatalf("the browser key must be able to draw: %v", err)
	}

	// A raised cap must not verify against the wallet's signature either.
	tampered := *a
	tampered.Cap *= 1000
	if err := tampered.Verify(); !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("a raised cap survived the wallet signature: %v", err)
	}
}

// An Ethereum buyer has no ed25519 key, so offering them a Sign method that
// takes one would be offering a way to make a signature no wallet would ever
// produce - and the place it would be called from is a server.
func TestAnEthereumBuyerCannotBeSignedForInProcess(t *testing.T) {
	w := newEthWallet(t)
	delegatePub, _ := keypair(t)
	_, someKey := keypair(t)

	a := &SpendingAuthorization{
		Buyer:           w.addr[:],
		Delegate:        MarshalPublicKey(delegatePub),
		Cap:             10,
		PerJobCap:       10,
		MaxPricePerUnit: 10,
		Expiry:          time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
		Nonce:           1,
	}
	if err := a.Sign(someKey); !errors.Is(err, ErrInvalidAuthorization) {
		t.Fatalf("an ethereum buyer was signed for locally: %v", err)
	}
}

// The digest is what a wallet independently recomputes, so its inputs are fixed
// forever. A short delegate must be refused rather than padded, because
// Ethereum tooling right-pads a short bytes32 while every integer here is
// left-padded - the two would disagree under a message that reads "invalid
// signature" and sends you looking at the key.
func TestAShortDelegateIsRefusedRatherThanPadded(t *testing.T) {
	w := newEthWallet(t)
	if _, err := EthSpendingAuthorizationDigest(w.addr, []byte{1, 2, 3}, 1, 1, 1, 1, 1); err == nil {
		t.Fatal("a 3-byte delegate was padded into a bytes32")
	}
}
