package inference

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"

	"github.com/ecirlabs/matrix-core/internal/ethsig"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// metamask stands in for a browser wallet: a secp256k1 key that signs 32-byte
// EIP-712 digests, which is the only thing MetaMask will sign.
type metamask struct {
	priv *secp256k1.PrivateKey
	addr ethsig.Address
}

func newMetamask(t *testing.T) metamask {
	t.Helper()
	priv, err := secp256k1.GeneratePrivateKeyFromRand(rand.Reader)
	if err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	return metamask{priv: priv, addr: ethsig.AddressFromPubKey(priv.PubKey())}
}

func (m metamask) accountID() string { return token.EthAccountID(m.addr) }

func (m metamask) sign(t *testing.T, digest []byte) []byte {
	t.Helper()
	sig, err := ethsig.SignDigest(m.priv, digest)
	if err != nil {
		t.Fatalf("SignDigest: %v", err)
	}
	return sig
}

// ethService builds a service whose buyer is an Ethereum-controlled account and
// whose Accounts resolver knows NOBODY - so if a test passes, the node settled
// without holding any key at all, ed25519 or otherwise.
func ethService(t *testing.T, settler Settler, pricePerUnit uint64, buyer string, credits uint64) (*Service, string) {
	t.Helper()
	svc, _, providerID := newTestService(t, settler, pricePerUnit, 0)
	svc.accounts = memAccounts{m: map[string]*token.Account{}}
	if err := svc.market.Ledger().Credit(buyer, credits); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	return svc, providerID
}

// TestMetaMaskCanRunAndPayForInference is the whole point of the eth account
// kind: one wallet, no ed25519 key anywhere, and the node holding nothing.
func TestMetaMaskCanRunAndPayForInference(t *testing.T) {
	wallet := newMetamask(t)
	fs := &fakeSettler{committed: true, applied: true}
	svc, provider := ethService(t, fs, 3, wallet.accountID(), 1_000_000)

	req := InferenceRequest{Prompt: "hello from metamask", Model: "m"}

	// 1. The run authorization, signed over the EIP-712 digest.
	digest := requestDigest(req)
	runDigest, err := token.EthRunAuthorizationDigest(wallet.addr, provider, "m", digest[:], time.Now().UnixNano())
	if err != nil {
		t.Fatalf("EthRunAuthorizationDigest: %v", err)
	}
	auth := &RunAuthorization{
		PublicKey: wallet.addr[:],
		Provider:  provider,
		Model:     "m",
		Timestamp: time.Now().UnixNano(),
		Signature: wallet.sign(t, runDigest),
	}
	// The timestamp has to be the one that was signed, or the digest differs.
	auth.Timestamp = mustSignedTimestamp(t, wallet, provider, "m", digest[:], auth)

	if err := svc.VerifyRunAuthorization(wallet.accountID(), req, auth); err != nil {
		t.Fatalf("VerifyRunAuthorization: %v", err)
	}

	// 2. Run, which returns the invoice and withholds the completion.
	pr, err := svc.RunUnsettled(context.Background(), wallet.accountID(), provider, req, 1000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}
	if pr.From != wallet.accountID() {
		t.Fatalf("invoice is from %s, want %s", pr.From, wallet.accountID())
	}

	// 3. The payment, signed over its own EIP-712 digest.
	tx := pr.Transaction(wallet.addr[:])
	payDigest, err := tx.EthTransferDigest()
	if err != nil {
		t.Fatalf("EthTransferDigest: %v", err)
	}
	tx.Signature = wallet.sign(t, payDigest)

	settled, err := svc.SettleSigned(context.Background(), pr.JobID, tx)
	if err != nil {
		t.Fatalf("SettleSigned: %v", err)
	}
	if settled.Status != InferenceJobCompleted {
		t.Fatalf("status = %s, want completed", settled.Status)
	}
	if settled.Completion == "" {
		t.Fatal("a paid job must hand over its completion")
	}
	if fs.signed() == nil || fs.signed().SenderID() != wallet.accountID() {
		t.Fatalf("the submitted transfer is from %v, want the eth account", fs.signed())
	}
}

// mustSignedTimestamp re-signs at a single fixed timestamp, so the value in the
// authorization is the one the signature covers.
func mustSignedTimestamp(t *testing.T, w metamask, provider, model string, promptDigest []byte, auth *RunAuthorization) int64 {
	t.Helper()
	ts := time.Now().UnixNano()
	d, err := token.EthRunAuthorizationDigest(w.addr, provider, model, promptDigest, ts)
	if err != nil {
		t.Fatalf("EthRunAuthorizationDigest: %v", err)
	}
	auth.Signature = w.sign(t, d)
	return ts
}

