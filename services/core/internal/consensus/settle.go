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

// SubmitAccountTransfer is a convenience wrapper over SubmitTransfer for a
// token.Account (which carries both key halves).
func (e *Engine) SubmitAccountTransfer(from *token.Account, recipient string, amount, nonce uint64) (*token.Transaction, error) {
	if from == nil {
		return nil, fmt.Errorf("%w: account is required", ErrInvalidMessage)
	}
	return e.SubmitTransfer(from.PublicKey, from.PrivateKey, recipient, amount, nonce)
}
