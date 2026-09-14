package token

import (
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/ecirlabs/matrix-core/internal/ethsig"
	"github.com/ecirlabs/matrix-core/internal/evmtx"
)

const testChainID uint64 = 61_337

// walletSigned builds and signs an envelope the way a wallet would, returning
// the raw bytes and the address that signed them.
func walletSigned(t *testing.T, mutate func(*evmtx.Transaction)) ([]byte, ethsig.Address) {
	t.Helper()
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	to, err := ethsig.ParseAddress("0x00000000000000000000000000000000000000aa")
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	tx := &evmtx.Transaction{
		Type:      evmtx.TxLegacy,
		Nonce:     3,
		GasFeeCap: big.NewInt(1_000_000_000),
		Gas:       21000,
		To:        &to,
		// One whole MATRIX, in the 18-decimal scale a wallet shows.
		Value: NativeToERC20(NativeUnit),
	}
	if mutate != nil {
		mutate(tx)
	}
	if err := tx.Sign(priv, testChainID); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	return raw, ethsig.AddressFromPubKey(priv.PubKey())
}

// TestEnvelopeBecomesAChainTransaction is the path a MetaMask transfer takes:
// raw bytes in, a transaction the ledger understands out, with every field read
// out of the signed envelope.
func TestEnvelopeBecomesAChainTransaction(t *testing.T) {
	raw, signer := walletSigned(t, nil)

	tx, err := NewTransactionFromEVM(raw, testChainID, nil)
	if err != nil {
		t.Fatalf("NewTransactionFromEVM: %v", err)
	}
	if got, want := tx.SenderID(), EthAccountID(signer); got != want {
		t.Fatalf("sender = %s, want %s", got, want)
	}
	if tx.To != "eth:0x00000000000000000000000000000000000000aa" {
		t.Fatalf("recipient = %s", tx.To)
	}
	// 1e18 in the wallet's scale is 1e9 native base units: one whole MATRIX
	// either way, which is the only reading that keeps the two scales one story.
	if tx.Amount != NativeUnit {
		t.Fatalf("amount = %d, want %d", tx.Amount, NativeUnit)
	}
	if tx.Nonce != 3 {
		t.Fatalf("nonce = %d, want 3", tx.Nonce)
	}
	if tx.ChainID != testChainID {
		t.Fatalf("chain id = %d, want %d", tx.ChainID, testChainID)
	}
	if !tx.IsEVM() {
		t.Fatal("the transaction should report itself as ethereum-enveloped")
	}
	if err := tx.VerifyForChain(testChainID, nil); err != nil {
		t.Fatalf("VerifyForChain: %v", err)
	}

	hash, err := tx.EVMHash()
	if err != nil {
		t.Fatalf("EVMHash: %v", err)
	}
	if len(hash) != 32 {
		t.Fatalf("transaction id is %d bytes, want 32", len(hash))
	}
}

// TestTamperedFieldsAreRejected is why the envelope is the authority. A peer
// that edits the struct while keeping the signed bytes must not be believed,
// because only the bytes were ever signed.
func TestTamperedFieldsAreRejected(t *testing.T) {
	raw, _ := walletSigned(t, nil)
	base, err := NewTransactionFromEVM(raw, testChainID, nil)
	if err != nil {
		t.Fatalf("NewTransactionFromEVM: %v", err)
	}

	for _, tc := range []struct {
		name   string
		tamper func(*Transaction)
	}{
		{"the amount", func(tx *Transaction) { tx.Amount *= 1000 }},
		{"the recipient", func(tx *Transaction) { tx.To = "eth:0x00000000000000000000000000000000000000bb" }},
		{"the nonce", func(tx *Transaction) { tx.Nonce = 99 }},
		{"the sender", func(tx *Transaction) {
			other, _ := ethsig.ParseAddress("0x00000000000000000000000000000000000000cc")
			tx.From = other[:]
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tampered := *base
			tc.tamper(&tampered)
			if err := tampered.VerifyForChain(testChainID, nil); err == nil {
				t.Fatalf("editing %s should have failed verification", tc.name)
			}
		})
	}
}

// TestAnotherChainsTransactionIsRejected is the property that did not exist
// before: a transaction signed for a different network must not apply here.
func TestAnotherChainsTransactionIsRejected(t *testing.T) {
	raw, _ := walletSigned(t, nil)
	if _, err := NewTransactionFromEVM(raw, testChainID+1, nil); !errors.Is(err, evmtx.ErrChainIDMismatch) {
		t.Fatalf("building for the wrong chain = %v, want ErrChainIDMismatch", err)
	}
	tx, err := NewTransactionFromEVM(raw, testChainID, nil)
	if err != nil {
		t.Fatalf("NewTransactionFromEVM: %v", err)
	}
	if err := tx.VerifyForChain(testChainID+1, nil); err == nil {
		t.Fatal("verifying against the wrong chain should have failed")
	}
	// A node that configured no chain id accepts nothing on this path, rather
	// than treating zero as a wildcard.
	if err := tx.VerifyForChain(0, nil); err == nil {
		t.Fatal("a node with no chain id must not accept an enveloped transaction")
	}
}

