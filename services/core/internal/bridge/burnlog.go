package bridge

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// This file closes the wrapped-burn -> native-unlock half of the bridge loop by
// decoding a real WrappedMatrix `Burned` event log into a BurnEvent that
// ProcessBurn can apply. Before this, ProcessBurn was a trusted primitive with
// no path from an actual on-chain burn to a native unlock; now an operator (or a
// future log subscription) that has an Ethereum receipt log for a Burned event
// can turn it directly into an authorized, replay-protected unlock.
//
// The decoder is a hand-written ABI log parser using only the crypto already in
// the build graph (keccak256 from attestor.go). It adds no new Go dependency, in
// line with the bridge's stdlib-only constraint, and it is exercised in
// burnlog_test.go against the exact bytes a hardhat-emitted Burned event
// produces so the on-chain layout and the Go decoder are proven byte-compatible.

// BurnedEventSignature is the human-readable Solidity event signature of the
// WrappedMatrix `Burned` event. Its keccak256 is topics[0] of every Burned log.
//
//	event Burned(address indexed burner, string nativeRecipient, uint256 amount)
//
// Only `burner` is indexed, so `nativeRecipient` and `amount` live in the log
// data (ABI-encoded as a dynamic tuple), while `burner` is topics[1].
const BurnedEventSignature = "Burned(address,string,uint256)"

// Errors specific to decoding a Burned event log.
var (
	// ErrWrongEventTopic is returned when a log's topics[0] is not the keccak256
	// of BurnedEventSignature, i.e. the log is not a Burned event.
	ErrWrongEventTopic = errors.New("bridge: log is not a WrappedMatrix Burned event")
	// ErrMalformedLog is returned when a log's topics or data do not match the
	// ABI layout of the Burned event.
	ErrMalformedLog = errors.New("bridge: malformed Burned event log")
)

// wordLen is the size of an ABI-encoded word (a uint256/bytes32) in bytes.
const wordLen = 32

// BurnedEventTopic returns topics[0] for the Burned event: keccak256 of the
// canonical event signature. It is computed rather than hardcoded so it stays
// correct if the signature constant ever changes.
func BurnedEventTopic() [wordLen]byte {
	var out [wordLen]byte
	copy(out[:], keccak256([]byte(BurnedEventSignature)))
	return out
}

// EthLog is the minimal shape of an Ethereum event log needed to decode a
// Burned event into a BurnEvent. It mirrors the fields an eth_getLogs /
// transaction-receipt log carries (topics, data, and the tx hash + log index
// that uniquely locate the log on-chain). It is deliberately a plain struct so a
// caller can populate it from any Ethereum client, an operator-supplied receipt,
// or a test, without this package importing an Ethereum RPC dependency.
type EthLog struct {
	// Topics are the log topics. topics[0] is the event signature hash and
	// topics[1] is the indexed `burner` address (left-padded to 32 bytes).
	Topics [][wordLen]byte
	// Data is the ABI-encoded non-indexed event arguments
	// (string nativeRecipient, uint256 amount).
	Data []byte
	// TxHash is the 0x-prefixed hex Ethereum transaction hash the log belongs to.
	TxHash string
	// LogIndex is the log's index within the block. Together with TxHash it
	// uniquely identifies the log and forms the replay-protection id.
	LogIndex uint64
}

// DecodedBurn is the fully parsed content of a Burned event, in addition to the
// BurnEvent it yields. It exposes the raw decoded fields for logging/audit while
// BurnEvent() gives the value ProcessBurn consumes.
type DecodedBurn struct {
	// Burner is the Ethereum address (20 bytes) that burned the wrapped tokens.
	Burner Address
	// NativeRecipient is the L1 account id the unlocked native MATRIX must go to.
	NativeRecipient string
	// ERC20Amount is the burned wrapped amount in 18-decimal ERC-20 base units.
	ERC20Amount *big.Int
	// ID is the stable, unique replay-protection id derived from the source log
	// as "txHash:logIndex".
	ID string
}

// BurnEvent converts a DecodedBurn into the BurnEvent ProcessBurn applies. The
// ERC-20 amount is validated against the native conversion factor inside
// ProcessBurn (ERC20ToNative), so this is a pure field mapping.
func (d *DecodedBurn) BurnEvent() BurnEvent {
	return BurnEvent{
		ID:          d.ID,
		ToAccount:   d.NativeRecipient,
		ERC20Amount: new(big.Int).Set(d.ERC20Amount),
	}
}

