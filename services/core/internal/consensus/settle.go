package consensus

import (
	"crypto/ed25519"
	"fmt"
	"time"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// SubmitTransfer is the consensus-backed settlement entrypoint. It builds a
// signed token.Transaction transferring amount compute credits from the signer
// (whose key is priv/pub) to recipient, and submits it into consensus. When a
// future block containing the transaction commits, every node deterministically
// reflects the transfer on its market ledger (see Engine.commitAndApply), so
// marketplace settlement now flows through the globally-agreed consensus ledger
// instead of the per-node pairwise path.
//
// Unlike the token chain's per-sender nonce+prev-hash gate (which binds a
// transfer to one node's local chain head), a consensus transfer's PrevHash is
// unused for ledger linkage: ordering and finality come from the committed block
// chain, and replay protection comes from the engine's committed-transaction
// dedup set. The nonce is therefore just a uniquifier that makes otherwise
// identical transfers distinct; callers should pass a monotonically increasing
// value per sender to allow repeated same-amount transfers.
//
// It returns the signed transaction that was submitted so callers can correlate
// it with the committed block (e.g. by matching its signature).
func (e *Engine) SubmitTransfer(pub ed25519.PublicKey, priv ed25519.PrivateKey, recipient string, amount, nonce uint64) (*token.Transaction, error) {
	if len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: public key must be %d bytes", ErrInvalidMessage, ed25519.PublicKeySize)
	}
	if recipient == "" {
		return nil, fmt.Errorf("%w: recipient must not be empty", ErrInvalidMessage)
	}
	tx := &token.Transaction{
		From:      pub,
		To:        recipient,
		Amount:    amount,
		Nonce:     nonce,
		Timestamp: time.Now().UnixNano(),
		// PrevHash is not used for consensus ledger linkage; a stable zero seed
		// keeps the canonical signing bytes well-formed.
		PrevHash: make([]byte, token.HashSize),
	}
	if err := tx.Sign(priv); err != nil {
		return nil, err
	}
	if err := e.Submit(tx); err != nil {
		return nil, err
	}
	return tx, nil
}

// SubmitBond locks amount of the account's own native MATRIX into its bond, by
// submitting a transfer to its reserved bond account. Bonded coins leave the
// spendable balance and are what its voting power is measured in.
//
// It takes effect on the ledger when the block commits, and changes voting
// power at the next epoch boundary - not immediately, because power has to
// change at the same height on every node.
func (e *Engine) SubmitBond(from *token.Account, amount, nonce uint64) (*token.Transaction, error) {
	if from == nil {
		return nil, fmt.Errorf("%w: account is required", ErrInvalidMessage)
	}
	if amount == 0 {
		return nil, fmt.Errorf("%w: a bond must carry a non-zero amount", ErrInvalidMessage)
	}
	return e.SubmitAccountTransfer(from, BondAccount(from.AccountID()), amount, nonce)
}

// SubmitWithdrawBond asks for the account's whole bond back. It is valid only
// once the account is out of the validator set and its unbonding period has
// elapsed, so the transaction may sit in the mempool until then and land by
// itself - which is the intended behaviour, not a failure.
func (e *Engine) SubmitWithdrawBond(from *token.Account, nonce uint64) (*token.Transaction, error) {
	if from == nil {
		return nil, fmt.Errorf("%w: account is required", ErrInvalidMessage)
	}
	return e.SubmitAccountTransfer(from, WithdrawRecipient(from.AccountID()), 0, nonce)
}

// ProviderEmissionAt returns what the pool pays out per block at a height,
// which decays on the configured half-life and reaches zero.
func (e *Engine) ProviderEmissionAt(height uint64) uint64 {
	return EmissionFor(height, e.emissionPerBlock, e.emissionHalfLife)
}

// IsRewardedProvider reports whether an account earns provider rewards.
func (e *Engine) IsRewardedProvider(id string) (bool, error) {
	if e.providers == nil {
		return false, nil
	}
	return e.providers.IsRegistered(id)
}

// RewardedProviders lists the accounts that earn provider rewards.
func (e *Engine) RewardedProviders() ([]string, error) {
	if e.providers == nil {
		return nil, nil
	}
	return e.providers.All()
}

// RewardPoolBalance returns what is left of the genesis pool the provider
// emission is paid from.
func (e *Engine) RewardPoolBalance() (uint64, error) {
	if e.ledger == nil {
		return 0, nil
	}
	return e.ledger.Balance(rewardPoolAccount)
}

// BondedStake returns how much the given account has bonded, and zero on a
// network without stake.
func (e *Engine) BondedStake(id string) (uint64, error) {
	if e.stake == nil {
		return 0, nil
	}
	return e.stake.Bonded(id)
}

// BondWithdrawableAt returns the height from which an account may take its bond
// back, and whether it may at all (a sitting validator may not, at any height).
//
// Exposed because the height is as much of a bond as the amount. Capital that
// can be pulled in the next block is committed to nothing, so a buyer weighing a
// seller's stake needs both numbers or the first one flatters.
func (e *Engine) BondWithdrawableAt(id string) (uint64, bool, error) {
	if e.stake == nil {
		return 0, false, nil
	}
	return e.stake.WithdrawableAt(id, e.vset(), e.unbondingPeriod, e.bondResidency)
}

// MinBond is the stake required before an account may be admitted to the
// validator set. Zero means membership costs nothing.
func (e *Engine) MinBond() uint64 { return e.minBond }

// UnbondingPeriod is how many blocks after leaving the set an account waits
// before it may withdraw its bond.
func (e *Engine) UnbondingPeriod() uint64 { return e.unbondingPeriod }

// SubmitAccountTransfer is a convenience wrapper over SubmitTransfer for a
// token.Account (which carries both key halves).
func (e *Engine) SubmitAccountTransfer(from *token.Account, recipient string, amount, nonce uint64) (*token.Transaction, error) {
	if from == nil {
		return nil, fmt.Errorf("%w: account is required", ErrInvalidMessage)
	}
	return e.SubmitTransfer(from.PublicKey, from.PrivateKey, recipient, amount, nonce)
}
