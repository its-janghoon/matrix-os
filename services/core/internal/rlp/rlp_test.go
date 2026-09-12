package rlp

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"testing"
)

// mustHex decodes a hex literal used as an expected encoding.
func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex literal %q: %v", s, err)
	}
	return b
}

// TestCanonicalVectors checks the encodings published with the specification.
// They are the ground truth for this package: an encoder that agrees with them
// agrees with every Ethereum client, and one that does not produces signatures
// nobody can verify.
func TestCanonicalVectors(t *testing.T) {
	lorem := "Lorem ipsum dolor sit amet, consectetur adipisicing elit"

	for _, tc := range []struct {
		name string
		got  []byte
		want string
	}{
		{"the string dog", EncodeBytes([]byte("dog")), "83646f67"},
		{"the empty string", EncodeBytes(nil), "80"},
		{"the empty list", EncodeList(), "c0"},
		{"the zero byte", EncodeBytes([]byte{0x00}), "00"},
		{"the byte 0x0f", EncodeBytes([]byte{0x0f}), "0f"},
		{"the two bytes 0x0400", EncodeBytes([]byte{0x04, 0x00}), "820400"},
		{"the integer zero", EncodeUint(0), "80"},
		{"the integer 15", EncodeUint(15), "0f"},
		{"the integer 1024", EncodeUint(1024), "820400"},
		{"cat and dog", EncodeList(EncodeBytes([]byte("cat")), EncodeBytes([]byte("dog"))), "c88363617483646f67"},
		// 56 bytes is the first length that needs the long form, so it is the
		// boundary the two encodings meet at.
		{"a 56-byte string", EncodeBytes([]byte(lorem)), "b838" + hex.EncodeToString([]byte(lorem))},
		// The set-theoretical three, which is the only vector that exercises
		// nested lists.
		{
			"nested empty lists",
			EncodeList(EncodeList(), EncodeList(EncodeList()), EncodeList(EncodeList(), EncodeList(EncodeList()))),
			"c7c0c1c0c3c0c1c0",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hex.EncodeToString(tc.got); got != tc.want {
				t.Fatalf("encoding = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestDecodeRoundTrip checks that decoding inverts encoding for the shapes a
// transaction uses, including the long forms.
func TestDecodeRoundTrip(t *testing.T) {
	long := bytes.Repeat([]byte{0xab}, 500)

	encoded := EncodeList(
		EncodeUint(9),
		EncodeUint(20_000_000_000),
		EncodeBytes(mustHex(t, "3535353535353535353535353535353535353535")),
		EncodeBytes(nil),
		EncodeBytes(long),
		EncodeList(EncodeUint(1), EncodeUint(0)),
	)

	item, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !item.IsList() || len(item.List) != 6 {
		t.Fatalf("decoded %d elements (list=%v), want a list of 6", len(item.List), item.IsList())
	}
	if v, err := item.List[0].Uint(); err != nil || v != 9 {
		t.Fatalf("element 0 = %d, %v; want 9", v, err)
	}
	if v, err := item.List[1].Uint(); err != nil || v != 20_000_000_000 {
		t.Fatalf("element 1 = %d, %v; want 20000000000", v, err)
	}
	if len(item.List[3].Bytes) != 0 || item.List[3].IsList() {
		t.Fatal("element 3 should decode as the empty string")
	}
	if !bytes.Equal(item.List[4].Bytes, long) {
		t.Fatal("the 500-byte element did not survive the round trip")
	}
	if !item.List[5].IsList() || len(item.List[5].List) != 2 {
		t.Fatal("element 5 should decode as a nested list of 2")
	}
}

// TestEmptyListAndEmptyStringStayDistinct pins the one ambiguity a naive
// decoder introduces. Both have an empty payload and they mean different
// things; a transaction's `to` field being one or the other is the difference
// between a transfer and a contract creation.
func TestEmptyListAndEmptyStringStayDistinct(t *testing.T) {
	str, err := Decode(EncodeBytes(nil))
	if err != nil {
		t.Fatalf("decode empty string: %v", err)
	}
	list, err := Decode(EncodeList())
	if err != nil {
		t.Fatalf("decode empty list: %v", err)
	}
	if str.IsList() {
		t.Fatal("the empty string decoded as a list")
	}
	if !list.IsList() {
		t.Fatal("the empty list decoded as a string")
	}
}

// TestDecodeRejectsNonCanonicalInput covers the encodings that are structurally
// readable but are a SECOND spelling of a value that already has one. Accepting
// any of them would mean one transaction has two encodings and therefore two
// hashes, which is the property a signature depends on not existing.
func TestDecodeRejectsNonCanonicalInput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
	}{
		{"a small byte in long form", "8105"},
		{"a short payload in long form", "b8056c6f72656d"},
		{"a length with a leading zero", "b900056c6f72656d"},
		{"trailing bytes after the item", "83646f6700"},
		{"a truncated string", "83646f"},
		{"a truncated list", "c883646f67"},
		{"a truncated length prefix", "b9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode(mustHex(t, tc.input)); !errors.Is(err, ErrDecode) {
				t.Fatalf("Decode(%s) = %v, want ErrDecode", tc.input, err)
			}
		})
	}
}

// TestIntegersRejectLeadingZeros is the same canonicality rule at the value
// level: 0x0001 and 0x01 are the same number and only one is a valid encoding.
func TestIntegersRejectLeadingZeros(t *testing.T) {
	item, err := Decode(mustHex(t, "820001"))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if _, err := item.Uint(); !errors.Is(err, ErrDecode) {
		t.Fatalf("Uint on a leading-zero integer = %v, want ErrDecode", err)
	}
	if _, err := item.Big(); !errors.Is(err, ErrDecode) {
		t.Fatalf("Big on a leading-zero integer = %v, want ErrDecode", err)
	}
}

// TestEncodeBigRejectsNegative guards the one input RLP has no representation
// for. Encoding the magnitude would silently produce a payload that verifies
// against a different number than the caller meant.
func TestEncodeBigRejectsNegative(t *testing.T) {
	if _, err := EncodeBig(big.NewInt(-1)); err == nil {
		t.Fatal("EncodeBig(-1) should fail")
	}
	got, err := EncodeBig(nil)
	if err != nil {
		t.Fatalf("EncodeBig(nil): %v", err)
	}
	if hex.EncodeToString(got) != "80" {
		t.Fatalf("EncodeBig(nil) = %s, want 80", hex.EncodeToString(got))
	}
}

// TestDecodeRejectsAnOversizedLengthPrefix stops a hostile payload from turning
// a length field into an allocation.
func TestDecodeRejectsAnOversizedLengthPrefix(t *testing.T) {
	// 0xbf says "a string whose length occupies 8 bytes", then claims a length
	// far past the cap, with no payload behind it.
	if _, err := Decode(mustHex(t, "bf"+strings.Repeat("ff", 8))); !errors.Is(err, ErrDecode) {
		t.Fatalf("oversized length = %v, want ErrDecode", err)
	}
}
