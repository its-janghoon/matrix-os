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
//	      emits a Burned event naming the native recipient and amount.
//	      DecodeBurnedLog parses that on-chain log (topics + ABI data) into a
//	      BurnEvent with a stable txHash:logIndex id, and ProcessBurn applies it
//	      exactly once (replay-protected on that id), releasing the escrowed
//	      native MATRIX back to the recipient. The decoder makes the unlock half
//	      a real, testable step from an actual emitted event rather than a
//	      hand-built value; see burnlog.go and burnlog_test.go (and the
//	      contracts BridgeE2E test, which cross-checks the raw log bytes).
//
//	      Ingestion of those Burned logs is automated by Watcher (watcher.go): an
//	      always-on eth_getLogs poller that pulls Burned events off an Ethereum
//	      endpoint and drives each through ProcessBurn exactly once, with
//	      confirmation-depth handling and an optionally persisted scan cursor. It
//	      runs as a matrixd subsystem against the node's own ledger (the
//	      bridge.watch config section; see internal/node/bridge_watch.go) or out
//	      of process via cmd/bridge-watch. So both halves are closed loops: the
//	      mint half is end-to-end (a real Go attestation mints on-chain
//	      unmodified) and the unlock half ingests real on-chain events without an
//	      operator hand-feeding logs.
//
//	      What the watcher is NOT is consensus: it applies the unlock to the
//	      ledger of whichever node runs it, rather than through a
//	      consensus-ordered operation. That boundary is stated in watcher.go and
//	      is unchanged by the in-node wiring.
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