// DecodeBurnedLog decodes a WrappedMatrix `Burned` event log into a DecodedBurn.
// It verifies the event signature topic, recovers the indexed burner address
// from topics[1], and ABI-decodes (string nativeRecipient, uint256 amount) from
// the log data. The resulting id is "txHash:logIndex", which is globally unique
// per emitted log and is exactly the replay key ProcessBurn dedups on.
//
// The layout it expects is the standard Solidity ABI encoding for the two
// non-indexed args as a head/tail tuple:
//
//	data[0:32]   = offset to the string tail (0x40 for this 2-arg tuple)
//	data[32:64]  = amount (uint256)
//	data[off:..] = string length (32 bytes) followed by the UTF-8 bytes,
//	               right-padded to a multiple of 32.
func DecodeBurnedLog(log EthLog) (*DecodedBurn, error) {
	if len(log.Topics) < 2 {
		return nil, fmt.Errorf("%w: expected 2 topics (signature, burner), got %d", ErrMalformedLog, len(log.Topics))
	}
	if log.Topics[0] != BurnedEventTopic() {
		return nil, ErrWrongEventTopic
	}
	if log.TxHash == "" {
		return nil, fmt.Errorf("%w: log has no tx hash", ErrMalformedLog)
	}

	// topics[1] is the indexed address, left-padded to 32 bytes: the address is
	// the low 20 bytes. The high 12 bytes must be zero for a well-formed address
	// topic.
	burnerTopic := log.Topics[1]
	for _, b := range burnerTopic[:wordLen-AddressLen] {
		if b != 0 {
			return nil, fmt.Errorf("%w: burner topic has non-zero padding", ErrMalformedLog)
		}
	}
	var burner Address
	copy(burner[:], burnerTopic[wordLen-AddressLen:])

	recipient, amount, err := decodeStringUint(log.Data)
	if err != nil {
		return nil, err
	}

	return &DecodedBurn{
		Burner:          burner,
		NativeRecipient: recipient,
		ERC20Amount:     amount,
		ID:              fmt.Sprintf("%s:%d", log.TxHash, log.LogIndex),
	}, nil
}

// DecodeBurnEvent is the convenience path from an on-chain log straight to the
// BurnEvent ProcessBurn consumes.
func DecodeBurnEvent(log EthLog) (BurnEvent, error) {
	d, err := DecodeBurnedLog(log)
	if err != nil {
		return BurnEvent{}, err
	}
	return d.BurnEvent(), nil
}

// decodeStringUint ABI-decodes a (string, uint256) tuple as laid out in a
// Solidity event's data section. It rejects truncated data, an out-of-range
// string offset, or a string length that overruns the buffer, so a malformed or
// adversarial log cannot cause an out-of-bounds read or a silently wrong unlock.
func decodeStringUint(data []byte) (string, *big.Int, error) {
	// Head: 2 words (offset-to-string, amount). Minimum length is 2 words plus at
	// least one word for the string length.
	if len(data) < 3*wordLen {
		return "", nil, fmt.Errorf("%w: data too short (%d bytes)", ErrMalformedLog, len(data))
	}
	if len(data)%wordLen != 0 {
		return "", nil, fmt.Errorf("%w: data length %d is not a multiple of %d", ErrMalformedLog, len(data), wordLen)
	}

	strOffset := new(big.Int).SetBytes(data[0:wordLen])
	amount := new(big.Int).SetBytes(data[wordLen : 2*wordLen])

	// The string offset is a byte offset from the start of data and must land on a
	// word boundary within the buffer, leaving room for the length word.
	if !strOffset.IsUint64() {
		return "", nil, fmt.Errorf("%w: string offset out of range", ErrMalformedLog)
	}
	off := strOffset.Uint64()
	if off%wordLen != 0 || off+wordLen > uint64(len(data)) {
		return "", nil, fmt.Errorf("%w: string offset %d out of bounds", ErrMalformedLog, off)
	}

	lenWord := new(big.Int).SetBytes(data[off : off+wordLen])
	if !lenWord.IsUint64() {
		return "", nil, fmt.Errorf("%w: string length out of range", ErrMalformedLog)
	}
	strLen := lenWord.Uint64()
	start := off + wordLen
	// The prior offset check guarantees off+wordLen <= len(data), so
	// start <= len(data) and uint64(len(data))-start does not underflow. Compare
	// strLen against that remaining room instead of computing start+strLen, which
	// would wrap for a near-MaxUint64 strLen and let an adversarial log bypass the
	// guard and panic on the slice below.
	if strLen > uint64(len(data))-start {
		return "", nil, fmt.Errorf("%w: string length %d overruns data", ErrMalformedLog, strLen)
	}
	// The string bytes are right-padded to a multiple of 32; the padding must be
	// present (already guaranteed by the multiple-of-word length check) but need
	// not be zero to decode. Extract exactly strLen bytes.
	s := string(data[start : start+strLen])
	if strings.ContainsRune(s, '\x00') {
		return "", nil, fmt.Errorf("%w: native recipient contains a NUL byte", ErrMalformedLog)
	}
	return s, amount, nil
}
