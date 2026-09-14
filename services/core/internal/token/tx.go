package token

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/ecirlabs/matrix-core/internal/ethsig"
)

// Errors related to transaction validation.
var (
	// ErrInvalidSignature is returned when a transaction signature does not
	// verify against the sender's public key and canonical payload.
	ErrInvalidSignature = errors.New("token: invalid transaction signature")
	// ErrUnsignedTransaction is returned when Verify is called on a transaction
	// that carries no signature.
	ErrUnsignedTransaction = errors.New("token: transaction is not signed")
	// ErrInvalidTransaction is returned when a transaction is structurally
	// invalid (e.g. a malformed sender key or empty recipient).
	ErrInvalidTransaction = errors.New("token: invalid transaction")
)

// Transaction is a signed value transfer. From is the sender's raw ed25519
// public key; the sender's AccountID is derived from it. To is the recipient's
// AccountID. Amount is the number of compute credits transferred. Nonce is a
// per-sender monotonically increasing counter that provides replay protection:
// the chain accepts a sender's transaction only when its Nonce equals the
// sender's next expected value (see Chain.Append). Timestamp is an advisory
// wall-clock time (unix nanoseconds) that does not affect validation. PrevHash
// links the transaction to the chain head at append time. Signature is the
// ed25519 signature over the canonical serialization of all the preceding
// fields.
type Transaction struct {
	From      ed25519.PublicKey `json:"from"`
	To        string            `json:"to"`
	Amount    uint64            `json:"amount"`
	Nonce     uint64            `json:"nonce"`
	Timestamp int64             `json:"timestamp"`
	PrevHash  []byte            `json:"prev_hash"`
	Signature []byte            `json:"signature"`

	// ChainID is the chain this transaction is for. It is set only on the
	// Ethereum-envelope path, where it is derived from the signature, and is
	// zero on the two older paths, which carry no chain identifier at all.
	ChainID uint64 `json:"chain_id,omitempty"`
	// Raw is the exact signed Ethereum envelope, present only on that path.
	//
	// It is the AUTHORITY, not a copy: every other field above is derived from
	// these bytes and re-derived on verification, so a peer cannot gossip an
	// envelope with the amount field edited. Signature is empty on this path
	// because the envelope carries its own r, s and v.
	Raw []byte `json:"raw,omitempty"`
}

// SenderID returns the sender's stable account identifier derived from From.
//
// It dispatches on the LENGTH of From, which is what tells the two account kinds
// apart: 32 bytes is an ed25519 public key, 20 bytes is an Ethereum address. The
// discriminator is deliberately not a field in the signed payload - see eth.go -
// because adding one would change every ed25519 signature ever produced.
func (t *Transaction) SenderID() string {
	if len(t.From) == ethsig.AddressLen {
		addr, err := ethsig.AddressFromBytes(t.From)
		if err != nil {
			return ""
		}
		return EthAccountID(addr)
	}
	return AccountIDFromPublicKey(t.From)
}

// SenderIsEth reports whether this transfer is from an Ethereum-controlled
// account, and therefore verified with ecrecover over an EIP-712 digest rather
// than with ed25519 over SigningBytes.
func (t *Transaction) SenderIsEth() bool {
	return len(t.From) == ethsig.AddressLen
}

// SigningBytes returns the canonical, deterministic serialization of the
// signable fields of the transaction (everything except Signature). The layout
// is length-prefixed so no two distinct field combinations can collide:
//
//	uint32(len(From)) | From | uint32(len(To)) | To |
//	uint64(Amount) | uint64(Nonce) | int64(Timestamp) |
//	uint32(len(PrevHash)) | PrevHash
//
// All integers are big-endian. This is the exact byte string that is signed and
// verified, and it is also fed into the chain hash, so its stability is a
// consensus-relevant invariant.
func (t *Transaction) SigningBytes() []byte {
	toBytes := []byte(t.To)
	size := 4 + len(t.From) + 4 + len(toBytes) + 8 + 8 + 8 + 4 + len(t.PrevHash)
	buf := make([]byte, 0, size)

	buf = appendLenPrefixed(buf, t.From)
	buf = appendLenPrefixed(buf, toBytes)

	var scratch [8]byte
	binary.BigEndian.PutUint64(scratch[:], t.Amount)
	buf = append(buf, scratch[:]...)
	binary.BigEndian.PutUint64(scratch[:], t.Nonce)
	buf = append(buf, scratch[:]...)
	binary.BigEndian.PutUint64(scratch[:], uint64(t.Timestamp))
	buf = append(buf, scratch[:]...)

	buf = appendLenPrefixed(buf, t.PrevHash)
	return buf
}

// appendLenPrefixed appends a 4-byte big-endian length followed by the bytes.
func appendLenPrefixed(dst, b []byte) []byte {
	var lp [4]byte
	binary.BigEndian.PutUint32(lp[:], uint32(len(b)))
	dst = append(dst, lp[:]...)
	dst = append(dst, b...)
	return dst
}

// Sign signs the transaction's canonical payload with priv and stores the
// signature on the transaction. It validates that priv corresponds to the
// transaction's From public key so a transaction can never be signed by a key
// other than its declared sender.
func (t *Transaction) Sign(priv ed25519.PrivateKey) error {
	if len(t.From) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: sender key must be %d bytes", ErrInvalidTransaction, ed25519.PublicKeySize)
	}
	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: private key must be %d bytes", ErrInvalidTransaction, ed25519.PrivateKeySize)
	}
	derived := priv.Public().(ed25519.PublicKey)
	if !derived.Equal(t.From) {
		return fmt.Errorf("%w: private key does not match sender public key", ErrInvalidTransaction)
	}
	t.Signature = ed25519.Sign(priv, t.SigningBytes())
	return nil
}

// Verify validates the transaction signature against its From public key and
// canonical payload. It returns ErrUnsignedTransaction when no signature is
// present, ErrInvalidTransaction when the sender key is malformed, and
// ErrInvalidSignature when the signature does not verify. Any mutation to a
// signed field (Amount, Nonce, To, PrevHash, ...) invalidates the signature and
// causes Verify to fail.
func (t *Transaction) Verify() error {
	// An Ethereum-controlled sender has a different signable payload, because
	// MetaMask signs EIP-712 typed data and never arbitrary bytes. The ed25519
	// path below is untouched.
	if t.IsEVM() {
		// The envelope names the chain it was signed for, so it can be verified
		// self-consistently here; confirming it is THIS chain, and resolving any
		// reserved address, needs config and happens in VerifyForChain, which
		// every consensus path calls.
		return t.VerifyEVM(t.ChainID, nil)
	}
	if t.SenderIsEth() {
		return t.VerifyEthTransfer()
	}
	if len(t.From) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: sender key must be %d bytes", ErrInvalidTransaction, ed25519.PublicKeySize)
	}
	if len(t.Signature) == 0 {
		return ErrUnsignedTransaction
	}
	if !ed25519.Verify(t.From, t.SigningBytes(), t.Signature) {
		return ErrInvalidSignature
	}
	return nil
}
