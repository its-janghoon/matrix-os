package consensus

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// The unlock half of the bridge, made consensus-ordered.
//
// THE PROBLEM. The Ethereum side burns wrapped MATRIX and emits a Burned event;
// the native side must release the same value from escrow. Observing that event
// requires an Ethereum endpoint, so it was done by a per-node watcher that
// applied the release to its own ledger and nowhere else. On a validator set
// that splits the escrow balance and the 1:1 backing invariant across nodes -
// the same divergence that made FundAccount unsafe, except this one moves real
// collateral. The bridge's own doc comment admitted it: "What the watcher is NOT
// is consensus."
//
// THE SHAPE OF THE FIX. It cannot be "check it at apply time" the way
// affordability is, because a node with no Ethereum endpoint cannot verify a
// burn at all, and consensus may only depend on committed state. So the burn is
// turned into something the chain CAN agree on: an attestation.
//
// Each validator that watches Ethereum submits a transaction to a reserved
// recipient that encodes the burn. The recipient is a pure function of the burn,
// so two validators attesting the same burn produce byte-identical recipients
// and their attestations are countable as being about one thing. The engine
// counts attesting VOTING POWER from committed blocks only, and releases escrow
// on the block where the count crosses quorum. Every node replaying those blocks
// reaches the same release at the same height, whether or not it has ever seen
// an Ethereum node.
//
// This mirrors the lock half, which already required a threshold of validator
// signatures before the Ethereum contract would mint. Until now only one
// direction was quorum-gated.
//
// WHAT IT DOES NOT DO. It does not make one validator's word cheaper to forge
// than the quorum: a single attestation releases nothing. It does not verify
// that the burn happened - it verifies that a quorum of the set says so, which
// is the same trust assumption the mint half has always had.
const (
	// burnUnlockPrefix namespaces the reserved recipient. It shares the
	// "bridge/" namespace with bridge.EscrowAccount so bridge-related reserved
	// ids are recognizable, and it can never collide with a real account id
	// (those are 64 lowercase hex characters with no slash).
	burnUnlockPrefix = "bridge/unlock/"
)

// BurnUnlock is the burn a validator attests to. Every field is carried in the
// transaction recipient, so all attestations for one burn are byte-identical and
// every node can apply the release from the committed block alone.
type BurnUnlock struct {
	// BurnIDHash is the hex sha256 of the burn's "txHash:logIndex" id, which is
	// what the bridge tracks its replay marker by (bridge.BurnIDHash).
	BurnIDHash string
	// ToAccount is the native account the released MATRIX goes to.
	ToAccount string
	// NativeAmount is the amount to release, in native base units.
	NativeAmount uint64
}

// Recipient renders the reserved recipient that encodes this unlock. It is the
// attestation's identity: same burn, same string, on every node.
func (u BurnUnlock) Recipient() string {
	return fmt.Sprintf("%s%s/%s/%d", burnUnlockPrefix, u.BurnIDHash, u.ToAccount, u.NativeAmount)
}

// IsBurnUnlockRecipient reports whether a recipient encodes a burn unlock
// attestation, so the apply path can tell it from an ordinary transfer.
func IsBurnUnlockRecipient(to string) bool {
	return strings.HasPrefix(to, burnUnlockPrefix)
}

// ParseBurnUnlock decodes an unlock from a transaction recipient. Anything
// malformed is an error rather than a best guess, so a transfer to a lookalike
// account can never be mistaken for an attestation - and because the recipient
// is the attestation's identity, a decoder that accepted sloppy input would let
// two spellings of one burn split the quorum.
func ParseBurnUnlock(to string) (BurnUnlock, error) {
	if !IsBurnUnlockRecipient(to) {
		return BurnUnlock{}, fmt.Errorf("%w: %q is not a burn unlock", ErrInvalidMessage, to)
	}
	rest := strings.TrimPrefix(to, burnUnlockPrefix)
	parts := strings.Split(rest, "/")
	if len(parts) != 3 {
		return BurnUnlock{}, fmt.Errorf("%w: burn unlock must be <burn id hash>/<account>/<amount>, got %q",
			ErrInvalidMessage, rest)
	}
	idHash, account, amountStr := parts[0], parts[1], parts[2]

	// A fixed-width lowercase hex sha256. Fixed width matters: a variable-length
	// id would let two spellings of the same burn exist.
	raw, err := hex.DecodeString(idHash)
	if err != nil || len(raw) != 32 || idHash != strings.ToLower(idHash) {
		return BurnUnlock{}, fmt.Errorf("%w: burn id must be a 64-lowercase-hex sha256, got %q",
			ErrInvalidMessage, idHash)
	}
	if _, err := token.ParsePublicKeyHex(account); err != nil {
		return BurnUnlock{}, fmt.Errorf("%w: burn unlock recipient %q is not an account id: %v",
			ErrInvalidMessage, account, err)
	}
	// Canonical decimal only: no sign, no leading zero, no underscores. Two
	// spellings of one amount would again split the quorum.
	if amountStr == "" || (len(amountStr) > 1 && amountStr[0] == '0') {
		return BurnUnlock{}, fmt.Errorf("%w: burn amount %q is not canonical decimal", ErrInvalidMessage, amountStr)
	}
	amount, err := strconv.ParseUint(amountStr, 10, 64)
	if err != nil {
		return BurnUnlock{}, fmt.Errorf("%w: burn amount %q: %v", ErrInvalidMessage, amountStr, err)
	}
	if amount == 0 {
		return BurnUnlock{}, fmt.Errorf("%w: burn amount must be positive", ErrInvalidMessage)
	}
	return BurnUnlock{BurnIDHash: idHash, ToAccount: account, NativeAmount: amount}, nil
}

