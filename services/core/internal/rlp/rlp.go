// Package rlp implements the subset of Ethereum's Recursive Length Prefix
// encoding that a transaction needs.
//
// It is written here rather than pulled in from go-ethereum for the same reason
// internal/bridge carries its own JSON-RPC client: the dependency would bring a
// whole node implementation to get one encoder, and the encoder is a page of
// well-specified rules. What it must be is EXACT - RLP is inside the hash a
// signature covers, so a byte this package gets wrong is a transaction MetaMask
// signs and this chain rejects, with nothing to point at.
//
// The rules, from the yellow paper:
//
//	a single byte in [0x00, 0x7f]  is its own encoding
//	a string of 0-55 bytes         0x80+len, then the bytes
//	a longer string                0xb7+len(len), len big-endian, then the bytes
//	a list with 0-55 payload bytes 0xc0+len, then the payload
//	a longer list                  0xf7+len(len), len big-endian, then the payload
//
// Integers are big-endian with no leading zeros, so zero is the empty string.
// That is not a quirk to paper over: it is why a canonical encoding exists at
// all, and why decoding rejects a leading zero rather than accepting two
// spellings of one number.
package rlp

import (
	"errors"
	"fmt"
	"math/big"
)

// ErrDecode is the sentinel every decoding failure wraps, so a caller can tell
// a malformed payload from an I/O or semantic error without matching strings.
var ErrDecode = errors.New("rlp: malformed input")

// maxPayload bounds a single decoded item. A transaction is kilobytes; a length
// prefix claiming gigabytes is a hostile or corrupt input and must not become an
// allocation.
const maxPayload = 1 << 24 // 16 MiB

// Item is one decoded RLP value. Exactly one of Bytes or List is meaningful,
// which IsList decides. A decoded item never aliases the input buffer.
type Item struct {
	// Bytes is the payload of a string item.
	Bytes []byte
	// List holds the elements of a list item.
	List []Item
	// isList distinguishes an empty list from an empty string. Both encode to a
	// single byte (0xc0 and 0x80) and mean different things, so the distinction
	// cannot be recovered from the payload being empty.
	isList bool
}

// IsList reports whether the item is a list rather than a string.
func (i Item) IsList() bool { return i.isList }

// EncodeBytes encodes a byte string.
func EncodeBytes(b []byte) []byte {
	if len(b) == 1 && b[0] <= 0x7f {
		return []byte{b[0]}
	}
	return append(encodeLength(len(b), 0x80), b...)
}

// EncodeList encodes already-encoded elements as a list. Callers pass the
// concatenated encodings of the elements, which is what every transaction
// encoder below builds.
func EncodeList(encodedElements ...[]byte) []byte {
	var payload []byte
	for _, e := range encodedElements {
		payload = append(payload, e...)
	}
	return append(encodeLength(len(payload), 0xc0), payload...)
}

// EncodeUint encodes an unsigned integer in the canonical minimal-length form.
// Zero encodes as the empty string, which is the rule and not an edge case.
func EncodeUint(v uint64) []byte {
	return EncodeBytes(trimLeadingZeros(uintBytes(v)))
}

// EncodeBig encodes a non-negative big integer. A nil or zero value encodes as
// the empty string. A negative value is rejected: RLP has no signed form, and
// silently encoding the magnitude would produce a payload that verifies against
// the wrong number.
func EncodeBig(v *big.Int) ([]byte, error) {
	if v == nil {
		return EncodeBytes(nil), nil
	}
	if v.Sign() < 0 {
		return nil, fmt.Errorf("rlp: cannot encode the negative integer %s", v)
	}
	return EncodeBytes(v.Bytes()), nil
}

// encodeLength builds the prefix for a payload of n bytes, where offset is 0x80
// for strings and 0xc0 for lists.
func encodeLength(n int, offset byte) []byte {
	if n <= 55 {
		return []byte{offset + byte(n)}
	}
	lenBytes := trimLeadingZeros(uintBytes(uint64(n)))
	out := make([]byte, 0, 1+len(lenBytes))
	out = append(out, offset+55+byte(len(lenBytes)))
	return append(out, lenBytes...)
}

func uintBytes(v uint64) []byte {
	return []byte{
		byte(v >> 56), byte(v >> 48), byte(v >> 40), byte(v >> 32),
		byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v),
	}
}

func trimLeadingZeros(b []byte) []byte {
	i := 0
	for i < len(b) && b[i] == 0 {
		i++
	}
	return b[i:]
}

// Decode decodes exactly one item and requires the input to contain nothing
// else. Trailing bytes are an error rather than being ignored: a signed
// transaction with junk appended must not decode to the same transaction, or
// one payload would have two valid spellings and the hash of each would differ.
func Decode(data []byte) (Item, error) {
	item, rest, err := decodeItem(data)
	if err != nil {
		return Item{}, err
	}
	if len(rest) != 0 {
		return Item{}, fmt.Errorf("%w: %d trailing bytes after the item", ErrDecode, len(rest))
	}
	return item, nil
}

