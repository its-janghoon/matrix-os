package bridge

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// LockIDLen is the length in bytes of a lock id. It matches Solidity's bytes32.
const LockIDLen = 32

// ErrThresholdNotMet is returned when fewer than the required number of distinct
// registered attestors have signed an attestation.
var ErrThresholdNotMet = errors.New("bridge: attestation threshold not met")

// ErrDuplicateAttestor is returned when the same attestor signs an attestation
// more than once (which must not count twice toward the threshold).
var ErrDuplicateAttestor = errors.New("bridge: duplicate attestor signature")

// AttestationParams are the immutable, chain-binding parameters that both the Go
// signer and the Solidity verifier fold into every attestation digest. Binding
// the chain id and the bridge contract address prevents a signature that is
// valid for one deployment from being replayed against another (cross-chain or
// cross-contract replay).
type AttestationParams struct {
	// ChainID is the EVM chain id the WrappedMatrix contract is deployed on.
	ChainID *big.Int
	// BridgeContract is the address of the WrappedMatrix contract.
	BridgeContract Address
}

// AttestationDigest computes the 32-byte keccak256 digest that validators sign
// and the Solidity contract recomputes to authorize a mint. The preimage is the
// tight abi.encodePacked layout
//
//	recipient (20) || amount (32) || lockId (32) || chainId (32) || bridge (20)
//
// where recipient is the wrapped-token recipient address, amount is the wrapped
// ERC-20 amount (18 decimals, uint256), lockId is the unique lock identifier,
// chainId and bridge come from params. WrappedMatrix.sol MUST hash the identical
// bytes (see _digest there) so a Go-produced signature recovers on-chain.
func AttestationDigest(recipient Address, amount *big.Int, lockID [LockIDLen]byte, params AttestationParams) []byte {
	return keccak256(
		recipient[:],
		bigTo32(amount),
		lockID[:],
		bigTo32(params.ChainID),
		params.BridgeContract[:],
	)
}

// Attestation is the evidence the Ethereum side verifies before minting. It
// names the wrapped-token recipient, the wrapped amount, the originating lock
// id, and carries a set of validator secp256k1 signatures over the canonical
// digest (see AttestationDigest).
type Attestation struct {
	// Recipient is the Ethereum address that will receive the wrapped tokens.
	Recipient Address `json:"recipient"`
	// Amount is the wrapped ERC-20 amount (18 decimals) to mint, equal to the
	// locked native base units converted via token.NativeToERC20.
	Amount *big.Int `json:"amount"`
	// LockID is the unique identifier of the native lock this attestation
	// authorizes minting for. The Solidity contract mints each LockID at most
	// once.
	LockID [LockIDLen]byte `json:"lock_id"`
	// Signatures are the 65-byte r||s||v secp256k1 signatures of individual
	// validators over the canonical digest.
	Signatures [][]byte `json:"signatures"`
}

// Verify checks the attestation against the given attestor set and threshold. It
// recomputes the canonical digest, recovers the signer of each signature, and
// requires at least `threshold` DISTINCT signatures that each recover to an
// address in `attestors`. Signatures from unknown addresses, malformed
// signatures, and duplicate signers are rejected; a duplicate signer returns
// ErrDuplicateAttestor and a shortfall returns ErrThresholdNotMet. This mirrors
// the on-chain WrappedMatrix.mint verification so a Go-verified attestation is
// exactly the one the contract will accept.
func (a *Attestation) Verify(attestors map[Address]bool, threshold int, params AttestationParams) error {
	if a.Amount == nil {
		return fmt.Errorf("%w: nil amount", ErrInvalidSignature)
	}
	if threshold <= 0 {
		return fmt.Errorf("bridge: threshold must be positive, got %d", threshold)
	}
	digest := AttestationDigest(a.Recipient, a.Amount, a.LockID, params)
	seen := make(map[Address]bool)
	valid := 0
	for i, sig := range a.Signatures {
		addr, err := RecoverAddress(digest, sig)
		if err != nil {
			return fmt.Errorf("bridge: signature %d: %w", i, err)
		}
		if !attestors[addr] {
			return fmt.Errorf("%w: signer %s is not a registered attestor", ErrInvalidSignature, addr)
		}
		if seen[addr] {
			return fmt.Errorf("%w: %s signed twice", ErrDuplicateAttestor, addr)
		}
		seen[addr] = true
		valid++
	}
	if valid < threshold {
		return fmt.Errorf("%w: got %d valid signatures, need %d", ErrThresholdNotMet, valid, threshold)
	}
	return nil
}

// SortSignaturesForChain reorders the attestation's signatures into strictly
// ascending recovered-signer-address order. The WrappedMatrix.mint contract
// requires that ordering (it uses ascending order to cheaply enforce distinct
// signers), so callers that submit an attestation on-chain should call this
// first. It recomputes the digest to recover each signer and returns an error if
// any signature is malformed. Duplicate signers are collapsed is NOT performed
// here; the contract rejects duplicates.
func (a *Attestation) SortSignaturesForChain(params AttestationParams) error {
	digest := AttestationDigest(a.Recipient, a.Amount, a.LockID, params)
	type sigWithAddr struct {
		sig  []byte
		addr Address
	}
	items := make([]sigWithAddr, 0, len(a.Signatures))
	for i, sig := range a.Signatures {
		addr, err := RecoverAddress(digest, sig)
		if err != nil {
			return fmt.Errorf("bridge: signature %d: %w", i, err)
		}
		items = append(items, sigWithAddr{sig: sig, addr: addr})
	}
	sort.Slice(items, func(i, j int) bool {
		return bytes.Compare(items[i].addr[:], items[j].addr[:]) < 0
	})
	sorted := make([][]byte, len(items))
	for i := range items {
		sorted[i] = items[i].sig
	}
	a.Signatures = sorted
	return nil
}

// ValidatorSigner pairs a validator's secp256k1 attestor with a human label for
// building an attestation. The label is advisory (e.g. the validator's ed25519
// account id) and is not part of the signed digest.
type ValidatorSigner struct {
	Label    string
	Attestor *Attestor
}

// SignAttestation produces an Attestation for the given lock parameters, signed
// by each of the provided validator attestors over the canonical digest. It is
// the Go counterpart to the on-chain verification: feed the returned Attestation
// (recipient, amount, lockId, signatures) into WrappedMatrix.mint. Signers must
// be distinct; duplicates would be rejected by Verify and by the contract.
func SignAttestation(recipient Address, amount *big.Int, lockID [LockIDLen]byte, params AttestationParams, signers []ValidatorSigner) (*Attestation, error) {
	if amount == nil {
		return nil, fmt.Errorf("%w: nil amount", ErrInvalidSignature)
	}
	if len(signers) == 0 {
		return nil, fmt.Errorf("bridge: at least one signer required")
	}
	digest := AttestationDigest(recipient, amount, lockID, params)
	att := &Attestation{
		Recipient:  recipient,
		Amount:     new(big.Int).Set(amount),
		LockID:     lockID,
		Signatures: make([][]byte, 0, len(signers)),
	}
	for _, s := range signers {
		if s.Attestor == nil {
			return nil, fmt.Errorf("bridge: signer %q has nil attestor", s.Label)
		}
		sig, err := s.Attestor.SignDigest(digest)
		if err != nil {
			return nil, fmt.Errorf("bridge: sign for %q: %w", s.Label, err)
		}
		att.Signatures = append(att.Signatures, sig)
	}
	return att, nil
}
