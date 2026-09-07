package token

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ecirlabs/matrix-core/internal/ethsig"
)

// This file lets an ETHEREUM key control a native account, so MetaMask can be
// the wallet.
//
// The problem it solves: native accounts are ed25519, MetaMask is secp256k1, and
// MetaMask cannot sign an ed25519 anything. That left a user needing two wallets
// - MetaMask for wMATRIX on Ethereum, and something of ours for native MATRIX -
// which is the state Solana, Near and Aptos are all in, and it is a real cost.
//
// WHAT WAS NOT DONE, and why. Switching native signing to secp256k1 would rewrite
// the identity of every existing account and every test for the same outcome. And
// having MetaMask sign the existing canonical bytes is not possible: MetaMask
// signs EIP-712 typed data or EIP-191 personal messages, never arbitrary bytes.
//
// So a second account KIND exists alongside the first:
//
//	<64 hex>      an ed25519 account. Unchanged, byte for byte.
//	eth:0x<40hex> controlled by that Ethereum address.
//
// The two are told apart by the account id, NOT by a new field in the signed
// payload. That is deliberate: adding a scheme byte to Transaction.SigningBytes
// would change every ed25519 signature ever produced, which the doc comment on
// SigningBytes correctly calls a consensus-relevant invariant. An eth account
// simply has a different signable payload - the EIP-712 digest below - and
// Verify dispatches on which kind of sender it is looking at.

// EthAccountPrefix marks an account controlled by an Ethereum key.
//
// A prefix rather than a bare 40-hex string, because "is this 40 hex characters
// or 64" is the kind of distinction that works until someone truncates an id in
// a log. The ledger already holds structured ids ("bridge/escrow",
// "consensus/stake/bond/<id>"), so this is not a new shape.
const EthAccountPrefix = "eth:"

// ErrNotEthAccount is returned when an account id is not an Ethereum-controlled
// one.
var ErrNotEthAccount = errors.New("token: not an ethereum-controlled account")

// EthAccountID returns the native account id an Ethereum address controls. It
// lowercases the address, so one address is one account however it was typed.
func EthAccountID(addr ethsig.Address) string {
	return EthAccountPrefix + strings.ToLower(addr.Hex())
}

// IsEthAccountID reports whether an id names an Ethereum-controlled account.
func IsEthAccountID(id string) bool {
	return strings.HasPrefix(id, EthAccountPrefix)
}

// ParseEthAccountID recovers the address from an Ethereum-controlled account id.
func ParseEthAccountID(id string) (ethsig.Address, error) {
	if !IsEthAccountID(id) {
		return ethsig.Address{}, fmt.Errorf("%w: %q", ErrNotEthAccount, id)
	}
	return ethsig.ParseAddress(strings.TrimPrefix(id, EthAccountPrefix))
}

// The EIP-712 domain. It carries a name, a version and a salt, and deliberately
// NO chainId or verifyingContract.
//
// chainId is omitted because this chain is not an EVM chain and claiming an EVM
// chain id we do not own would be squatting on someone else's domain separator.
// The salt gives the separation instead, and because it is unique to us a
// signature produced here cannot be replayed as a signature for any EVM
// contract's domain.
const (
	eip712DomainName    = "Matrix OS"
	eip712DomainVersion = "1"
	eip712DomainSalt    = "matrix-os-native-l1"
)

// The EIP-712 type strings. These are part of the signature: a wallet hashes
// them, so changing one by a character invalidates every signature made under
// the old form. They are also what MetaMask SHOWS the user, which is why the
// field names are the plain words a person would expect rather than internal
// ones.
const (
	eip712DomainType = "EIP712Domain(string name,string version,bytes32 salt)"

	// Transfer is a native MATRIX transfer. `to` is a string because a recipient
	// may be an ed25519 id, an eth: id, or a reserved account like
	// "bridge/escrow" - an address type could not express those.
	eip712TransferType = "Transfer(address from,string to,uint256 amount,uint256 nonce,int64 timestamp,bytes32 prevHash)"

	// RunAuthorization is the buyer's proof it is asking for a specific
	// inference. promptDigest keeps the transcript out of the wallet prompt while
	// still binding the signature to it.
	eip712RunAuthorizationType = "RunAuthorization(address buyer,string provider,string model,bytes32 promptDigest,int64 timestamp)"
)

// eip712DomainSeparator is the hashed domain, computed once.
func eip712DomainSeparator() []byte {
	return ethsig.Keccak256(
		ethsig.Keccak256([]byte(eip712DomainType)),
		ethsig.Keccak256([]byte(eip712DomainName)),
		ethsig.Keccak256([]byte(eip712DomainVersion)),
		ethsig.Keccak256([]byte(eip712DomainSalt)),
	)
}

// eip712Digest assembles the final digest a wallet signs:
// keccak256(0x19 || 0x01 || domainSeparator || structHash). The 0x1901 prefix is
// EIP-191's version byte for structured data, and it is what stops an EIP-712
// signature from also being a valid signature over a plain message.
func eip712Digest(structHash []byte) []byte {
	return ethsig.Keccak256([]byte{0x19, 0x01}, eip712DomainSeparator(), structHash)
}