// TestAnEthAuthorizationCannotNameAnotherAccount: the account is derived from
// the address, so a wallet can only authorise its own.
func TestAnEthAuthorizationCannotNameAnotherAccount(t *testing.T) {
	attacker := newMetamask(t)
	victim := newMetamask(t)
	fs := &fakeSettler{committed: true, applied: true}
	svc, provider := ethService(t, fs, 3, victim.accountID(), 1_000_000)

	req := InferenceRequest{Prompt: "free work", Model: "m"}
	digest := requestDigest(req)
	auth := &RunAuthorization{PublicKey: attacker.addr[:], Provider: provider, Model: "m"}
	auth.Timestamp = mustSignedTimestamp(t, attacker, provider, "m", digest[:], auth)

	if err := svc.VerifyRunAuthorization(victim.accountID(), req, auth); !errors.Is(err, ErrRunUnauthorized) {
		t.Fatalf("err = %v, want ErrRunUnauthorized", err)
	}
}

// TestAnEthPaymentForAnotherAmountIsRefused: the eth path gets the same
// field-by-field invoice check as the ed25519 one, or a wallet could sign a
// perfectly valid transfer of one base unit.
func TestAnEthPaymentForAnotherAmountIsRefused(t *testing.T) {
	wallet := newMetamask(t)
	fs := &fakeSettler{committed: true, applied: true}
	svc, provider := ethService(t, fs, 3, wallet.accountID(), 1_000_000)

	pr, err := svc.RunUnsettled(context.Background(), wallet.accountID(), provider,
		InferenceRequest{Prompt: "hello", Model: "m"}, 1000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}

	cheap := *pr
	cheap.Amount = 1
	tx := cheap.Transaction(wallet.addr[:])
	d, err := tx.EthTransferDigest()
	if err != nil {
		t.Fatalf("EthTransferDigest: %v", err)
	}
	tx.Signature = wallet.sign(t, d)

	if _, err := svc.SettleSigned(context.Background(), pr.JobID, tx); !errors.Is(err, ErrPaymentMismatch) {
		t.Fatalf("err = %v, want ErrPaymentMismatch", err)
	}
	if fs.signed() != nil {
		t.Fatal("an underpaying eth transfer must not reach consensus")
	}
}

// TestAnEthAccountIsAnOrdinaryAccountOnTheLedger: it has to be fundable,
// spendable and readable like any other, or the kind is a curiosity rather than
// a wallet.
func TestAnEthAccountIsAnOrdinaryAccountOnTheLedger(t *testing.T) {
	wallet := newMetamask(t)
	fs := &fakeSettler{committed: true, applied: true}
	svc, provider := ethService(t, fs, 3, wallet.accountID(), 500)

	balance, err := svc.market.Ledger().Balance(wallet.accountID())
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if balance != 500 {
		t.Fatalf("balance = %d, want 500", balance)
	}

	// And the affordability check applies: 1000 units at 3 is 3000, over its 500.
	if _, err := svc.RunUnsettled(context.Background(), wallet.accountID(), provider,
		InferenceRequest{Prompt: "hello", Model: "m"}, 1000); !errors.Is(err, market.ErrInsufficientFunds) {
		t.Fatalf("err = %v, want ErrInsufficientFunds", err)
	}
}

// TestAnEd25519AuthorizationStillWorks: adding the eth kind must not disturb the
// path that already existed.
func TestAnEd25519AuthorizationStillWorks(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)
	req := InferenceRequest{Prompt: "hello", Model: "m"}

	auth := authFor(t, buyer, provider, "m", req, time.Now())
	if err := svc.VerifyRunAuthorization(buyer.AccountID(), req, auth); err != nil {
		t.Fatalf("an ed25519 authorization should still verify: %v", err)
	}
}

// TestAnEthKeyCannotSignTheEd25519Payload, and vice versa. The two schemes must
// not be interchangeable, or the weaker check would decide.
func TestTheTwoSchemesAreNotInterchangeable(t *testing.T) {
	wallet := newMetamask(t)
	fs := &fakeSettler{committed: true, applied: true}
	svc, provider := ethService(t, fs, 3, wallet.accountID(), 1_000_000)
	req := InferenceRequest{Prompt: "hello", Model: "m"}

	// An eth address presented with an ed25519-length signature.
	auth := &RunAuthorization{
		PublicKey: wallet.addr[:],
		Provider:  provider,
		Model:     "m",
		Timestamp: time.Now().UnixNano(),
		Signature: make([]byte, 64),
	}
	if err := svc.VerifyRunAuthorization(wallet.accountID(), req, auth); !errors.Is(err, ErrRunUnauthorized) {
		t.Fatalf("err = %v, want the wrong-length signature refused", err)
	}

	// And a key that is neither length.
	odd := &RunAuthorization{
		PublicKey: make([]byte, 24),
		Provider:  provider,
		Model:     "m",
		Timestamp: time.Now().UnixNano(),
		Signature: make([]byte, 65),
	}
	if err := svc.VerifyRunAuthorization(wallet.accountID(), req, odd); !errors.Is(err, ErrRunUnauthorized) {
		t.Fatalf("err = %v, want an unrecognised key length refused", err)
	}
}
