package token

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"

	"github.com/ecirlabs/matrix-core/internal/ethsig"
)

// TestTheEd25519SigningBytesAreUnchanged is the one test in this file that
// protects everything else in the system.
//
// Adding a second account kind must not touch the ed25519 payload by a single
// byte. SigningBytes feeds both the signature and the chain hash, so a change
// there invalidates every signature ever produced and re-hashes every committed
// block. The vector is the same one packages/sdk and apps/web are pinned to.
func TestTheEd25519SigningBytesAreUnchanged(t *testing.T) {
	const golden = "AAAAIAABAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4fAAAACnByb3ZpZGVyLTEA" +
		"AAAAB1vNFQAAAAAAAAAHGNL8IrtyxRUAAAAE3q2+7w=="

	pub := make([]byte, 32)
	for i := range pub {
		pub[i] = byte(i)
	}
	tx := &Transaction{
		From:      pub,
		To:        "provider-1",
		Amount:    123456789,
		Nonce:     7,
		Timestamp: 1788769228123456789,
		PrevHash:  []byte{0xde, 0xad, 0xbe, 0xef},
	}
	if got := base64.StdEncoding.EncodeToString(tx.SigningBytes()); got != golden {
		t.Fatalf("the ed25519 signing bytes changed.\n got %s\nwant %s", got, golden)
	}
}

// TestAnEd25519AccountIsUnaffectedByTheNewKind: the dispatch must not change how
// an existing account signs, verifies or names itself.
func TestAnEd25519AccountIsUnaffectedByTheNewKind(t *testing.T) {
	acct, err := GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	tx := &Transaction{From: acct.PublicKey, To: "bob", Amount: 5, Nonce: 1}
	if err := tx.Sign(acct.PrivateKey); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := tx.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if tx.SenderIsEth() {
		t.Fatal("a 32-byte sender must not be read as an ethereum address")
	}
	if tx.SenderID() != acct.AccountID() {
		t.Fatalf("SenderID = %s, want %s", tx.SenderID(), acct.AccountID())
	}
	if IsEthAccountID(tx.SenderID()) {
		t.Fatal("an ed25519 id must not look like an eth id")
	}
}

// ethWallet stands in for MetaMask: a secp256k1 key that signs a 32-byte digest
// exactly as a wallet's eth_signTypedData_v4 does.
type ethWallet struct {
	priv *secp256k1.PrivateKey
	addr ethsig.Address
}

func newEthWallet(t *testing.T) ethWallet {
	t.Helper()
	priv, err := secp256k1.GeneratePrivateKeyFromRand(rand.Reader)
	if err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	return ethWallet{priv: priv, addr: ethsig.AddressFromPubKey(priv.PubKey())}
}

func (w ethWallet) sign(t *testing.T, digest []byte) []byte {
	t.Helper()
	sig, err := ethsig.SignDigest(w.priv, digest)
	if err != nil {
		t.Fatalf("SignDigest: %v", err)
	}
	return sig
}

// TestAnEthereumKeyCanAuthoriseANativeTransfer is what the whole file is for:
// MetaMask as the wallet, with no ed25519 key anywhere.
func TestAnEthereumKeyCanAuthoriseANativeTransfer(t *testing.T) {
	w := newEthWallet(t)

	tx := &Transaction{
		From:      w.addr[:],
		To:        "bob",
		Amount:    100,
		Nonce:     3,
		Timestamp: 1788769228123456789,
	}
	digest, err := tx.EthTransferDigest()
	if err != nil {
		t.Fatalf("EthTransferDigest: %v", err)
	}
	tx.Signature = w.sign(t, digest)

	if err := tx.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !tx.SenderIsEth() {
		t.Fatal("a 20-byte sender should be read as an ethereum address")
	}
	if want := EthAccountID(w.addr); tx.SenderID() != want {
		t.Fatalf("SenderID = %s, want %s", tx.SenderID(), want)
	}
}