// eip712Address encodes an address as EIP-712 requires: 32 bytes, left-padded.
func eip712Address(addr ethsig.Address) []byte {
	out := make([]byte, 32)
	copy(out[12:], addr[:])
	return out
}

// eip712Uint256 encodes a uint64 as a 32-byte big-endian value.
func eip712Uint256(v uint64) []byte {
	out := make([]byte, 32)
	binary.BigEndian.PutUint64(out[24:], v)
	return out
}

// eip712Int64 encodes a signed 64-bit value as a 32-byte two's-complement value,
// which is how EIP-712 encodes any signed integer type.
func eip712Int64(v int64) []byte {
	return leftPadBig(big.NewInt(v))
}

func leftPadBig(v *big.Int) []byte {
	out := make([]byte, 32)
	if v.Sign() >= 0 {
		v.FillBytes(out)
		return out
	}
	// Two's complement in 256 bits.
	mod := new(big.Int).Lsh(big.NewInt(1), 256)
	mod.Add(mod, v)
	mod.FillBytes(out)
	return out
}

// eip712Bytes32 encodes a value as a bytes32. It accepts exactly 32 bytes, or
// nothing (which is the zero value), and REFUSES anything in between.
//
// Padding is the trap. Ethereum tooling right-pads a short bytes32 while every
// integer in this file is left-padded, so a short value would encode one way
// here and the other way in the wallet, and the two digests would differ. The
// resulting failure reads as "invalid signature" and sends you looking at the
// key. Refusing a short value means the question never arises: the only bytes32
// values in this protocol are 32-byte hashes.
func eip712Bytes32(b []byte) ([]byte, error) {
	out := make([]byte, 32)
	switch len(b) {
	case 0:
		return out, nil
	case 32:
		copy(out, b)
		return out, nil
	default:
		return nil, fmt.Errorf("token: a bytes32 must be 32 bytes or empty, got %d: "+
			"a shorter value would be padded differently here and in a wallet", len(b))
	}
}

// EthTransferDigest returns the EIP-712 digest a MetaMask user signs to
// authorise this transfer. It is the eth-account counterpart of SigningBytes,
// and the ed25519 payload is untouched.
func (t *Transaction) EthTransferDigest() ([]byte, error) {
	from, err := ethsig.AddressFromBytes(t.From)
	if err != nil {
		return nil, fmt.Errorf("token: transfer sender is not an ethereum address: %w", err)
	}
	prevHash, err := eip712Bytes32(t.PrevHash)
	if err != nil {
		return nil, err
	}
	structHash := ethsig.Keccak256(
		ethsig.Keccak256([]byte(eip712TransferType)),
		eip712Address(from),
		ethsig.Keccak256([]byte(t.To)),
		eip712Uint256(t.Amount),
		eip712Uint256(t.Nonce),
		eip712Int64(t.Timestamp),
		prevHash,
	)
	return eip712Digest(structHash), nil
}

// EthRunAuthorizationDigest returns the EIP-712 digest for an inference run
// authorization. It lives here rather than in internal/inference so the typed
// data and the domain have one home: a domain defined in two places is a domain
// that will differ in one of them.
func EthRunAuthorizationDigest(buyer ethsig.Address, provider, model string, promptDigest []byte, timestamp int64) ([]byte, error) {
	digest, err := eip712Bytes32(promptDigest)
	if err != nil {
		return nil, err
	}
	// Field order follows the type string exactly. EIP-712 hashes the encoded
	// members in declaration order, so a reordering here would produce a digest
	// no wallet computes.
	structHash := ethsig.Keccak256(
		ethsig.Keccak256([]byte(eip712RunAuthorizationType)),
		eip712Address(buyer),
		ethsig.Keccak256([]byte(provider)),
		ethsig.Keccak256([]byte(model)),
		digest,
		eip712Int64(timestamp),
	)
	return eip712Digest(structHash), nil
}

// VerifyEthTransfer checks that an Ethereum-controlled account authorised this
// transfer. The recovered address must be the one the sender id names, which is
// what stops a valid signature from a different address being accepted.
func (t *Transaction) VerifyEthTransfer() error {
	if len(t.From) != ethsig.AddressLen {
		return fmt.Errorf("%w: an ethereum sender is %d bytes, got %d",
			ErrInvalidTransaction, ethsig.AddressLen, len(t.From))
	}
	if len(t.Signature) == 0 {
		return ErrUnsignedTransaction
	}
	digest, err := t.EthTransferDigest()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTransaction, err)
	}
	recovered, err := ethsig.RecoverAddress(digest, t.Signature)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	declared, err := ethsig.AddressFromBytes(t.From)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTransaction, err)
	}
	if recovered != declared {
		return fmt.Errorf("%w: signed by %s, but the sender is %s",
			ErrInvalidSignature, recovered.Hex(), declared.Hex())
	}
	return nil
}
