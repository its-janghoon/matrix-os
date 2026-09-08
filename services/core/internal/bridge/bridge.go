package bridge

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"

	"github.com/cockroachdb/pebble"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// KV key prefixes for bridge state. They live under a distinct bridge/*
// namespace so bridge bookkeeping never collides with the market/balance/* or
// token/native/* keyspaces.
const (
	lockPrefix     = "bridge/lock/"    // bridge/lock/<lockId hex> -> LockEvent JSON
	burnPrefix     = "bridge/burn/"    // bridge/burn/<burnId hex> -> BurnEvent JSON (processed marker)
	lockSeqKey     = "bridge/lock_seq" // uint64 monotonically increasing lock counter
	lockedTTLKey   = "bridge/locked"   // uint64 running total of locked native base units
	unlockedTTLKey = "bridge/unlocked" // uint64 running total of unlocked native base units
)

// EscrowAccount is the reserved account ID that holds native MATRIX locked by
// the bridge. Locked coins are transferred here and released from here on
// unlock, so the escrow balance is the on-ledger backing of the wrapped supply.
// It is a fixed, well-known, non-hex identifier so it can never collide with a
// real ed25519-derived account ID (those are 64 lowercase hex chars).
const EscrowAccount = "bridge/escrow"

// Errors specific to the bridge lock/unlock lifecycle.
var (
	// ErrLockNotFound is returned when an operation references a lock id that
	// was never recorded.
	ErrLockNotFound = errors.New("bridge: lock not found")
	// ErrBurnAlreadyProcessed is returned when a burn event with an id that has
	// already been applied is submitted again. It is the replay guard for the
	// unlock path.
	ErrBurnAlreadyProcessed = errors.New("bridge: burn already processed")
	// ErrZeroAmount is returned when a lock or unlock is attempted for zero
	// native base units.
	ErrZeroAmount = errors.New("bridge: amount must be positive")
	// ErrEmptyID is returned when a burn event carries an empty id.
	ErrEmptyID = errors.New("bridge: id must not be empty")
	// ErrConsensusOrdered is returned when ProcessBurn is called on a bridge
	// whose unlocks must come through the consensus engine.
	ErrConsensusOrdered = errors.New("bridge: this bridge is consensus-ordered")
	// ErrNotConsensusOrdered is returned when ApplyAttestedUnlock is called on a
	// bridge that applies burns directly.
	ErrNotConsensusOrdered = errors.New("bridge: this bridge is not consensus-ordered")
)

// LockEvent is the persisted record of a native MATRIX lock. It is the evidence
// that native was escrowed and is the basis for the attestation the Ethereum
// side verifies to mint wrapped tokens.
type LockEvent struct {
	// LockID is the unique 32-byte identifier of this lock (bytes32 on-chain).
	LockID [LockIDLen]byte `json:"lock_id"`
	// Seq is the monotonically increasing sequence number that makes the lock id
	// unique and the ledger auditable.
	Seq uint64 `json:"seq"`
	// FromAccount is the native account (ed25519 account id) whose MATRIX was
	// locked.
	FromAccount string `json:"from_account"`
	// Recipient is the Ethereum address that will receive the wrapped tokens.
	Recipient Address `json:"recipient"`
	// NativeAmount is the amount of native MATRIX base units (9 decimals) locked.
	NativeAmount uint64 `json:"native_amount"`
}

// ERC20Amount returns the wrapped ERC-20 amount (18 decimals) this lock backs,
// using the single-source-of-truth conversion in token.NativeToERC20.
func (e *LockEvent) ERC20Amount() *big.Int {
	return token.NativeToERC20(e.NativeAmount)
}

// BurnEvent is an unlock authorization originating from the Ethereum side: the
// WrappedMatrix contract burned tokens and emitted the native recipient and
// wrapped amount. The bridge applies each BurnEvent exactly once to release the
// corresponding native MATRIX from escrow.
type BurnEvent struct {
	// ID uniquely identifies the burn (e.g. the Ethereum "chainId:txHash:logIndex"
	// or a burn nonce). It is the replay-protection key: a burn id is applied at
	// most once.
	ID string `json:"id"`
	// ToAccount is the native account (ed25519 account id) that receives the
	// unlocked MATRIX.
	ToAccount string `json:"to_account"`
	// ERC20Amount is the burned wrapped amount in ERC-20 base units (18 decimals).
	// It is converted back to native base units via token.ERC20ToNative and must
	// be an exact multiple of token.ERC20PerNativeUnit.
	ERC20Amount *big.Int `json:"erc20_amount"`
}