// TestAnotherAddressSignatureIsRefused: recovering successfully is not enough.
// The recovered address has to be the one the sender claims, or a valid
// signature from any key would spend from any account.
func TestAnotherAddressSignatureIsRefused(t *testing.T) {
	victim := newEthWallet(t)
	attacker := newEthWallet(t)

	tx := &Transaction{From: victim.addr[:], To: "bob", Amount: 100, Nonce: 3}
	digest, err := tx.EthTransferDigest()
	if err != nil {
		t.Fatalf("EthTransferDigest: %v", err)
	}
	tx.Signature = attacker.sign(t, digest)

	if err := tx.Verify(); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("err = %v, want ErrInvalidSignature", err)
	}
}

// TestEveryTransferFieldIsCoveredByTheDigest: a signature that did not cover the
// amount or the recipient would be a blank cheque.
func TestEveryTransferFieldIsCoveredByTheDigest(t *testing.T) {
	w := newEthWallet(t)
	// A full 32 bytes: the eth path refuses a short bytes32, because padding it
	// would disagree with the wallet. See TestABytes32IsExactlyThirtyTwoBytesOrNothing.
	prevA := make([]byte, 32)
	prevA[0] = 0x01
	prevB := make([]byte, 32)
	prevB[0] = 0x02

	base := Transaction{
		From: w.addr[:], To: "bob", Amount: 100, Nonce: 3,
		Timestamp: 1, PrevHash: prevA,
	}
	baseDigest, err := base.EthTransferDigest()
	if err != nil {
		t.Fatalf("EthTransferDigest: %v", err)
	}

	mutations := map[string]Transaction{
		"recipient": {From: w.addr[:], To: "attacker", Amount: 100, Nonce: 3, Timestamp: 1, PrevHash: prevA},
		"amount":    {From: w.addr[:], To: "bob", Amount: 999, Nonce: 3, Timestamp: 1, PrevHash: prevA},
		"nonce":     {From: w.addr[:], To: "bob", Amount: 100, Nonce: 4, Timestamp: 1, PrevHash: prevA},
		"timestamp": {From: w.addr[:], To: "bob", Amount: 100, Nonce: 3, Timestamp: 2, PrevHash: prevA},
		"prevHash":  {From: w.addr[:], To: "bob", Amount: 100, Nonce: 3, Timestamp: 1, PrevHash: prevB},
	}
	for name, mutated := range mutations {
		t.Run(name, func(t *testing.T) {
			got, err := mutated.EthTransferDigest()
			if err != nil {
				t.Fatalf("EthTransferDigest: %v", err)
			}
			if hex.EncodeToString(got) == hex.EncodeToString(baseDigest) {
				t.Fatalf("changing the %s did not change the digest", name)
			}
			// And a signature over the original must not verify the mutation.
			mutated.Signature = w.sign(t, baseDigest)
			if err := mutated.Verify(); err == nil {
				t.Fatalf("a signature over the original verified the mutated %s", name)
			}
		})
	}
}

// TestTheDigestIsEIP712Shaped pins the two things a wallet computes
// independently: the 0x1901 prefix and a domain with no chainId. If either
// differs from what MetaMask does, every signature fails and the failure looks
// like a bad key.
func TestTheDigestIsEIP712Shaped(t *testing.T) {
	// The domain separator is fixed, so pin it. Recomputed from the type string
	// and the three field hashes.
	want := hex.EncodeToString(ethsig.Keccak256(
		ethsig.Keccak256([]byte("EIP712Domain(string name,string version,bytes32 salt)")),
		ethsig.Keccak256([]byte("Matrix OS")),
		ethsig.Keccak256([]byte("1")),
		ethsig.Keccak256([]byte("matrix-os-native-l1")),
	))
	if got := hex.EncodeToString(eip712DomainSeparator()); got != want {
		t.Fatalf("domain separator = %s, want %s", got, want)
	}

	// And the digest is keccak(0x19 || 0x01 || domain || structHash).
	structHash := ethsig.Keccak256([]byte("anything"))
	expect := ethsig.Keccak256([]byte{0x19, 0x01}, eip712DomainSeparator(), structHash)
	if hex.EncodeToString(eip712Digest(structHash)) != hex.EncodeToString(expect) {
		t.Fatal("the digest is not the EIP-712 0x1901 construction")
	}
}

