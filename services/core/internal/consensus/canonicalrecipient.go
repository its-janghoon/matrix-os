package consensus

import (
	"github.com/ecirlabs/matrix-core/internal/token"
)

// Refusing a recipient that names an account nobody can spend from.
//
// An `eth:` account is keyed by the lowercase address, but every wallet and
// explorer DISPLAYS the EIP-55 mixed-case form, so the string a person copies is
// not the string the ledger keys their account by. A transfer to the copied form
// succeeds, credits a key no private key controls, and cannot be undone by
// anyone at any time. PR #23 closed every client path by canonicalizing before
// signing; this closes the client nobody in this repository wrote.
//
// THE CHAIN CANNOT REPAIR IT, ONLY REFUSE IT. The recipient is inside the signed
// payload. Rewriting tx.To to the canonical form would invalidate the signature
// the node just verified, so "credit the right account instead" is not available
// - the options are to credit the stranded id or to reject the transaction.
//
// WHICH IS WHY IT NEEDS A HEIGHT. Rejecting changes what blocks are valid, and a
// node applying the rule while its peers do not refuses to vote for blocks they
// commit. Gated on ProtocolVersionCanonicalEthRecipient, the whole network starts
// refusing at the same height or not at all.
//
// The rule is not restated here. It is token.RequireCanonicalEthRecipient, which
// is CanonicalAccountID read backwards, so the chain and the client cannot come
// to different conclusions about which account a string names.

// verifyCanonicalRecipientLocked refuses a transaction whose recipient names an
// Ethereum-controlled account in a form the ledger does not key it by, once the
// rules at that height have this version in them. Callers must hold e.mu.
//
// Height, not e.height, because a block is validated against the rules of ITS
// OWN height: a node catching up must reach the same verdict on an old block as
// the network did when it committed it.
func (e *Engine) verifyCanonicalRecipientLocked(tx *token.Transaction, height uint64) error {
	if v := e.protocolVersionAt(height); v < ProtocolVersionCanonicalEthRecipient {
		return nil
	}
	return token.RequireCanonicalEthRecipient(tx.To)
}

// rejectNonCanonicalRecipient is the submit-time half, so a sender is TOLD their
// transfer will not be included rather than watching it sit in a mempool.
//
// Unlike a budget before its activation, this refusal is permanent in the
// direction that matters: a non-canonical recipient does not become valid at a
// later height, it becomes invalid at one. So refusing at submit costs the sender
// nothing and saves them a transfer that silently never lands.
//
// It reads the height being built, the same one buildProposalLocked selects
// against, so submit and proposal agree about which rules apply. A transaction
// submitted in the last block before activation is still refused by block
// validation if it is not included in time - submit is the courtesy, not the
// guarantee.
func (e *Engine) rejectNonCanonicalRecipient(tx *token.Transaction) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.verifyCanonicalRecipientLocked(tx, e.height)
}