// TestValueMustBeExactlyRepresentable covers the scale boundary. The wallet
// works in 18 decimals and the ledger in 9, so a value with more precision than
// the ledger can hold has to be refused rather than rounded: rounding down
// destroys the remainder and rounding up creates money.
func TestValueMustBeExactlyRepresentable(t *testing.T) {
	// One wei below a whole native base unit.
	raw, _ := walletSigned(t, func(tx *evmtx.Transaction) {
		tx.Value = new(big.Int).Sub(NativeToERC20(NativeUnit), big.NewInt(1))
	})
	if _, err := NewTransactionFromEVM(raw, testChainID, nil); err == nil {
		t.Fatal("a value that is not an exact multiple of the native unit must be refused")
	}

	// The smallest representable amount: one native base unit.
	raw, _ = walletSigned(t, func(tx *evmtx.Transaction) {
		tx.Value = NativeToERC20(1)
	})
	tx, err := NewTransactionFromEVM(raw, testChainID, nil)
	if err != nil {
		t.Fatalf("one native base unit should be representable: %v", err)
	}
	if tx.Amount != 1 {
		t.Fatalf("amount = %d, want 1", tx.Amount)
	}

	// Zero is representable and is an ordinary transfer of nothing.
	raw, _ = walletSigned(t, func(tx *evmtx.Transaction) { tx.Value = big.NewInt(0) })
	if _, err := NewTransactionFromEVM(raw, testChainID, nil); err != nil {
		t.Fatalf("a zero-value transfer should decode: %v", err)
	}
}

// TestWhatThisChainCannotDoIsRefusedNotIgnored covers the two envelopes that ask
// for an EVM. Silently moving the value while dropping the request would be the
// worst available answer, because the wallet would report success.
func TestWhatThisChainCannotDoIsRefusedNotIgnored(t *testing.T) {
	raw, _ := walletSigned(t, func(tx *evmtx.Transaction) { tx.To = nil })
	if _, err := NewTransactionFromEVM(raw, testChainID, nil); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("contract creation = %v, want ErrInvalidTransaction", err)
	}

	raw, _ = walletSigned(t, func(tx *evmtx.Transaction) { tx.Data = []byte{0xa9, 0x05, 0x9c, 0xbb} })
	if _, err := NewTransactionFromEVM(raw, testChainID, nil); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("calldata = %v, want ErrInvalidTransaction", err)
	}
}

// TestDynamicFeeEnvelopeWorksToo covers the type a modern wallet prefers. A
// chain that accepted only legacy transactions would reject most of what
// arrives.
func TestDynamicFeeEnvelopeWorksToo(t *testing.T) {
	raw, signer := walletSigned(t, func(tx *evmtx.Transaction) {
		tx.Type = evmtx.TxDynamicFee
		tx.GasTipCap = big.NewInt(1_000_000)
	})
	tx, err := NewTransactionFromEVM(raw, testChainID, nil)
	if err != nil {
		t.Fatalf("NewTransactionFromEVM: %v", err)
	}
	if tx.SenderID() != EthAccountID(signer) {
		t.Fatalf("sender = %s, want %s", tx.SenderID(), EthAccountID(signer))
	}
	if err := tx.VerifyForChain(testChainID, nil); err != nil {
		t.Fatalf("VerifyForChain: %v", err)
	}
}

// TestNonEVMTransactionsAreUnaffected pins that adding this scheme changed
// nothing for the two that existed. An ed25519 transaction still verifies with
// no chain id, which is the gap this scheme closes only for transactions that
// use it.
func TestNonEVMTransactionsAreUnaffected(t *testing.T) {
	acct, err := GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	tx := &Transaction{From: acct.PublicKey, To: "somebody", Amount: 5, Nonce: 1}
	if err := tx.Sign(acct.PrivateKey); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if tx.IsEVM() {
		t.Fatal("an ed25519 transaction must not report itself as enveloped")
	}
	if err := tx.VerifyForChain(testChainID, nil); err != nil {
		t.Fatalf("VerifyForChain on an ed25519 transaction: %v", err)
	}
	if err := tx.VerifyForChain(0, nil); err != nil {
		t.Fatalf("an ed25519 transaction carries no chain id and must still verify: %v", err)
	}
}

// TestAReservedAddressNeedsTheResolverToVerify pins a fail-closed edge, so
// nobody "fixes" it by handing the plain path a resolver.
//
// Verify has no config and therefore no resolver, so an envelope naming a
// reserved address derives an ordinary account and disagrees with the operation
// string beside it. That refusal is correct: the paths that call Verify - the
// pairwise settled ledger, an inference payment, a gossiped settlement - are
// paying accounts, and a consensus operation arriving there is not something to
// wave through. VerifyForChain, which every consensus path uses, is where a
// resolver belongs.
func TestAReservedAddressNeedsTheResolverToVerify(t *testing.T) {
	reservedBond, err := ethsig.ParseAddress("0x0000000000000000000000000000000000000001")
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	raw, signer := walletSigned(t, func(tx *evmtx.Transaction) {
		tx.To = &reservedBond
		tx.Value = NativeToERC20(NativeUnit)
	})

	// A resolver that spells the operation out, standing in for the consensus one.
	resolver := func(to ethsig.Address, sender string) (string, bool) {
		if to == reservedBond {
			return "consensus/stake/bond/" + strings.TrimPrefix(sender, EthAccountPrefix), true
		}
		return "", false
	}

	tx, err := NewTransactionFromEVM(raw, testChainID, resolver)
	if err != nil {
		t.Fatalf("NewTransactionFromEVM: %v", err)
	}
	if !strings.HasPrefix(tx.To, "consensus/stake/bond/") {
		t.Fatalf("recipient = %q, want the resolved operation", tx.To)
	}
	if tx.SenderID() != EthAccountID(signer) {
		t.Fatalf("sender = %q", tx.SenderID())
	}
	if err := tx.VerifyForChain(testChainID, resolver); err != nil {
		t.Fatalf("VerifyForChain with the resolver: %v", err)
	}

	// Without it, the same transaction fails closed rather than being read as a
	// payment to an address nobody controls.
	if err := tx.VerifyForChain(testChainID, nil); err == nil {
		t.Fatal("a reserved-address transaction verified without a resolver")
	}
	if err := tx.Verify(); err == nil {
		t.Fatal("a reserved-address transaction verified through the plain path")
	}
}