// Bridge is the native side of the lock-and-mint bridge. It escrows native
// MATRIX on the shared consensus ledger, records lock/burn events under the
// bridge/* kv namespace, and produces validator attestations for the Ethereum
// contract. All state-mutating operations serialize through the ledger's
// Atomically critical section (for balances) plus an internal mutex (for the
// bridge counters), so concurrent callers observe a consistent locked total.
type Bridge struct {
	ledger *market.Ledger
	store  *kv.Store
	params AttestationParams

	// consensusOrdered makes the unlock half arrive through the consensus engine
	// instead of being applied by whichever node saw the burn.
	//
	// WHY IT IS A MODE AND NOT A CHOICE PER CALL. ProcessBurn takes b.mu and then
	// the ledger critical section; ApplyAttestedUnlock is called from INSIDE the
	// consensus apply path, which already holds the ledger critical section. Two
	// orders over the same two locks is a deadlock waiting for the one run where
	// both paths are live at once, so they are made mutually exclusive here
	// rather than by comment: a consensus-ordered bridge refuses ProcessBurn
	// outright, and a direct bridge refuses ApplyAttestedUnlock.
	consensusOrdered bool

	mu sync.Mutex // guards the read-modify-write of bridge counters/sequence
}

// New builds a Bridge over the given ledger and kv.Store. params binds every
// attestation this bridge produces to a specific chain id and WrappedMatrix
// contract address, so its attestations cannot be replayed against a different
// deployment.
func New(ledger *market.Ledger, store *kv.Store, params AttestationParams) *Bridge {
	return &Bridge{ledger: ledger, store: store, params: params}
}

// NewConsensusOrdered builds a Bridge whose unlock half is applied by the
// consensus engine once a quorum of validators has attested to the burn, rather
// than by whichever node's watcher saw it first.
//
// This is what a validator SET needs. A per-node relayer moves escrowed
// collateral on one node's ledger and nowhere else, so the nodes' escrow
// balances and their 1:1 backing invariant diverge - the same divergence that
// made FundAccount unsafe on a multi-validator network, except this one moves
// real collateral. On such a bridge ProcessBurn is refused; unlocks arrive only
// through ApplyAttestedUnlock.
func NewConsensusOrdered(ledger *market.Ledger, store *kv.Store, params AttestationParams) *Bridge {
	return &Bridge{ledger: ledger, store: store, params: params, consensusOrdered: true}
}

// IsConsensusOrdered reports whether this bridge expects its unlocks through
// consensus.
func (b *Bridge) IsConsensusOrdered() bool { return b.consensusOrdered }

// Params returns the attestation parameters this bridge signs against.
func (b *Bridge) Params() AttestationParams { return b.params }

// readUint64 loads an 8-byte big-endian counter, treating a missing key as zero.
func (b *Bridge) readUint64(key string) (uint64, error) {
	data, err := b.store.Get([]byte(key))
	if err != nil {
		return 0, fmt.Errorf("bridge: read %q: %w", key, err)
	}
	if data == nil {
		return 0, nil
	}
	if len(data) != 8 {
		return 0, fmt.Errorf("bridge: corrupt counter %q: expected 8 bytes, got %d", key, len(data))
	}
	return binary.BigEndian.Uint64(data), nil
}

func encodeU64(v uint64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, v)
	return buf
}

// deriveLockID computes a deterministic, collision-resistant 32-byte lock id
// from the sequence number, sender, recipient, and amount. Using sha256 over a
// length-prefixed encoding keeps ids unique per lock and reproducible for audit.
// DeriveLockID is deriveLockID with the sequence supplied by the caller. The
// consensus-ordered lock passes the transaction's nonce, which every node agrees
// on, instead of the per-node counter Lock uses.
func DeriveLockID(seq uint64, from string, recipient Address, amount uint64) [LockIDLen]byte {
	return deriveLockID(seq, from, recipient, amount)
}

