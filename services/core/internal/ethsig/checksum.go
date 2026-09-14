package ethsig

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// EIP-55 checksummed addresses: the typo protection a bare hex account id does
// not have.
//
// The native account id this replaces was raw lowercase hex with no redundancy,
// so a single mistyped character produced another perfectly valid id. Funds sent
// there are not recoverable by anyone, because no key controls that account and
// nothing in the protocol can tell the mistake from an intended transfer.
//
// EIP-55 spends no extra characters on the fix. It carries four bits of
// redundancy per character in the CASE of the hex digits: uppercase a letter
// when the corresponding nibble of keccak256(lowercase address) is 8 or above.
// Every Ethereum wallet, explorer and exchange already validates this, so
// adopting it also means their existing input checks protect our users.

// ChecksumHex returns the EIP-55 mixed-case form of the address. It is the form
// to display, log, and put in front of a person.
func (a Address) ChecksumHex() string {
	lower := hex.EncodeToString(a[:])
	digest := Keccak256([]byte(lower))

	out := make([]byte, 0, len(lower)+2)
	out = append(out, '0', 'x')
	for i := 0; i < len(lower); i++ {
		c := lower[i]
		if c >= 'a' && c <= 'f' {
			// Nibble i of the digest: the high half for an even index, the low
			// half for an odd one.
			nibble := digest[i/2]
			if i%2 == 0 {
				nibble >>= 4
			} else {
				nibble &= 0x0f
			}
			if nibble >= 8 {
				c -= 'a' - 'A'
			}
		}
		out = append(out, c)
	}
	return string(out)
}

// VerifyChecksum reports whether s is exactly the EIP-55 form of the address it
// encodes, comparing case-sensitively against the computed form.
//
// Exact equality rather than "mixed case looks right" is deliberate. Some
// addresses have an all-lowercase checksummed form - keccak simply produces no
// nibble above 7 at any letter position - and a rule that skipped the check for
// a lowercase input would both fail to verify those and quietly accept an
// unchecksummed address as verified. Comparing against the computed string gets
// every case right with one comparison.
func VerifyChecksum(s string) error {
	addr, err := ParseAddress(s)
	if err != nil {
		return err
	}
	want := addr.ChecksumHex()
	got := strings.TrimSpace(s)
	if !strings.HasPrefix(got, "0x") && !strings.HasPrefix(got, "0X") {
		got = "0x" + got
	}
	if got != want {
		return fmt.Errorf("%w: %s is not the checksummed form; it should be %s", ErrInvalidAddress, got, want)
	}
	return nil
}

// ParseChecksumAddress parses an address and REQUIRES its EIP-55 checksum.
//
// This is the parser for anywhere a person types or pastes a destination: a
// deposit address, a transfer recipient, a payout account. Accepting an
// unchecksummed address there gives back exactly the typo exposure the checksum
// exists to remove. Machine-to-machine input - JSON-RPC parameters, where every
// client sends lowercase - goes through ParseAddress instead.
func ParseChecksumAddress(s string) (Address, error) {
	var zero Address
	addr, err := ParseAddress(s)
	if err != nil {
		return zero, err
	}
	if err := VerifyChecksum(s); err != nil {
		return zero, err
	}
	return addr, nil
}