// ErrBurnAlreadyReleased is what a BurnUnlocker returns (wrapped) when the burn
// has already been released. It is declared here, not in internal/bridge,
// because it is part of the interface CONTRACT: consensus has to tell the
// ordinary case of a late attestation apart from a genuine failure without
// importing the bridge or matching on error text. The adapter that wires a real
// bridge in translates the bridge's own sentinel into this one.
var ErrBurnAlreadyReleased = errors.New("consensus: burn already released")

// BurnUnlocker is the bridge, as consensus needs it: a way to release escrow
// inside the caller's ledger transaction. It is an interface so this package
// does not depend on internal/bridge, and so a test can count releases without
// a bridge at all.
type BurnUnlocker interface {
	// ApplyAttestedUnlock releases native from escrow using ltx. It must not
	// open its own ledger transaction - the caller already holds the critical
	// section. It must be idempotent per burnIDHash, returning an error wrapping
	// ErrBurnAlreadyReleased for a burn already released, because a node that
	// applies the same committed block twice has to reach the same escrow
	// balance rather than release twice or wedge.
	ApplyAttestedUnlock(ltx market.LedgerTx, burnIDHash, toAccount string, native uint64) error
}

// verifyBurnUnlockLocked decides whether a burn unlock attestation may be in a
// block. Callers must hold e.mu.
//
// Only a member of the set in force may attest: an outsider's attestation would
// otherwise sit in the tally forever, and a set of n could be pushed over
// quorum by n strangers. It deliberately does NOT reject an attestation for an
// already-released burn - a validator that was slow, or replaying, submits one
// honestly, and rejecting the whole block for it would let one late validator
// stall the chain. The release itself is idempotent, so a late attestation is a
// no-op.
func (e *Engine) verifyBurnUnlockLocked(tx *token.Transaction) error {
	if _, err := ParseBurnUnlock(tx.To); err != nil {
		return err
	}
	if tx.Amount != 0 {
		return fmt.Errorf("%w: a burn unlock attestation carries no value, got %d", ErrInvalidMessage, tx.Amount)
	}
	if !e.vset().Contains(tx.SenderID()) {
		return fmt.Errorf("%w: %s is not in the validator set", ErrNotValidator, tx.SenderID())
	}
	return nil
}

// recordBurnAttestationLocked adds one validator's attestation to a burn's tally
// and reports the attesting voting power afterwards.
//
// The "Locked" suffix follows this package's convention for "the caller has
// arranged exclusion". Here that is not e.mu: it is either the startup
// rehydration, which runs before the engine starts, or the single-threaded
// block-apply path. See applyBurnAttestation for why e.mu is deliberately not
// taken under the ledger lock.
//
// The tally is keyed by the RECIPIENT string, not by the burn id hash alone, so
// two attestations that disagree about the account or the amount are two
// different tallies and neither borrows the other's power. A quorum has to agree
// on where the money goes, not merely that something was burned.
func (e *Engine) recordBurnAttestationLocked(recipient, validatorID string) uint64 {
	byValidator := e.burnAttestations[recipient]
	if byValidator == nil {
		byValidator = make(map[string]struct{}, 4)
		e.burnAttestations[recipient] = byValidator
	}
	byValidator[validatorID] = struct{}{}
	return e.vset().PowerOfSet(byValidator)
}

