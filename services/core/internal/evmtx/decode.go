package evmtx

import (
	"fmt"
	"math/big"

	"github.com/ecirlabs/matrix-core/internal/ethsig"
	"github.com/ecirlabs/matrix-core/internal/rlp"
)

// DecodeRaw parses the bytes eth_sendRawTransaction carries.
//
// The first byte decides the envelope: a value below 0x80 is a type byte (RLP
// could never start a list or a long string there), and anything else is a
// legacy transaction whose first byte is its own RLP list prefix. That is the
// rule EIP-2718 defines, and it is why type numbers stop at 0x7f.
//
// Decoding is strict. A transaction that decodes must re-encode to the exact
// bytes it came from, because the hash of those bytes is its identity: a lenient
// decoder that normalises anything gives one transaction two ids, and an
// exchange crediting a deposit by id would then be looking for the wrong one.
// internal/rlp enforces most of this by refusing non-canonical encodings; the
// re-encode check below closes the rest.
func DecodeRaw(raw []byte) (*Transaction, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: empty payload", ErrInvalidTx)
	}

	var (
		tx  *Transaction
		err error
	)
	switch first := raw[0]; {
	case first == byte(TxDynamicFee):
		tx, err = decodeDynamicFee(raw[1:])
	case first < 0x80:
		return nil, fmt.Errorf("%w: type 0x%02x", ErrUnsupportedType, first)
	default:
		tx, err = decodeLegacy(raw)
	}
	if err != nil {
		return nil, err
	}

	// Prove the round trip rather than trust it. This is the check that makes
	// the transaction id trustworthy.
	reencoded, err := tx.MarshalBinary()
	if err != nil {
		return nil, err
	}
	if len(reencoded) != len(raw) {
		return nil, fmt.Errorf("%w: re-encoding produced %d bytes, not %d", ErrInvalidTx, len(reencoded), len(raw))
	}
	for i := range raw {
		if raw[i] != reencoded[i] {
			return nil, fmt.Errorf("%w: re-encoding differs at byte %d", ErrInvalidTx, i)
		}
	}
	return tx, nil
}

// decodeLegacy parses rlp([nonce, gasPrice, gas, to, value, data, v, r, s]).
func decodeLegacy(raw []byte) (*Transaction, error) {
	fields, err := listFields(raw, 9, "legacy")
	if err != nil {
		return nil, err
	}

	tx := &Transaction{Type: TxLegacy}
	if tx.Nonce, err = fields[0].Uint(); err != nil {
		return nil, fmt.Errorf("%w: nonce: %v", ErrInvalidTx, err)
	}
	if tx.GasFeeCap, err = fields[1].Big(); err != nil {
		return nil, fmt.Errorf("%w: gasPrice: %v", ErrInvalidTx, err)
	}
	if tx.Gas, err = fields[2].Uint(); err != nil {
		return nil, fmt.Errorf("%w: gas: %v", ErrInvalidTx, err)
	}
	if tx.To, err = decodeTo(fields[3]); err != nil {
		return nil, err
	}
	if tx.Value, err = fields[4].Big(); err != nil {
		return nil, fmt.Errorf("%w: value: %v", ErrInvalidTx, err)
	}
	if fields[5].IsList() {
		return nil, fmt.Errorf("%w: data must be a byte string", ErrInvalidTx)
	}
	tx.Data = fields[5].Bytes
	if tx.V, tx.R, tx.S, err = decodeSignature(fields[6], fields[7], fields[8]); err != nil {
		return nil, err
	}
	// The chain id a legacy transaction commits to lives inside V, so recover it
	// here and let the struct report it like a typed transaction does.
	if _, signedChainID, err := tx.recoveryID(); err == nil {
		tx.ChainID = signedChainID
	}
	return tx, nil
}

