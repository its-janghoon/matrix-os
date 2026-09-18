package token

import (
	"fmt"
	"strings"

	"github.com/ecirlabs/matrix-core/internal/ethsig"
)

// One account, one id.
//
// THE TRAP THIS CLOSES. An Ethereum-controlled account is keyed by
// "eth:0x<40 lowercase hex>", because EthAccountID lowercases. But lowercase is
// not the form anyone ever SEES: MetaMask, Etherscan, every explorer and every
// block of documentation shows the EIP-55 mixed-case form, because that is the
// form that carries a checksum. So the string a person copies is not the string
// the ledger keys their account by.
//
// A transfer to the copied form is not an error. tx.To is an opaque string in
// the signed payload, and the ledger credits whatever it is handed - so the
// money arrives at a key no private key controls, the transfer reports success,
// and nobody can tell from the outside that anything went wrong. It cannot be
// undone by anyone, at any time, because there is nothing to sign with.
//
// WHY THIS IS A CLIENT RULE AND NOT A CONSENSUS ONE. The recipient is INSIDE
// the signature. A node cannot rewrite it to the canonical form without
// invalidating the signature it just verified, so the only thing consensus
// could do is refuse the transaction - and a refusal rule is a change to block
// validity, which needs a protocol version and an activation height before it
// protects anybody. Canonicalizing before signing needs neither: the signed
// payload carries the right id from the start, every node applies it unchanged,
// and no honest client can build the broken transaction in the first place. A
// consensus refusal is still worth having against a hand-rolled client, and is
// the follow-up rather than the thing that has to land first.
//
// WHY A WRONG CHECKSUM IS REFUSED RATHER THAN LOWERCASED. Lowercasing a
// mistyped address would strand the money just as completely, at a different
// key, while looking like it had been repaired. The checksum is four bits of
// redundancy per character and it exists precisely to catch that, so a
// mixed-case address that fails it is a typo and is reported as one. An
// all-lowercase address carries no checksum to check and is taken at face
// value: it is the machine-to-machine form, it is what every client sends, and
// refusing it would break them all to protect against a form nobody types.

// CanonicalAccountID returns the single id the ledger keys an account by, and
// an error if the id is one no key could control.
//
// Anything that is not an Ethereum-controlled id comes back untouched. An
// ed25519 id is raw hex with a fixed form already, and a reserved recipient -
// "bridge/escrow", "spend/budget/...", "infer/escrow/..." - carries its terms
// in the name, where case is meaning rather than notation.
//
// Call it on anything a person typed, pasted or was shown, BEFORE it is signed
// over or sent: a transfer recipient, a bridge release account, an account to
// look a balance up for.
func CanonicalAccountID(id string) (string, error) {
	trimmed := strings.TrimSpace(id)
	if !IsEthAccountID(trimmed) {
		return trimmed, nil
	}

	body := strings.TrimPrefix(trimmed, EthAccountPrefix)
	addr, err := ethsig.ParseAddress(body)
	if err != nil {
		return "", fmt.Errorf("%q is not an ethereum account id: %w", id, err)
	}

	// Only a mixed-case body carries a checksum, so only a mixed-case body is
	// held to one. Measured on the hex itself rather than the whole string,
	// because "0X" is a prefix spelling and says nothing about the address.
	hexOnly := strings.TrimPrefix(strings.TrimPrefix(body, "0x"), "0X")
	if hexOnly != strings.ToLower(hexOnly) {
		if err := ethsig.VerifyChecksum(body); err != nil {
			return "", fmt.Errorf("%q will not reach anybody: %w", id, err)
		}
	}
	return EthAccountID(addr), nil
}

// MustCanonicalAccountID is CanonicalAccountID for ids this code built itself
// and where a failure would mean a bug rather than bad input. It returns the id
// unchanged when it cannot be canonicalized, so a caller can never be made
// worse off than before by asking.
func MustCanonicalAccountID(id string) string {
	canonical, err := CanonicalAccountID(id)
	if err != nil {
		return id
	}
	return canonical
}
