package evmtx

import (
	"encoding/hex"
	"errors"
	"math/big"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/ecirlabs/matrix-core/internal/ethsig"
)

// The worked example published with EIP-155. It is the ground truth for this
// package: every Ethereum client agrees on these bytes, so an encoder that
// reproduces them is compatible with all of them, and one that does not
// produces transactions MetaMask signs and this chain rejects.
const (
	eip155SigningPayload = "ec098504a817c800825208943535353535353535353535353535353535353535880de0b6b3a764000080018080"
	eip155PrivateKey     = "4646464646464646464646464646464646464646464646464646464646464646"
	eip155SignedTx       = "f86c098504a817c800825208943535353535353535353535353535353535353535880de0b6b3a764000080" +
		"25a028ef61340bd939bc2195fe537567866003e1a15d3c71ff63e1590620aa636276" +
		"a067cbe9d8997f761aecb703304b3800ccf555c9f3dc64214b297fb1966a3b6d83"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex literal: %v", err)
	}
	return b
}

func mustAddress(t *testing.T, s string) *ethsig.Address {
	t.Helper()
	addr, err := ethsig.ParseAddress(s)
	if err != nil {
		t.Fatalf("ParseAddress(%s): %v", s, err)
	}
	return &addr
}

// eip155Transaction builds the EIP-155 example as an unsigned transaction.
func eip155Transaction(t *testing.T) *Transaction {
	t.Helper()
	value, _ := new(big.Int).SetString("1000000000000000000", 10)
	return &Transaction{
		Type:      TxLegacy,
		ChainID:   1,
		Nonce:     9,
		GasFeeCap: big.NewInt(20_000_000_000),
		Gas:       21000,
		To:        mustAddress(t, "0x3535353535353535353535353535353535353535"),
		Value:     value,
	}
}

// TestEIP155SigningPayloadMatchesTheSpecification is the check that decides
// whether this chain is Ethereum-compatible at all. The signing payload is what
// the wallet hashes; if a single byte differs, the wallet's signature recovers
// to a different address and nothing downstream can tell why.
func TestEIP155SigningPayloadMatchesTheSpecification(t *testing.T) {
	tx := eip155Transaction(t)

	payload, err := tx.signingPayload()
	if err != nil {
		t.Fatalf("signingPayload: %v", err)
	}
	if got := hex.EncodeToString(payload); got != eip155SigningPayload {
		t.Fatalf("signing payload =\n  %s\nwant\n  %s", got, eip155SigningPayload)
	}

	// Checked against keccak256 of the PUBLISHED payload rather than against a
	// hash constant. A constant copied from our own output would assert nothing;
	// this asserts that SigningHash hashes the specification's bytes. What
	// proves the whole path end to end is the recovery test below, which
	// recovers the specification's published signature to the address derived
	// independently from its published private key.
	digest, err := tx.SigningHash()
	if err != nil {
		t.Fatalf("SigningHash: %v", err)
	}
	want := ethsig.Keccak256(mustHex(t, eip155SigningPayload))
	if hex.EncodeToString(digest) != hex.EncodeToString(want) {
		t.Fatalf("signing hash = %s, want %s", hex.EncodeToString(digest), hex.EncodeToString(want))
	}
}