// decodeDynamicFee parses the EIP-1559 body, which follows the 0x02 type byte.
func decodeDynamicFee(body []byte) (*Transaction, error) {
	fields, err := listFields(body, 12, "dynamic-fee")
	if err != nil {
		return nil, err
	}

	tx := &Transaction{Type: TxDynamicFee}
	if tx.ChainID, err = fields[0].Uint(); err != nil {
		return nil, fmt.Errorf("%w: chainId: %v", ErrInvalidTx, err)
	}
	if tx.Nonce, err = fields[1].Uint(); err != nil {
		return nil, fmt.Errorf("%w: nonce: %v", ErrInvalidTx, err)
	}
	if tx.GasTipCap, err = fields[2].Big(); err != nil {
		return nil, fmt.Errorf("%w: maxPriorityFeePerGas: %v", ErrInvalidTx, err)
	}
	if tx.GasFeeCap, err = fields[3].Big(); err != nil {
		return nil, fmt.Errorf("%w: maxFeePerGas: %v", ErrInvalidTx, err)
	}
	if tx.Gas, err = fields[4].Uint(); err != nil {
		return nil, fmt.Errorf("%w: gas: %v", ErrInvalidTx, err)
	}
	if tx.To, err = decodeTo(fields[5]); err != nil {
		return nil, err
	}
	if tx.Value, err = fields[6].Big(); err != nil {
		return nil, fmt.Errorf("%w: value: %v", ErrInvalidTx, err)
	}
	if fields[7].IsList() {
		return nil, fmt.Errorf("%w: data must be a byte string", ErrInvalidTx)
	}
	tx.Data = fields[7].Bytes
	if !fields[8].IsList() {
		return nil, fmt.Errorf("%w: accessList must be a list", ErrInvalidTx)
	}
	// Re-encoded from the decoded form rather than sliced out of the input,
	// which keeps this package free of offset arithmetic over the caller's
	// buffer. DecodeRaw's re-encode check proves the result is byte-identical to
	// what arrived, so a list this cannot reproduce is rejected rather than
	// silently rewritten.
	if tx.accessListRLP, err = reencodeAccessList(fields[8]); err != nil {
		return nil, err
	}
	if tx.V, tx.R, tx.S, err = decodeSignature(fields[9], fields[10], fields[11]); err != nil {
		return nil, err
	}
	return tx, nil
}

// listFields decodes raw as a list and requires exactly n elements.
func listFields(raw []byte, n int, kind string) ([]rlp.Item, error) {
	item, err := rlp.Decode(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTx, err)
	}
	if !item.IsList() {
		return nil, fmt.Errorf("%w: a %s transaction must be an RLP list", ErrInvalidTx, kind)
	}
	if len(item.List) != n {
		return nil, fmt.Errorf("%w: a %s transaction has %d fields, got %d",
			ErrInvalidTx, kind, n, len(item.List))
	}
	return item.List, nil
}

// decodeTo reads the recipient. An empty string is contract creation; anything
// other than empty or twenty bytes is malformed.
func decodeTo(item rlp.Item) (*ethsig.Address, error) {
	if item.IsList() {
		return nil, fmt.Errorf("%w: to must be a byte string", ErrInvalidTx)
	}
	if len(item.Bytes) == 0 {
		return nil, nil
	}
	addr, err := ethsig.AddressFromBytes(item.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: to: %v", ErrInvalidTx, err)
	}
	return &addr, nil
}

// decodeSignature reads the V, R and S fields.
func decodeSignature(vItem, rItem, sItem rlp.Item) (v, r, s *big.Int, err error) {
	if v, err = vItem.Big(); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: v: %v", ErrInvalidTx, err)
	}
	if r, err = rItem.Big(); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: r: %v", ErrInvalidTx, err)
	}
	if s, err = sItem.Big(); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: s: %v", ErrInvalidTx, err)
	}
	if r.Sign() == 0 && s.Sign() == 0 {
		return nil, nil, nil, fmt.Errorf("%w: transaction is unsigned", ErrInvalidTx)
	}
	return v, r, s, nil
}

// reencodeAccessList rebuilds the encoding of a decoded access list.
func reencodeAccessList(item rlp.Item) ([]byte, error) {
	encoded, err := reencodeItem(item)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

// reencodeItem rebuilds the RLP of an already-decoded item.
func reencodeItem(item rlp.Item) ([]byte, error) {
	if !item.IsList() {
		return rlp.EncodeBytes(item.Bytes), nil
	}
	parts := make([][]byte, 0, len(item.List))
	for _, child := range item.List {
		encoded, err := reencodeItem(child)
		if err != nil {
			return nil, err
		}
		parts = append(parts, encoded)
	}
	return rlp.EncodeList(parts...), nil
}
