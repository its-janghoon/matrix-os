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

	mu sync.Mutex // guards the read-modify-write of bridge counters/sequence
}

// New builds a Bridge over the given ledger and kv.Store. params binds every
// attestation this bridge produces to a specific chain id and WrappedMatrix
// contract address, so its attestations cannot be replayed against a different
// deployment.
func New(ledger *market.Ledger, store *kv.Store, params AttestationParams) *Bridge {
	return &Bridge{ledger: ledger, store: store, params: params}
}

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

	b.mu.Lock()
	defer b.mu.Unlock()

	burnKey := burnPrefix + hex.EncodeToString(sha256Sum([]byte(burn.ID)))
	return b.ledger.Atomically(func(ltx market.LedgerTx) error {
		existing, err := b.store.Get([]byte(burnKey))
		if err != nil {
			return fmt.Errorf("bridge: read burn marker: %w", err)
		}
		if existing != nil {
			return fmt.Errorf("%w: %s", ErrBurnAlreadyProcessed, burn.ID)
		}

		unlocked, err := b.readUint64(unlockedTTLKey)
		if err != nil {
			return err
		}

		// Release from escrow back to the user (fails if escrow lacks funds,
		// which would indicate a reconciliation violation upstream).
		if err := ltx.Transfer(EscrowAccount, burn.ToAccount, native); err != nil {
			return fmt.Errorf("bridge: unlock %d to %q: %w", native, burn.ToAccount, err)
		}

		payload, err := json.Marshal(burn)
		if err != nil {
			return fmt.Errorf("bridge: marshal burn event: %w", err)
		}

		batch := b.store.NewBatch()
		defer batch.Close()
		if err := batch.Set([]byte(burnKey), payload, nil); err != nil {
			return fmt.Errorf("bridge: stage burn marker: %w", err)
		}
		if err := batch.Set([]byte(unlockedTTLKey), encodeU64(unlocked+native), nil); err != nil {
			return fmt.Errorf("bridge: stage unlocked total: %w", err)
		}
		if err := batch.Commit(pebble.Sync); err != nil {
			return fmt.Errorf("bridge: commit burn: %w", err)
		}
		return nil
	})
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
func (b *Bridge) Reconcile() (*Reconciliation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	locked, err := b.readUint64(lockedTTLKey)
	if err != nil {
		return nil, err
	}
	unlocked, err := b.readUint64(unlockedTTLKey)
	if err != nil {
		return nil, err
	}
	if unlocked > locked {
		return nil, fmt.Errorf("bridge: unlocked %d exceeds locked %d", unlocked, locked)
	}
	outstanding := locked - unlocked

	escrow, err := b.ledger.Balance(EscrowAccount)
	if err != nil {
		return nil, err
	}
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