// TestEIP155SignedTransactionRoundTrips decodes the published signed
// transaction, re-encodes it, and recovers its sender. It proves the decoder,
// the encoder and the recovery agree with the specification at once.
func TestEIP155SignedTransactionRoundTrips(t *testing.T) {
	raw := mustHex(t, eip155SignedTx)

	tx, err := DecodeRaw(raw)
	if err != nil {
		t.Fatalf("DecodeRaw: %v", err)
	}
	if tx.Type != TxLegacy {
		t.Fatalf("type = %d, want legacy", tx.Type)
	}
	if tx.Nonce != 9 || tx.Gas != 21000 {
		t.Fatalf("nonce/gas = %d/%d, want 9/21000", tx.Nonce, tx.Gas)
	}
	if tx.GasFeeCap.Cmp(big.NewInt(20_000_000_000)) != 0 {
		t.Fatalf("gasPrice = %s, want 20000000000", tx.GasFeeCap)
	}
	if tx.To == nil || tx.To.Hex() != "0x3535353535353535353535353535353535353535" {
		t.Fatalf("to = %v, want 0x3535...35", tx.To)
	}
	if tx.ChainID != 1 {
		t.Fatalf("chain id recovered from V = %d, want 1", tx.ChainID)
	}

	// The sender must be the address of the private key the specification used.
	priv := secp256k1.PrivKeyFromBytes(mustHex(t, eip155PrivateKey))
	want := ethsig.AddressFromPubKey(priv.PubKey())
	got, err := tx.Sender(1)
	if err != nil {
		t.Fatalf("Sender: %v", err)
	}
	if got != want {
		t.Fatalf("sender = %s, want %s", got.Hex(), want.Hex())
	}

	// Re-encoding has to reproduce the input exactly, because the transaction id
	// is the hash of these bytes.
	reencoded, err := tx.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	if hex.EncodeToString(reencoded) != eip155SignedTx {
		t.Fatalf("re-encoding =\n  %s\nwant\n  %s", hex.EncodeToString(reencoded), eip155SignedTx)
	}

	// And the id itself must be stable and 32 bytes.
	hash, err := tx.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if len(hash) != 32 {
		t.Fatalf("transaction id is %d bytes, want 32", len(hash))
	}
}

// TestSignThenRecover proves our own signing produces what our own recovery
// reads, for both envelopes. It is the path a test wallet takes.
func TestSignThenRecover(t *testing.T) {
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	want := ethsig.AddressFromPubKey(priv.PubKey())
	const chainID = 61_337

	for _, txType := range []TxType{TxLegacy, TxDynamicFee} {
		t.Run(map[TxType]string{TxLegacy: "legacy", TxDynamicFee: "dynamic-fee"}[txType], func(t *testing.T) {
			tx := &Transaction{
				Type:      txType,
				Nonce:     7,
				GasFeeCap: big.NewInt(1_000_000_000),
				GasTipCap: big.NewInt(1_000_000),
				Gas:       21000,
				To:        mustAddress(t, "0x00000000000000000000000000000000000000aa"),
				Value:     big.NewInt(12345),
				Data:      []byte{0xde, 0xad, 0xbe, 0xef},
			}
			if txType == TxLegacy {
				tx.GasTipCap = nil
			}
			if err := tx.Sign(priv, chainID); err != nil {
				t.Fatalf("Sign: %v", err)
			}

			raw, err := tx.MarshalBinary()
			if err != nil {
				t.Fatalf("MarshalBinary: %v", err)
			}
			decoded, err := DecodeRaw(raw)
			if err != nil {
				t.Fatalf("DecodeRaw: %v", err)
			}
			got, err := decoded.Sender(chainID)
			if err != nil {
				t.Fatalf("Sender: %v", err)
			}
			if got != want {
				t.Fatalf("sender = %s, want %s", got.Hex(), want.Hex())
			}
			if decoded.Nonce != 7 || decoded.Value.Cmp(big.NewInt(12345)) != 0 {
				t.Fatalf("fields did not survive: nonce %d value %s", decoded.Nonce, decoded.Value)
			}
		})
	}
}

// TestSenderRejectsAnotherChainsTransaction is the property EIP-155 exists for,
// and the one the native layout this replaces did not have at all: a signature
// made for one chain must not be usable on another.
func TestSenderRejectsAnotherChainsTransaction(t *testing.T) {
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	for _, txType := range []TxType{TxLegacy, TxDynamicFee} {
		tx := &Transaction{
			Type:      txType,
			Nonce:     1,
			GasFeeCap: big.NewInt(1),
			GasTipCap: big.NewInt(1),
			Gas:       21000,
			To:        mustAddress(t, "0x00000000000000000000000000000000000000aa"),
			Value:     big.NewInt(1),
		}
		if txType == TxLegacy {
			tx.GasTipCap = nil
		}
		if err := tx.Sign(priv, 1); err != nil {
			t.Fatalf("Sign: %v", err)
		}
		if _, err := tx.Sender(2); !errors.Is(err, ErrChainIDMismatch) {
			t.Fatalf("Sender on the wrong chain = %v, want ErrChainIDMismatch", err)
		}
		if _, err := tx.Sender(1); err != nil {
			t.Fatalf("Sender on the right chain: %v", err)
		}
	}
}