// TestTheDomainClaimsNoChainId: claiming an EVM chain id we do not own would
// squat on someone else's domain separator, and it would let a signature made
// here be meaningful on that chain.
func TestTheDomainClaimsNoChainId(t *testing.T) {
	if strings.Contains(eip712DomainType, "chainId") {
		t.Fatal("the domain must not claim a chain id")
	}
	if strings.Contains(eip712DomainType, "verifyingContract") {
		t.Fatal("the domain must not claim a verifying contract")
	}
	if !strings.Contains(eip712DomainType, "salt") {
		t.Fatal("the domain needs a salt to separate it from EVM domains")
	}
}

func TestEthAccountIDsRoundTrip(t *testing.T) {
	w := newEthWallet(t)
	id := EthAccountID(w.addr)

	if !IsEthAccountID(id) {
		t.Fatal("an eth account id should be recognised as one")
	}
	if !strings.HasPrefix(id, "eth:0x") {
		t.Fatalf("id = %q, want an eth:0x prefix", id)
	}
	// Lowercased, so one address is one account however it was typed.
	if id != strings.ToLower(id) {
		t.Fatalf("id = %q, want it lowercased", id)
	}
	back, err := ParseEthAccountID(id)
	if err != nil {
		t.Fatalf("ParseEthAccountID: %v", err)
	}
	if back != w.addr {
		t.Fatalf("round-tripped to %s, want %s", back.Hex(), w.addr.Hex())
	}
}

func TestAMixedCaseAddressNamesTheSameAccount(t *testing.T) {
	// An EIP-55 checksummed address is the form a user copies out of MetaMask,
	// and it has to name the same account as the lowercase form or a user funds
	// one account and spends from another.
	const lower = "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed"
	upper := strings.ToUpper(strings.TrimPrefix(lower, "0x"))

	a, err := ethsig.ParseAddress(lower)
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	b, err := ethsig.ParseAddress("0x" + upper)
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	if EthAccountID(a) != EthAccountID(b) {
		t.Fatalf("%s and %s named different accounts", EthAccountID(a), EthAccountID(b))
	}
}

func TestParseEthAccountIDRejectsWhatItShould(t *testing.T) {
	acct, _ := GenerateAccount()
	for _, id := range []string{
		acct.AccountID(),          // an ed25519 id
		"eth:",                    // no address
		"eth:0xnothex",            // not hex
		"eth:0x1234",              // too short
		"bridge/escrow",           // a reserved account
		"eth:" + acct.AccountID(), // 64 hex, not an address
	} {
		if _, err := ParseEthAccountID(id); err == nil {
			t.Errorf("ParseEthAccountID(%q) should have failed", id)
		}
	}
}

func TestAnUnsignedEthTransferIsRefused(t *testing.T) {
	w := newEthWallet(t)
	tx := &Transaction{From: w.addr[:], To: "bob", Amount: 1}
	if err := tx.Verify(); !errors.Is(err, ErrUnsignedTransaction) {
		t.Fatalf("err = %v, want ErrUnsignedTransaction", err)
	}
}