// decodeItem decodes the leading item and returns the remainder.
func decodeItem(data []byte) (Item, []byte, error) {
	if len(data) == 0 {
		return Item{}, nil, fmt.Errorf("%w: empty input", ErrDecode)
	}
	prefix := data[0]

	switch {
	case prefix <= 0x7f:
		return Item{Bytes: []byte{prefix}}, data[1:], nil

	case prefix <= 0xb7:
		n := int(prefix - 0x80)
		if len(data) < 1+n {
			return Item{}, nil, fmt.Errorf("%w: string of %d bytes truncated", ErrDecode, n)
		}
		payload := data[1 : 1+n]
		// A single byte below 0x80 must have used its own one-byte encoding.
		// Accepting the long form too would give that byte two spellings.
		if n == 1 && payload[0] <= 0x7f {
			return Item{}, nil, fmt.Errorf("%w: byte 0x%02x must be encoded as itself", ErrDecode, payload[0])
		}
		return Item{Bytes: copyOf(payload)}, data[1+n:], nil

	case prefix <= 0xbf:
		payload, rest, err := decodeLongPayload(data, prefix-0xb7)
		if err != nil {
			return Item{}, nil, err
		}
		return Item{Bytes: copyOf(payload)}, rest, nil

	case prefix <= 0xf7:
		n := int(prefix - 0xc0)
		if len(data) < 1+n {
			return Item{}, nil, fmt.Errorf("%w: list of %d bytes truncated", ErrDecode, n)
		}
		list, err := decodeList(data[1 : 1+n])
		if err != nil {
			return Item{}, nil, err
		}
		return Item{List: list, isList: true}, data[1+n:], nil

	default:
		payload, rest, err := decodeLongPayload(data, prefix-0xf7)
		if err != nil {
			return Item{}, nil, err
		}
		list, err := decodeList(payload)
		if err != nil {
			return Item{}, nil, err
		}
		return Item{List: list, isList: true}, rest, nil
	}
}

// decodeLongPayload reads a multi-byte length prefix and slices the payload it
// describes.
func decodeLongPayload(data []byte, lenOfLen byte) (payload, rest []byte, err error) {
	n := int(lenOfLen)
	if n == 0 || n > 8 {
		return nil, nil, fmt.Errorf("%w: length prefix of %d bytes", ErrDecode, n)
	}
	if len(data) < 1+n {
		return nil, nil, fmt.Errorf("%w: length prefix truncated", ErrDecode)
	}
	lenBytes := data[1 : 1+n]
	if lenBytes[0] == 0 {
		return nil, nil, fmt.Errorf("%w: length has a leading zero byte", ErrDecode)
	}
	var size uint64
	for _, b := range lenBytes {
		size = size<<8 | uint64(b)
	}
	// Below 56 the short form was required, so the long form is a second
	// spelling of the same payload.
	if size <= 55 {
		return nil, nil, fmt.Errorf("%w: payload of %d bytes must use the short form", ErrDecode, size)
	}
	if size > maxPayload {
		return nil, nil, fmt.Errorf("%w: payload of %d bytes exceeds the %d-byte cap", ErrDecode, size, maxPayload)
	}
	if uint64(len(data)-1-n) < size {
		return nil, nil, fmt.Errorf("%w: payload of %d bytes truncated", ErrDecode, size)
	}
	return data[1+n : 1+n+int(size)], data[1+n+int(size):], nil
}

// decodeList decodes every item in a list payload.
func decodeList(payload []byte) ([]Item, error) {
	out := make([]Item, 0, 8)
	for len(payload) > 0 {
		item, rest, err := decodeItem(payload)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
		payload = rest
	}
	return out, nil
}

func copyOf(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// Uint reads a string item as an unsigned integer, rejecting a non-canonical
// leading zero and anything wider than 64 bits.
func (i Item) Uint() (uint64, error) {
	if i.isList {
		return 0, fmt.Errorf("%w: expected an integer, got a list", ErrDecode)
	}
	if len(i.Bytes) > 0 && i.Bytes[0] == 0 {
		return 0, fmt.Errorf("%w: integer has a leading zero byte", ErrDecode)
	}
	if len(i.Bytes) > 8 {
		return 0, fmt.Errorf("%w: integer of %d bytes exceeds 64 bits", ErrDecode, len(i.Bytes))
	}
	var v uint64
	for _, b := range i.Bytes {
		v = v<<8 | uint64(b)
	}
	return v, nil
}

// Big reads a string item as a non-negative big integer, rejecting a
// non-canonical leading zero.
func (i Item) Big() (*big.Int, error) {
	if i.isList {
		return nil, fmt.Errorf("%w: expected an integer, got a list", ErrDecode)
	}
	if len(i.Bytes) > 0 && i.Bytes[0] == 0 {
		return nil, fmt.Errorf("%w: integer has a leading zero byte", ErrDecode)
	}
	return new(big.Int).SetBytes(i.Bytes), nil
}