// TestPreEIP155SignatureIsRefused covers the one legacy form that carries no
// chain id. Accepting it would reintroduce cross-chain replay through the back
// door after EIP-155 closed the front one.
func TestPreEIP155SignatureIsRefused(t *testing.T) {
	tx := eip155Transaction(t)
	tx.R = big.NewInt(1)
	tx.S = big.NewInt(1)
	tx.V = big.NewInt(27)
	if _, err := tx.Sender(1); !errors.Is(err, ErrInvalidTx) {
		t.Fatalf("v=27 signature = %v, want ErrInvalidTx", err)
	}
	tx.V = big.NewInt(28)
	if _, err := tx.Sender(1); !errors.Is(err, ErrInvalidTx) {
		t.Fatalf("v=28 signature = %v, want ErrInvalidTx", err)
	}
}

// TestContractCreationIsDistinctFromTheZeroAddress pins the encoding difference
// that a careless decoder collapses. An empty `to` means contract creation; an
// address of twenty zero bytes is a transfer to a real (if unspendable) account,
// and the two must not decode to the same transaction.
func TestContractCreationIsDistinctFromTheZeroAddress(t *testing.T) {
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	build := func(to *ethsig.Address) []byte {
		tx := &Transaction{Type: TxLegacy, Nonce: 0, GasFeeCap: big.NewInt(1), Gas: 21000, To: to, Value: big.NewInt(0)}
		if err := tx.Sign(priv, 1); err != nil {
			t.Fatalf("Sign: %v", err)
		}
		raw, err := tx.MarshalBinary()
		if err != nil {
			t.Fatalf("MarshalBinary: %v", err)
		}
		return raw
	}

	var zeroAddr ethsig.Address
	creation := build(nil)
	toZero := build(&zeroAddr)
	if hex.EncodeToString(creation) == hex.EncodeToString(toZero) {
		t.Fatal("contract creation and a transfer to the zero address encoded identically")
	}

	decodedCreation, err := DecodeRaw(creation)
	if err != nil {
		t.Fatalf("DecodeRaw(creation): %v", err)
	}
	if decodedCreation.To != nil {
		t.Fatalf("contract creation decoded with to = %s", decodedCreation.To.Hex())
	}
	decodedZero, err := DecodeRaw(toZero)
	if err != nil {
		t.Fatalf("DecodeRaw(to zero): %v", err)
	}
	if decodedZero.To == nil || *decodedZero.To != zeroAddr {
		t.Fatalf("transfer to the zero address decoded as %v", decodedZero.To)
	}
}

// TestDecodeRawRejectsMalformedPayloads covers the inputs an RPC endpoint will
// actually be handed, deliberately or by a broken client.
func TestDecodeRawRejectsMalformedPayloads(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  error
	}{
		{"empty", "", ErrInvalidTx},
		{"an unknown type byte", "05c0", ErrUnsupportedType},
		{"a legacy list of the wrong length", "c50102030405", ErrInvalidTx},
		{"an unsigned transaction", "e0098504a817c800825208943535353535353535353535353535353535353535880de0b6b3a7640000808080", ErrInvalidTx},
		{"trailing bytes", eip155SignedTx + "00", ErrInvalidTx},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeRaw(mustHex(t, tc.input)); !errors.Is(err, tc.want) {
				t.Fatalf("DecodeRaw = %v, want %v", err, tc.want)
			}
		})
	}
}
