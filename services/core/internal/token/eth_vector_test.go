package token

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/ethsig"
)

// These vectors come from ethers v6 - ethers.TypedDataEncoder.hash and
// wallet.signTypedData - which is the same computation MetaMask performs for
// eth_signTypedData_v4.
//
// This is the test that decides whether MetaMask works at all. Every other test
// in eth_test.go checks that my implementation is self-consistent, which it
// would be even if the digest were wrong; only agreeing with a real Ethereum
// library proves a wallet will produce a signature this code accepts. A drift
// here surfaces to a user as "invalid signature", which reads as a key problem
// and sends you looking in the wrong place.
//
// Regenerate with (from contracts/, where ethers is installed):
//
//	node -e '...ethers.TypedDataEncoder.hash(domain, types, value)...'
//
// The domain is {name: "Matrix OS", version: "1", salt: keccak256("matrix-os-native-l1")}.
const (
	vectorAddress         = "0x19E7E376E7C213B7E7e7e46cc70A5dD086DAff2A"
	vectorDomainSeparator = "28f5b183fb57116b085f776ea6b8a21043b279ab44af26aa806feb10554db908"

	vectorTransferDigest = "facc0d11a3db56f52f0e96178c3c711a2af353584871596d019f492ee5aa26d2"
	vectorTransferSig    = "9e6b40350d6c3d36b1bbd63f180090a085e43dccbf0d568a35364dcf5322221f" +
		"17f82f6d62482cac3a1dc355e51d30056f2812704c04f192de0223b877af6882" + "1b"

	vectorRunDigest = "2553ecc42296ceeb54c003fdb3e5ca875631e739c3e5fd2b308bfa00b4de5b7a"
	vectorRunSig    = "d8bd6586c8ba0c17ca7216fb34dea7c1ad4b02027558cee0c83d1dd2139587a4" +
		"18d62e0fe8223754927332b705afe653e50e2a960f091e3d705f0f05805368ab" + "1b"

	// prevHash in the vector is the single byte 0xde as a bytes32, which ethers
	// writes RIGHT-padded. That is worth pinning: our own digests left-pad a
	// short value, and the two conventions disagreeing is exactly the kind of
	// silent mismatch this test exists to catch.
	vectorPrevHash = "de00000000000000000000000000000000000000000000000000000000000000"

	vectorPromptDigest = "1c8aff950685c2ed4bc3174f3472287b56d9517b9c948127319a09a7a36deac8"
	vectorTimestamp    = int64(1788769228123456789)
)

func TestTheDomainSeparatorMatchesEthers(t *testing.T) {
	if got := hex.EncodeToString(eip712DomainSeparator()); got != vectorDomainSeparator {
		t.Fatalf("domain separator = %s,\n                want %s", got, vectorDomainSeparator)
	}
}

func TestTheTransferDigestMatchesEthers(t *testing.T) {
	addr, err := ethsig.ParseAddress(vectorAddress)
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	prevHash, err := hex.DecodeString(vectorPrevHash)
	if err != nil {
		t.Fatalf("bad prevHash hex: %v", err)
	}

	tx := &Transaction{
		From:      addr[:],
		To:        "bob",
		Amount:    100,
		Nonce:     3,
		Timestamp: vectorTimestamp,
		PrevHash:  prevHash,
	}
	got, err := tx.EthTransferDigest()
	if err != nil {
		t.Fatalf("EthTransferDigest: %v", err)
	}
	if hex.EncodeToString(got) != vectorTransferDigest {
		t.Fatalf("transfer digest = %s,\n                want %s",
			hex.EncodeToString(got), vectorTransferDigest)
	}
}

// TestAWalletSignatureVerifies is the end of the chain: a signature produced by
// a real Ethereum library over a real EIP-712 payload is accepted by Verify, and
// recovers to the account the sender claims.
func TestAWalletSignatureVerifies(t *testing.T) {
	addr, err := ethsig.ParseAddress(vectorAddress)
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	prevHash, _ := hex.DecodeString(vectorPrevHash)
	sig, err := hex.DecodeString(vectorTransferSig)
	if err != nil {
		t.Fatalf("bad signature hex: %v", err)
	}

	tx := &Transaction{
		From:      addr[:],
		To:        "bob",
		Amount:    100,
		Nonce:     3,
		Timestamp: vectorTimestamp,
		PrevHash:  prevHash,
		Signature: sig,
	}
	if err := tx.Verify(); err != nil {
		t.Fatalf("a real wallet's signature does not verify: %v", err)
	}
	if want := EthAccountID(addr); tx.SenderID() != want {
		t.Fatalf("SenderID = %s, want %s", tx.SenderID(), want)
	}
}

func TestTheRunAuthorizationDigestMatchesEthers(t *testing.T) {
	addr, err := ethsig.ParseAddress(vectorAddress)
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	promptDigest, err := hex.DecodeString(vectorPromptDigest)
	if err != nil {
		t.Fatalf("bad promptDigest hex: %v", err)
	}

	got, err := EthRunAuthorizationDigest(addr, "gpu-1", "llama-3.3-70b", promptDigest, vectorTimestamp)
	if err != nil {
		t.Fatalf("EthRunAuthorizationDigest: %v", err)
	}
	if hex.EncodeToString(got) != vectorRunDigest {
		t.Fatalf("run digest = %s,\n           want %s", hex.EncodeToString(got), vectorRunDigest)
	}
}

func TestARunAuthorizationWalletSignatureRecoversTheBuyer(t *testing.T) {
	addr, _ := ethsig.ParseAddress(vectorAddress)
	promptDigest, _ := hex.DecodeString(vectorPromptDigest)
	sig, err := hex.DecodeString(vectorRunSig)
	if err != nil {
		t.Fatalf("bad signature hex: %v", err)
	}

	digest, err := EthRunAuthorizationDigest(addr, "gpu-1", "llama-3.3-70b", promptDigest, vectorTimestamp)
	if err != nil {
		t.Fatalf("EthRunAuthorizationDigest: %v", err)
	}
	recovered, err := ethsig.RecoverAddress(digest, sig)
	if err != nil {
		t.Fatalf("RecoverAddress: %v", err)
	}
	if !strings.EqualFold(recovered.Hex(), vectorAddress) {
		t.Fatalf("recovered %s, want %s", recovered.Hex(), vectorAddress)
	}
}