func TestARunAuthorizationDigestIsBoundToItsFields(t *testing.T) {
	w := newEthWallet(t)
	promptDigest := ethsig.Keccak256([]byte("hello"))

	base, err := EthRunAuthorizationDigest(w.addr, "gpu-1", "llama-3.3-70b", promptDigest, 1)
	if err != nil {
		t.Fatalf("EthRunAuthorizationDigest: %v", err)
	}

	other := newEthWallet(t)
	for name, digest := range map[string][]byte{
		"buyer":    mustDigest(t, other.addr, "gpu-1", "llama-3.3-70b", promptDigest, 1),
		"provider": mustDigest(t, w.addr, "gpu-2", "llama-3.3-70b", promptDigest, 1),
		"model":    mustDigest(t, w.addr, "gpu-1", "qwen-2.5-72b", promptDigest, 1),
		"prompt":   mustDigest(t, w.addr, "gpu-1", "llama-3.3-70b", ethsig.Keccak256([]byte("other")), 1),
		"moment":   mustDigest(t, w.addr, "gpu-1", "llama-3.3-70b", promptDigest, 2),
	} {
		if hex.EncodeToString(digest) == hex.EncodeToString(base) {
			t.Errorf("changing the %s did not change the digest", name)
		}
	}
}

func mustDigest(t *testing.T, buyer ethsig.Address, provider, model string, promptDigest []byte, ts int64) []byte {
	t.Helper()
	d, err := EthRunAuthorizationDigest(buyer, provider, model, promptDigest, ts)
	if err != nil {
		t.Fatalf("EthRunAuthorizationDigest: %v", err)
	}
	return d
}

// TestANegativeTimestampEncodesAsTwosComplement: int64 is signed in the type
// string, and a wallet encodes a negative one in two's complement. Getting this
// wrong would only show up on a clock before 1970, which is exactly the kind of
// bug that ships.
func TestANegativeTimestampEncodesAsTwosComplement(t *testing.T) {
	got := hex.EncodeToString(eip712Int64(-1))
	want := strings.Repeat("ff", 32)
	if got != want {
		t.Fatalf("eip712Int64(-1) = %s, want %s", got, want)
	}
	if hex.EncodeToString(eip712Int64(1)) != strings.Repeat("00", 31)+"01" {
		t.Fatal("eip712Int64(1) is not a left-padded 1")
	}
}

// TestABytes32IsExactlyThirtyTwoBytesOrNothing closes a padding trap rather than
// a size one. Ethereum tooling right-pads a short bytes32 while every integer
// here is left-padded, so a 4-byte value would encode one way in this code and
// the other way in the wallet - and the two digests would differ, surfacing as
// "invalid signature" and sending you to look at the key.
func TestABytes32IsExactlyThirtyTwoBytesOrNothing(t *testing.T) {
	if _, err := eip712Bytes32(make([]byte, 32)); err != nil {
		t.Fatalf("32 bytes should be fine: %v", err)
	}
	zero, err := eip712Bytes32(nil)
	if err != nil {
		t.Fatalf("empty should be the zero value: %v", err)
	}
	if hex.EncodeToString(zero) != strings.Repeat("00", 32) {
		t.Fatalf("empty encoded to %s, want 32 zero bytes", hex.EncodeToString(zero))
	}
	for _, n := range []int{1, 4, 31, 33, 64} {
		if _, err := eip712Bytes32(make([]byte, n)); err == nil {
			t.Errorf("%d bytes should be refused rather than padded ambiguously", n)
		}
	}
}

// TestAShortPrevHashIsRefusedOnTheEthPath: the same trap, reached through the
// transfer digest a caller actually builds.
func TestAShortPrevHashIsRefusedOnTheEthPath(t *testing.T) {
	w := newEthWallet(t)
	tx := &Transaction{From: w.addr[:], To: "bob", Amount: 1, PrevHash: []byte{0xde}}
	if _, err := tx.EthTransferDigest(); err == nil {
		t.Fatal("want a refusal for a 1-byte prev hash")
	}
	// The ed25519 path is unaffected: it length-prefixes, so a short prev hash
	// is unambiguous there and has always been allowed.
	acct, _ := GenerateAccount()
	ed := &Transaction{From: acct.PublicKey, To: "bob", Amount: 1, PrevHash: []byte{0xde}}
	if err := ed.Sign(acct.PrivateKey); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := ed.Verify(); err != nil {
		t.Fatalf("the ed25519 path must still accept a short prev hash: %v", err)
	}
}
