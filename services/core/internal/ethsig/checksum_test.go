package ethsig

import (
	"strings"
	"testing"
)

// TestChecksumVectors checks the addresses published with EIP-55. They include
// the two awkward cases a naive implementation gets wrong: an address whose
// checksummed form is entirely uppercase, and one whose form is entirely
// lowercase because keccak happened to produce no high nibble at any letter.
func TestChecksumVectors(t *testing.T) {
	for _, want := range []string{
		// Mixed case, the ordinary outcome.
		"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
		"0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359",
		"0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB",
		"0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb",
		// All uppercase.
		"0x52908400098527886E0F7030069857D2E4169EE7",
		"0x8617E340B3D01FA5F11F306F4090FD50E238070D",
		// All lowercase. A rule that treated a lowercase input as "carries no
		// checksum" would refuse to verify these perfectly valid addresses.
		"0xde709f2102306220921060314715629080e2fb77",
		"0x27b1fdb04752bbc536007a920d24acb045561c26",
	} {
		t.Run(want, func(t *testing.T) {
			addr, err := ParseAddress(strings.ToLower(want))
			if err != nil {
				t.Fatalf("ParseAddress: %v", err)
			}
			if got := addr.ChecksumHex(); got != want {
				t.Fatalf("ChecksumHex = %s, want %s", got, want)
			}
			if err := VerifyChecksum(want); err != nil {
				t.Fatalf("VerifyChecksum(%s): %v", want, err)
			}
			if _, err := ParseChecksumAddress(want); err != nil {
				t.Fatalf("ParseChecksumAddress(%s): %v", want, err)
			}
		})
	}
}

// TestChecksumCatchesASingleCharacterTypo is the whole reason this exists. The
// bare-hex account id it replaces accepted this silently, and the funds were
// then held by an account no key controls.
func TestChecksumCatchesASingleCharacterTypo(t *testing.T) {
	const good = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"

	// One hex digit changed. The result is still 40 valid hex characters, so
	// only the checksum can tell it is wrong.
	typo := "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAe4"
	if _, err := ParseChecksumAddress(typo); err == nil {
		t.Fatal("a single mistyped character must not parse as a checksummed address")
	}
	if _, err := ParseAddress(typo); err != nil {
		t.Fatalf("the typo is still valid hex, so ParseAddress should accept it: %v", err)
	}

	// One character's CASE changed. Same length, same bytes, broken checksum.
	caseTypo := "0x5aaeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
	if _, err := ParseChecksumAddress(caseTypo); err == nil {
		t.Fatal("a flipped case must not parse as a checksummed address")
	}
	if _, err := ParseChecksumAddress(good); err != nil {
		t.Fatalf("the correct address should parse: %v", err)
	}
}

// TestUnchecksummedInputIsRefusedWhereAChecksumIsRequired covers the common
// paste: an address copied from a log or an RPC response, which is lowercase.
// It must not pass a parser whose job is to catch typos, unless lowercase IS
// this address's checksummed form.
func TestUnchecksummedInputIsRefusedWhereAChecksumIsRequired(t *testing.T) {
	const mixed = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
	if _, err := ParseChecksumAddress(strings.ToLower(mixed)); err == nil {
		t.Fatal("a lowercase form of a mixed-case address must be refused")
	}
	if _, err := ParseChecksumAddress(strings.ToUpper(mixed[2:])); err == nil {
		t.Fatal("an uppercase form of a mixed-case address must be refused")
	}
	// The machine-facing parser still accepts it, which is what JSON-RPC needs.
	if _, err := ParseAddress(strings.ToLower(mixed)); err != nil {
		t.Fatalf("ParseAddress should accept lowercase: %v", err)
	}
}

// TestChecksumRoundTripsFromAKey proves the display form of a freshly derived
// address is itself checksummed, so nothing in the node prints an address a
// strict parser would then reject.
func TestChecksumRoundTripsFromAKey(t *testing.T) {
	for i := 0; i < 64; i++ {
		var addr Address
		addr[0] = byte(i)
		addr[19] = byte(i * 7)
		printed := addr.ChecksumHex()
		parsed, err := ParseChecksumAddress(printed)
		if err != nil {
			t.Fatalf("ParseChecksumAddress(%s): %v", printed, err)
		}
		if parsed != addr {
			t.Fatalf("round trip changed the address: %s -> %s", addr.Hex(), parsed.Hex())
		}
	}
}
