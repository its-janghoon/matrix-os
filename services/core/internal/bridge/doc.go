// Package bridge implements the native side of a lock-and-mint bridge that
// connects native MATRIX (the consensus L1's canonical coin) to its wrapped
// ERC-20 mirror on Ethereum, and back.
//
// # Model
//
// Native MATRIX is the single source of truth (see internal/market and
// internal/token). The Ethereum ERC-20 is a WRAPPED representation that must be
// backed 1:1 by native MATRIX locked on this chain. The bridge enforces that
// invariant with two symmetric flows:
//
//	lock  (native -> wrapped): a user's native MATRIX is moved from their
//	      account into a bridge-controlled escrow account on the consensus
//	      ledger. The event is recorded and the validator set produces an
//	      attestation. The Ethereum contract mints wrapped tokens only after
//	      verifying a threshold of those validator signatures.
//
//	burn  (wrapped -> native): the Ethereum contract burns wrapped tokens and
//	      emits an unlock event naming the native recipient. The bridge applies
//	      that event exactly once (replay-protected) and releases the escrowed
//	      native MATRIX back to the recipient.
//
// Because every mint is gated on a real lock and every unlock consumes a real
// burn exactly once, total locked native always reconciles 1:1 (via the
// token.NativeToERC20 conversion) with the outstanding wrapped supply. Reconcile
// proves this on the native side; the Solidity WrappedMatrix.totalLocked / total
// supply invariant proves it on the Ethereum side.
//
// # Attestation scheme
//
// Native locking/unlocking and consensus itself use ed25519 accounts. The EVM,
// however, cannot verify ed25519 natively; its only signature precompile is
// ecrecover (secp256k1/ECDSA). So the bridge uses a SECOND key per validator: a
// secp256k1 "attestor" key whose Ethereum address is registered in the Solidity
// contract's attestor set. Validators sign the canonical attestation digest
//
//	keccak256(recipient || amount || lockId || chainId || bridgeContract)
//
// (the exact packing WrappedMatrix.sol recomputes) with their secp256k1 key,
// producing an Ethereum-style 65-byte {r,s,v} signature that ecrecover accepts.
// The Go signing (SignDigest) and the Solidity verification therefore operate on
// byte-identical digests, so a Go-produced attestation genuinely verifies
// on-chain. A threshold (m-of-n) of distinct registered attestors is required to
// mint, and each lockId can be minted only once.
//
// # Dependencies
//
// This package uses only crypto already present in the module's build graph: the
// standard library (crypto/sha256, crypto/ed25519 via internal/token) plus
// github.com/decred/dcrd/dcrec/secp256k1/v4 (transitive from libp2p) for
// secp256k1 signing and golang.org/x/crypto/sha3 for keccak256. It adds no new
// external module dependency.
package bridge