// applyBurnAttestation tallies one attestation and, on the block where the
// attesting power crosses quorum, releases the escrow. It returns the amount
// released (zero when quorum is not yet reached, when it was already released,
// or when this node has no bridge).
//
// Determinism. The tally comes only from committed blocks and the quorum
// threshold comes from the set in force at this height, so every node crosses
// the threshold on the same block. A node with no bridge still tallies, so it
// stays in step and starts releasing correctly if a bridge is configured later
// and it replays.
//
// A release that fails is not a reason to reject the block: the attestations are
// already agreed and the block is already committed. An already-processed burn
// is the ordinary case (a late attestation) and is silent; anything else is
// reported and the block still applies, because failing here would wedge the
// node against a chain its peers accepted.
func (e *Engine) applyBurnAttestation(ltx market.LedgerTx, tx *token.Transaction) error {
	unlock, err := ParseBurnUnlock(tx.To)
	if err != nil {
		// Block validation already rejected malformed recipients, so reaching
		// here means a block was committed that should not have been. Do not
		// release anything and do not wedge: report it.
		fmt.Printf("consensus: committed block carries an unparseable burn unlock %q: %v\n", tx.To, err)
		return nil
	}

	// No e.mu here, deliberately. This runs inside the ledger critical section,
	// and taking e.mu under the ledger lock would establish a second lock order
	// over the same two locks - the kind of thing that is fine until the one run
	// where both orders are live. It is safe to omit because burnAttestations is
	// touched in exactly two places: the startup rehydration, which finishes
	// before the engine runs, and this block-apply path, which is single-threaded
	// per node. e.vset() is an atomic pointer load and needs no lock either.
	power := e.recordBurnAttestationLocked(tx.To, tx.SenderID())
	vs := e.vset()
	quorum, total := vs.QuorumPower(), vs.TotalPower()

	if power < quorum {
		fmt.Printf("consensus: burn unlock %s attested by %d of %d voting power (need %d)\n",
			unlock.BurnIDHash, power, total, quorum)
		return nil
	}
	if e.burnUnlocker == nil {
		return nil
	}

	if err := e.burnUnlocker.ApplyAttestedUnlock(ltx, unlock.BurnIDHash, unlock.ToAccount, unlock.NativeAmount); err != nil {
		if errors.Is(err, ErrBurnAlreadyReleased) {
			// A late attestation on a burn already released. Expected.
			return nil
		}
		fmt.Printf("consensus: burn unlock %s reached quorum but the release failed: %v\n",
			unlock.BurnIDHash, err)
		return nil
	}
	fmt.Printf("consensus: released %d native base units from bridge escrow to %s "+
		"(burn %s, attested by %d of %d voting power)\n",
		unlock.NativeAmount, unlock.ToAccount, unlock.BurnIDHash, power, total)
	return nil
}

// SubmitBurnAttestation submits this node's attestation that a burn happened, so
// the escrow release can be applied by consensus once a quorum agrees.
//
// It is what a bridge watcher calls instead of applying the release itself. The
// transaction carries no value: the recipient encodes the whole claim, and the
// release comes out of escrow when the tally crosses quorum, not from this
// transaction.
//
// A node that is not in the validator set gets a clear refusal rather than a
// transaction its peers will reject: only a member's attestation counts, and one
// that cannot count would sit in the mempool being re-proposed.
func (e *Engine) SubmitBurnAttestation(unlock BurnUnlock) (*token.Transaction, error) {
	if e.self == nil {
		return nil, fmt.Errorf("%w: this node has no consensus identity to attest with", ErrInvalidMessage)
	}
	if unlock.NativeAmount == 0 {
		return nil, fmt.Errorf("%w: burn amount must be positive", ErrInvalidMessage)
	}
	recipient := unlock.Recipient()
	// Round-trip the recipient before submitting. It is the attestation's
	// identity, so a spelling this node's own parser would reject is one that
	// could never be counted, and finding that out here beats finding it out as
	// a rejected block.
	if _, err := ParseBurnUnlock(recipient); err != nil {
		return nil, err
	}
	if !e.vset().Contains(e.selfID) {
		return nil, fmt.Errorf("%w: only a validator's burn attestation counts toward quorum", ErrNotValidator)
	}

	e.mu.Lock()
	nonce := e.burnAttestNonce
	e.burnAttestNonce++
	e.mu.Unlock()

	return e.SubmitAccountTransfer(e.self, recipient, 0, nonce)
}