func deriveLockID(seq uint64, from string, recipient Address, amount uint64) [LockIDLen]byte {
	h := sha256.New()
	var u [8]byte
	binary.BigEndian.PutUint64(u[:], seq)
	h.Write(u[:])
	writeLP(h, []byte(from))
	h.Write(recipient[:])
	binary.BigEndian.PutUint64(u[:], amount)
	h.Write(u[:])
	var out [LockIDLen]byte
	copy(out[:], h.Sum(nil))
	return out
}

// writeLP writes a 4-byte big-endian length prefix followed by b.
func writeLP(h interface{ Write([]byte) (int, error) }, b []byte) {
	var lp [4]byte
	binary.BigEndian.PutUint32(lp[:], uint32(len(b)))
	_, _ = h.Write(lp[:])
	_, _ = h.Write(b)
}

// Lock escrows amount native base units from `from` into the bridge escrow
// account and records a LockEvent. The transfer and the counter/sequence/event
// writes all happen under the ledger critical section plus the bridge mutex, so
// the escrow balance, the locked total, and the recorded event stay consistent
// even under concurrent locks. It returns market.ErrInsufficientFunds (leaving
// all state unchanged) if `from` cannot afford the amount.
//
// After Lock returns, call Attest with the returned event to produce the
// validator attestation the Ethereum contract needs to mint.
func (b *Bridge) Lock(from string, recipient Address, amount uint64) (*LockEvent, error) {
	if amount == 0 {
		return nil, ErrZeroAmount
	}
	if from == "" {
		return nil, fmt.Errorf("bridge: lock source account must not be empty: %w", token.ErrInvalidAccountID)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	var ev *LockEvent
	err := b.ledger.Atomically(func(ltx market.LedgerTx) error {
		seq, err := b.readUint64(lockSeqKey)
		if err != nil {
			return err
		}
		locked, err := b.readUint64(lockedTTLKey)
		if err != nil {
			return err
		}
		if locked > (^uint64(0))-amount {
			return fmt.Errorf("bridge: locked total overflow")
		}

		// Move native from the user to escrow (fails atomically if unaffordable).
		if err := ltx.Transfer(from, EscrowAccount, amount); err != nil {
			return fmt.Errorf("bridge: escrow %d from %q: %w", amount, from, err)
		}

		seq++
		lockID := deriveLockID(seq, from, recipient, amount)
		event := &LockEvent{
			LockID:       lockID,
			Seq:          seq,
			FromAccount:  from,
			Recipient:    recipient,
			NativeAmount: amount,
		}
		payload, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("bridge: marshal lock event: %w", err)
		}

		batch := b.store.NewBatch()
		defer batch.Close()
		if err := batch.Set([]byte(lockPrefix+hex.EncodeToString(lockID[:])), payload, nil); err != nil {
			return fmt.Errorf("bridge: stage lock event: %w", err)
		}
		if err := batch.Set([]byte(lockSeqKey), encodeU64(seq), nil); err != nil {
			return fmt.Errorf("bridge: stage lock seq: %w", err)
		}
		if err := batch.Set([]byte(lockedTTLKey), encodeU64(locked+amount), nil); err != nil {
			return fmt.Errorf("bridge: stage locked total: %w", err)
		}
		if err := batch.Commit(pebble.Sync); err != nil {
			return fmt.Errorf("bridge: commit lock: %w", err)
		}
		ev = event
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ev, nil
}

// Attest produces the validator-set-signed attestation for a recorded lock,
// signing the canonical digest over (recipient, wrapped amount, lockId) bound to
// this bridge's chain id and contract. Feed the result into WrappedMatrix.mint.
func (b *Bridge) Attest(ev *LockEvent, signers []ValidatorSigner) (*Attestation, error) {
	if ev == nil {
		return nil, ErrLockNotFound
	}
	return SignAttestation(ev.Recipient, ev.ERC20Amount(), ev.LockID, b.params, signers)
}

// ProcessBurn applies a BurnEvent, releasing the corresponding native MATRIX
// from escrow to the named native account. It is idempotent per burn id: a burn
// id that was already applied returns ErrBurnAlreadyProcessed and changes
// nothing (replay protection). The ERC-20 burn amount is converted to native
// base units via token.ERC20ToNative and must be an exact multiple of the
// conversion factor (otherwise token.ErrConversionOverflow). The escrow debit,
// the unlocked-total increment, and the processed-marker write happen under the
// ledger critical section plus the bridge mutex.
func (b *Bridge) ProcessBurn(burn BurnEvent) error {
	if burn.ID == "" {
		return ErrEmptyID
	}
	if burn.ToAccount == "" {
		return fmt.Errorf("bridge: burn recipient must not be empty: %w", token.ErrInvalidAccountID)
	}
	native, err := token.ERC20ToNative(burn.ERC20Amount)
	if err != nil {
		return err
	}
	if native == 0 {
		return ErrZeroAmount
	}

	if b.consensusOrdered {
		return fmt.Errorf("%w: burn %s must be attested by a quorum and applied through the "+
			"consensus engine, not by this node alone", ErrConsensusOrdered, burn.ID)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	return b.ledger.Atomically(func(ltx market.LedgerTx) error {
		payload, err := json.Marshal(burn)
		if err != nil {
			return fmt.Errorf("bridge: marshal burn event: %w", err)
		}
		return b.releaseLocked(ltx, BurnIDHash(burn.ID), burn.ToAccount, native, payload, burn.ID)
	})
}

// BurnIDHash is the stable, fixed-width identifier a burn is tracked by: the hex
// sha256 of its "txHash:logIndex" id. The consensus-ordered path carries this
// rather than the raw id because it goes in a transaction recipient, which needs
// a bounded, delimiter-free encoding, and because two validators attesting the
// same burn must produce byte-identical recipients or their attestations cannot
// be counted as being about the same thing.
func BurnIDHash(burnID string) string {
	return hex.EncodeToString(sha256Sum([]byte(burnID)))
}

// ApplyAttestedUnlock releases escrowed native MATRIX for a burn that a quorum
// of validators has attested to, using the caller's ledger transaction.
//
// It is called from the consensus apply path, which already holds the ledger
// critical section - so it must NOT open its own, and must not take b.mu either
// (see the consensusOrdered field doc for why mixing the two lock orders is a
// deadlock). That path is single-threaded per node and a consensus-ordered
// bridge refuses ProcessBurn, so nothing else is mutating these counters.
//
// It is idempotent on burnIDHash: an already-released burn returns
// ErrBurnAlreadyProcessed and changes nothing, which is what lets every node
// apply the same committed block and reach the same escrow balance even if the
// attestation quorum is reached twice.
func (b *Bridge) ApplyAttestedUnlock(ltx market.LedgerTx, burnIDHash, toAccount string, native uint64) error {
	if !b.consensusOrdered {
		return fmt.Errorf("%w: this bridge applies burns directly; use ProcessBurn", ErrNotConsensusOrdered)
	}
	if burnIDHash == "" {
		return ErrEmptyID
	}
	if toAccount == "" {
		return fmt.Errorf("bridge: burn recipient must not be empty: %w", token.ErrInvalidAccountID)
	}
	if native == 0 {
		return ErrZeroAmount
	}
	payload, err := json.Marshal(attestedUnlock{
		BurnIDHash:   burnIDHash,
		ToAccount:    toAccount,
		NativeAmount: native,
	})
	if err != nil {
		return fmt.Errorf("bridge: marshal attested unlock: %w", err)
	}
	return b.releaseLocked(ltx, burnIDHash, toAccount, native, payload, burnIDHash)
}

// attestedUnlock is the processed-marker payload the consensus path writes. The
// raw burn id is not available to it (the recipient carries the hash), so the
// record holds exactly what consensus agreed on.
type attestedUnlock struct {
	BurnIDHash   string `json:"burn_id_hash"`
	ToAccount    string `json:"to_account"`
	NativeAmount uint64 `json:"native_amount"`
}

// releaseLocked is the one place escrow is released, shared by the direct and
// the consensus-ordered paths so they cannot drift: the same replay marker, the
// same escrow debit, the same unlocked-total increment, staged in one batch.
// The caller supplies the ledger transaction and whatever serialization it holds.
func (b *Bridge) releaseLocked(ltx market.LedgerTx, burnIDHash, toAccount string, native uint64, marker []byte, idForError string) error {
	burnKey := burnPrefix + burnIDHash
	existing, err := b.store.Get([]byte(burnKey))
	if err != nil {
		return fmt.Errorf("bridge: read burn marker: %w", err)
	}
	if existing != nil {
		return fmt.Errorf("%w: %s", ErrBurnAlreadyProcessed, idForError)
	}

	unlocked, err := b.readUint64(unlockedTTLKey)
	if err != nil {
		return err
	}

	// Release from escrow back to the user (fails if escrow lacks funds, which
	// would indicate a reconciliation violation upstream).
	if err := ltx.Transfer(EscrowAccount, toAccount, native); err != nil {
		return fmt.Errorf("bridge: unlock %d to %q: %w", native, toAccount, err)
	}

	batch := b.store.NewBatch()
	defer batch.Close()
	if err := batch.Set([]byte(burnKey), marker, nil); err != nil {
		return fmt.Errorf("bridge: stage burn marker: %w", err)
	}
	if err := batch.Set([]byte(unlockedTTLKey), encodeU64(unlocked+native), nil); err != nil {
		return fmt.Errorf("bridge: stage unlocked total: %w", err)
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return fmt.Errorf("bridge: commit burn: %w", err)
	}
	return nil
}

func sha256Sum(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}

// LockEvent returns the recorded lock event for a lock id, or ErrLockNotFound.
func (b *Bridge) GetLock(lockID [LockIDLen]byte) (*LockEvent, error) {
	data, err := b.store.Get([]byte(lockPrefix + hex.EncodeToString(lockID[:])))
	if err != nil {
		return nil, fmt.Errorf("bridge: read lock: %w", err)
	}
	if data == nil {
		return nil, ErrLockNotFound
	}
	var ev LockEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, fmt.Errorf("bridge: unmarshal lock: %w", err)
	}
	return &ev, nil
}

// Reconciliation is a snapshot proving the bridge's 1:1 backing invariant.
type Reconciliation struct {
	// LockedNative is the cumulative native base units ever locked.
	LockedNative uint64
	// UnlockedNative is the cumulative native base units ever unlocked (released
	// on burn).
	UnlockedNative uint64
	// OutstandingNative is LockedNative - UnlockedNative: the native currently
	// held in escrow backing the wrapped supply.
	OutstandingNative uint64
	// EscrowBalance is the actual escrow account balance on the ledger. It must
	// equal OutstandingNative.
	EscrowBalance uint64
	// OutstandingERC20 is OutstandingNative converted to ERC-20 base units (18
	// decimals) via token.NativeToERC20; this is the wrapped supply the Ethereum
	// contract must show.
	OutstandingERC20 *big.Int
}

// Reconcile returns the current backing snapshot and verifies the on-ledger
// escrow balance equals the accounting (locked - unlocked). The returned
// OutstandingERC20 is exactly the wrapped ERC-20 total supply the Ethereum
// contract should report if it is correctly backed 1:1. It returns an error if
// the escrow balance and the accounting disagree, which would signal a bug or
// out-of-band tampering.
//
// THE WHOLE SNAPSHOT IS TAKEN IN ONE LEDGER CRITICAL SECTION, because the two
// halves it compares are written by one. A commit moves value into escrow and
// bumps the locked total inside a single Atomically block; reading the counters
// outside it and the escrow balance inside it can straddle that commit and
// report a mismatch that never existed on any node. An operator alarm that fires
// because a lock happened to land mid-read is worse than no alarm.
func (b *Bridge) Reconcile() (*Reconciliation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var locked, unlocked, escrow uint64
	// ReadOnly, not Atomically: this is a snapshot, and it is reachable from an
	// UNAUTHENTICATED GetBridgeReconciliation. Taking the write lock for it let
	// anyone who can poll a read stall every block the node was applying.
	if err := b.ledger.ReadOnly(func(ltx market.LedgerTx) error {
		var err error
		if locked, err = b.readUint64(lockedTTLKey); err != nil {
			return err
		}
		if unlocked, err = b.readUint64(unlockedTTLKey); err != nil {
			return err
		}
		escrow, err = ltx.Balance(EscrowAccount)
		return err
	}); err != nil {
		return nil, err
	}
	if unlocked > locked {
		return nil, fmt.Errorf("bridge: unlocked %d exceeds locked %d", unlocked, locked)
	}
	outstanding := locked - unlocked
	if escrow != outstanding {
		return nil, fmt.Errorf("bridge: reconciliation mismatch: escrow balance %d != outstanding %d", escrow, outstanding)
	}
	return &Reconciliation{
		LockedNative:      locked,
		UnlockedNative:    unlocked,
		OutstandingNative: outstanding,
		EscrowBalance:     escrow,
		OutstandingERC20:  token.NativeToERC20(outstanding),
	}, nil
}

// RecordLock persists a lock the CONSENSUS ENGINE has already applied.
//
// It is Lock's other half, split out because the two now happen in different
// places. Lock does both jobs - move the value into escrow and record the event
// - which was correct while it ran against one node's ledger and wrong the
// moment the escrow move became a committed transaction: consensus does the move
// so that every node makes it, and this records what the chain decided so the
// bridge can attest to it and Reconcile can account for it.
//
// It therefore does NOT transfer anything. An implementation that did would
// double count, moving the value twice out of an account the engine has already
// debited.
//
// IT MUST NOT TAKE A LOCK, AND ltx IS THE PROOF THAT IT NEED NOT. Like
// ApplyAttestedUnlock, this runs from inside the consensus apply path, which
// already holds the ledger critical section; opening a second one re-enters a
// non-reentrant mutex and wedges the node's consensus driver forever, and taking
// b.mu here would invert the order Reconcile and ProcessBurn use (b.mu, then the
// ledger) into a textbook AB-BA deadlock. Demanding the caller's ledger
// transaction is how the contract is stated in the type rather than in a comment
// nobody reads: you cannot call this without already being inside the section.
// The parameter is deliberately unused - the bridge counters live in the kv
// store, not the ledger - and the caller's section is what serializes the
// check-then-write below.
//
// IDEMPOTENT PER LOCK ID, which is what makes a replay safe. A node that
// re-applies a committed block - a crash before the cursor advanced, a resync -
// calls this again with the same id, and the running locked total must not
// climb twice for one lock.
//
// IT IS NOT GATED ON consensusOrdered, unlike ApplyAttestedUnlock, because the
// two halves are not symmetric. The unlock half has a mode: a solo node's
// watcher may release escrow itself, a validator set's may not. The lock half
// has none - since locking became a transaction, EVERY lock on every node
// arrives through the engine - so a solo node runs a direct bridge and still
// records consensus locks through here. Gating this would leave such a node
// escrowing value it never recorded, with nothing to attest to and a
// reconciliation that fails.
func (b *Bridge) RecordLock(ltx market.LedgerTx, lockID [LockIDLen]byte, from string, recipient Address, nativeAmount uint64) error {
	_ = ltx // held for the contract above, not written through
	if nativeAmount == 0 {
		return ErrZeroAmount
	}
	key := lockPrefix + hex.EncodeToString(lockID[:])
	if existing, err := b.store.Get([]byte(key)); err == nil && len(existing) > 0 {
		// Already recorded. Not an error: replaying a committed block is
		// ordinary, and the whole point of keying by lock id is that doing so
		// is free.
		return nil
	}
	locked, err := b.readUint64(lockedTTLKey)
	if err != nil {
		return err
	}
	if locked > (^uint64(0))-nativeAmount {
		return fmt.Errorf("bridge: locked total overflow")
	}
	event := &LockEvent{
		LockID:       lockID,
		FromAccount:  from,
		Recipient:    recipient,
		NativeAmount: nativeAmount,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("bridge: marshal lock event: %w", err)
	}
	batch := b.store.NewBatch()
	defer batch.Close()
	if err := batch.Set([]byte(key), payload, nil); err != nil {
		return fmt.Errorf("bridge: stage lock event: %w", err)
	}
	if err := batch.Set([]byte(lockedTTLKey), encodeU64(locked+nativeAmount), nil); err != nil {
		return fmt.Errorf("bridge: stage locked total: %w", err)
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return fmt.Errorf("bridge: commit recorded lock: %w", err)
	}
	return nil
}

// EscrowAccount reports where locked collateral is held. It is a method as well
// as a constant so the consensus engine can ask the bridge rather than carrying
// its own copy of the name: two places naming the escrow differently would move
// collateral somewhere the backing check does not look.
func (b *Bridge) EscrowAccount() string { return EscrowAccount }
