// Package evmtx encodes, decodes, hashes and recovers the sender of Ethereum
// transactions, so a wallet that already exists can sign for this chain.
//
// WHY THIS SHAPE AND NOT OURS. The native transaction is a length-prefixed
// layout of our own design, and nothing signs it but our own code. Every wallet,
// explorer, indexer and exchange integration in the ecosystem speaks the layout
// in this file instead. Adopting it is not a compatibility veneer over a
// different chain: these bytes become what a signature covers, so the chain
// itself has to agree that a transaction IS this.
//
// Two forms are supported, because a wallet picks between them and a chain that
// accepts only one rejects half of what arrives:
//
//	legacy (EIP-155)    rlp([nonce, gasPrice, gas, to, value, data, v, r, s])
//	dynamic fee (1559)  0x02 || rlp([chainId, nonce, tip, feeCap, gas, to,
//	                                 value, data, accessList, yParity, r, s])
//
// THE CHAIN ID IS THE POINT. It is inside the bytes the signature covers, so a
// transaction signed for chain 1 cannot be replayed on chain 2. The native
// layout this replaces had no chain identifier at all, which meant a testnet
// signature was byte-for-byte valid on mainnet.
//
// GAS IS CARRIED, NOT EXECUTED. This chain runs no EVM, so gasPrice and gasLimit
// are not metering anything. They are part of the signed payload because a
// wallet puts them there and the signature covers them, and the fee policy reads
// them. Calling them gas keeps the wallet honest about what it showed the user;
// it does not claim there is a virtual machine behind them.
package evmtx

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/ecirlabs/matrix-core/internal/ethsig"
	"github.com/ecirlabs/matrix-core/internal/rlp"
)

// TxType identifies the transaction envelope.
type TxType uint8

const (
	// TxLegacy is the original untyped transaction, made replay-safe by EIP-155.
	TxLegacy TxType = 0x00
	// TxDynamicFee is the EIP-1559 typed transaction.
	TxDynamicFee TxType = 0x02
)

// Errors this package returns. They are sentinels so a caller can tell a
// malformed payload from a signature that does not recover, which are different
// answers to give an RPC client.
var (
	// ErrInvalidTx is a structurally malformed or non-canonical transaction.
	ErrInvalidTx = errors.New("evmtx: malformed transaction")
	// ErrUnsupportedType is a transaction envelope this chain does not accept.
	ErrUnsupportedType = errors.New("evmtx: unsupported transaction type")
	// ErrChainIDMismatch is a transaction signed for a different chain. It is
	// separate from ErrInvalidTx because it is the one rejection whose cause is
	// the caller's network setting rather than their bytes.
	ErrChainIDMismatch = errors.New("evmtx: transaction is for a different chain")
)

// Transaction is a decoded Ethereum transaction.
//
// GasFeeCap carries gasPrice for a legacy transaction and maxFeePerGas for a
// dynamic-fee one, so a fee policy reads one field rather than branching on the
// type at every use.
type Transaction struct {
	Type    TxType
	ChainID uint64
	Nonce   uint64
	// GasTipCap is maxPriorityFeePerGas, and is nil on a legacy transaction.
	GasTipCap *big.Int
	// GasFeeCap is gasPrice (legacy) or maxFeePerGas (dynamic fee).
	GasFeeCap *big.Int
	Gas       uint64
	// To is the recipient. Nil means contract creation, which this chain has no
	// EVM to honour and therefore refuses at a higher layer rather than here.
	To    *ethsig.Address
	Value *big.Int
	Data  []byte

	// accessListRLP is the access list exactly as it arrived, kept as encoded
	// bytes rather than decoded.
	//
	// It is preserved verbatim and never interpreted. Nothing here can act on an
	// access list - it is an EVM state-access hint and there is no EVM - but it
	// is inside the signed payload, so re-encoding it from a parsed form risks
	// producing different bytes and therefore a different hash from the one the
	// wallet signed. Carrying the original bytes makes that impossible.
	accessListRLP []byte

	// V, R, S are the signature. On a legacy transaction V carries the EIP-155
	// chain encoding; on a typed one it is the bare y-parity.
	V, R, S *big.Int
}

// emptyAccessList is the encoding of an access list with no entries, which is
// what a wallet sends for an ordinary transfer.
var emptyAccessList = rlp.EncodeList()

// SigningHash returns the 32-byte digest the sender's key signs over.
func (t *Transaction) SigningHash() ([]byte, error) {
	payload, err := t.signingPayload()
	if err != nil {
		return nil, err
	}
	return ethsig.Keccak256(payload), nil
}

// signingPayload builds the pre-signature encoding for this transaction's type.
func (t *Transaction) signingPayload() ([]byte, error) {
	switch t.Type {
	case TxLegacy:
		// EIP-155: the chain id and two zeros stand in for the signature fields,
		// which is what binds a legacy signature to one chain.
		fields, err := t.commonLegacyFields()
		if err != nil {
			return nil, err
		}
		fields = append(fields, rlp.EncodeUint(t.ChainID), rlp.EncodeUint(0), rlp.EncodeUint(0))
		return rlp.EncodeList(fields...), nil

	case TxDynamicFee:
		fields, err := t.commonDynamicFields()
		if err != nil {
			return nil, err
		}
		return append([]byte{byte(TxDynamicFee)}, rlp.EncodeList(fields...)...), nil

	default:
		return nil, fmt.Errorf("%w: type 0x%02x", ErrUnsupportedType, byte(t.Type))
	}
}

// commonLegacyFields encodes the six fields a legacy transaction shares between
// its signing payload and its signed encoding.
func (t *Transaction) commonLegacyFields() ([][]byte, error) {
	gasPrice, err := rlp.EncodeBig(t.GasFeeCap)
	if err != nil {
		return nil, err
	}
	value, err := rlp.EncodeBig(t.Value)
	if err != nil {
		return nil, err
	}
	return [][]byte{
		rlp.EncodeUint(t.Nonce),
		gasPrice,
		rlp.EncodeUint(t.Gas),
		encodeTo(t.To),
		value,
		rlp.EncodeBytes(t.Data),
	}, nil
}

// commonDynamicFields encodes the nine fields a dynamic-fee transaction shares
// between its signing payload and its signed encoding.
func (t *Transaction) commonDynamicFields() ([][]byte, error) {
	tip, err := rlp.EncodeBig(t.GasTipCap)
	if err != nil {
		return nil, err
	}
	feeCap, err := rlp.EncodeBig(t.GasFeeCap)
	if err != nil {
		return nil, err
	}
	value, err := rlp.EncodeBig(t.Value)
	if err != nil {
		return nil, err
	}
	accessList := t.accessListRLP
	if len(accessList) == 0 {
		accessList = emptyAccessList
	}
	return [][]byte{
		rlp.EncodeUint(t.ChainID),
		rlp.EncodeUint(t.Nonce),
		tip,
		feeCap,
		rlp.EncodeUint(t.Gas),
		encodeTo(t.To),
		value,
		rlp.EncodeBytes(t.Data),
		accessList,
	}, nil
}

// encodeTo encodes the recipient. A nil recipient is the empty string, which is
// how contract creation is spelled and is deliberately NOT the same as an
// address of twenty zero bytes.
func encodeTo(to *ethsig.Address) []byte {
	if to == nil {
		return rlp.EncodeBytes(nil)
	}
	return rlp.EncodeBytes(to[:])
}

// MarshalBinary returns the signed transaction exactly as it travels on the
// wire, which is what eth_sendRawTransaction carries and what Hash covers.
func (t *Transaction) MarshalBinary() ([]byte, error) {
	if t.R == nil || t.S == nil || t.V == nil {
		return nil, fmt.Errorf("%w: transaction is not signed", ErrInvalidTx)
	}
	r, err := rlp.EncodeBig(t.R)
	if err != nil {
		return nil, err
	}
	s, err := rlp.EncodeBig(t.S)
	if err != nil {
		return nil, err
	}
	v, err := rlp.EncodeBig(t.V)
	if err != nil {
		return nil, err
	}

	switch t.Type {
	case TxLegacy:
		fields, err := t.commonLegacyFields()
		if err != nil {
			return nil, err
		}
		return rlp.EncodeList(append(fields, v, r, s)...), nil

	case TxDynamicFee:
		fields, err := t.commonDynamicFields()
		if err != nil {
			return nil, err
		}
		body := rlp.EncodeList(append(fields, v, r, s)...)
		return append([]byte{byte(TxDynamicFee)}, body...), nil

	default:
		return nil, fmt.Errorf("%w: type 0x%02x", ErrUnsupportedType, byte(t.Type))
	}
}

// Hash returns the transaction id: keccak256 over the signed encoding. It is
// the identifier every explorer, wallet and exchange uses to refer to a
// transaction, and the thing the native layout had no equivalent of.
func (t *Transaction) Hash() ([]byte, error) {
	raw, err := t.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return ethsig.Keccak256(raw), nil
}

// recoveryID extracts the 0/1 y-parity from V for this transaction's type, and
// reports the chain id a legacy V commits to.
func (t *Transaction) recoveryID() (recID byte, signedChainID uint64, err error) {
	if t.V == nil {
		return 0, 0, fmt.Errorf("%w: transaction has no V", ErrInvalidTx)
	}
	if t.Type != TxLegacy {
		v := t.V.Uint64()
		if !t.V.IsUint64() || v > 1 {
			return 0, 0, fmt.Errorf("%w: typed transaction y-parity must be 0 or 1, got %s", ErrInvalidTx, t.V)
		}
		return byte(v), t.ChainID, nil
	}

	if !t.V.IsUint64() {
		return 0, 0, fmt.Errorf("%w: legacy V is out of range", ErrInvalidTx)
	}
	v := t.V.Uint64()
	switch {
	case v == 27 || v == 28:
		// Pre-EIP-155. The signature commits to no chain, so it is replayable
		// across every chain by construction. Refused rather than accepted with
		// a warning: this chain exists after EIP-155 and has no legacy
		// signatures to honour.
		return 0, 0, fmt.Errorf("%w: pre-EIP-155 signature (v=%d) carries no chain id", ErrInvalidTx, v)
	case v >= 35:
		// v = recoveryID + chainID*2 + 35
		signed := (v - 35) / 2
		return byte((v - 35) % 2), signed, nil
	default:
		return 0, 0, fmt.Errorf("%w: legacy V must be 27, 28, or at least 35, got %d", ErrInvalidTx, v)
	}
}

// Sender recovers the address whose key signed this transaction, and confirms
// the signature commits to chainID. A caller that gets an address back has
// proof the holder of that key authorised exactly these bytes for exactly this
// chain.
func (t *Transaction) Sender(chainID uint64) (ethsig.Address, error) {
	var zero ethsig.Address
	if chainID == 0 {
		return zero, fmt.Errorf("%w: this node has no chain id configured", ErrChainIDMismatch)
	}
	recID, signedChainID, err := t.recoveryID()
	if err != nil {
		return zero, err
	}
	if signedChainID != chainID {
		return zero, fmt.Errorf("%w: signed for chain %d, this chain is %d",
			ErrChainIDMismatch, signedChainID, chainID)
	}
	// A typed transaction carries its chain id as a field as well as implying it
	// through the signing payload; they are the same value and disagreeing is
	// malformed.
	if t.Type != TxLegacy && t.ChainID != chainID {
		return zero, fmt.Errorf("%w: envelope says chain %d, this chain is %d",
			ErrChainIDMismatch, t.ChainID, chainID)
	}

	digest, err := t.SigningHash()
	if err != nil {
		return zero, err
	}
	sig := make([]byte, ethsig.SignatureLen)
	if t.R == nil || t.S == nil {
		return zero, fmt.Errorf("%w: transaction is not signed", ErrInvalidTx)
	}
	if len(t.R.Bytes()) > 32 || len(t.S.Bytes()) > 32 {
		return zero, fmt.Errorf("%w: R or S exceeds 32 bytes", ErrInvalidTx)
	}
	t.R.FillBytes(sig[0:32])
	t.S.FillBytes(sig[32:64])
	sig[64] = recID

	return ethsig.RecoverAddress(digest, sig)
}

// Sign signs the transaction with priv and fills V, R and S. It exists so a
// test can produce exactly what a wallet produces; nothing in the node signs a
// user's transaction.
func (t *Transaction) Sign(priv *secp256k1.PrivateKey, chainID uint64) error {
	t.ChainID = chainID
	digest, err := t.SigningHash()
	if err != nil {
		return err
	}
	sig, err := ethsig.SignDigest(priv, digest)
	if err != nil {
		return err
	}
	recID := sig[64]
	if recID >= 27 {
		recID -= 27
	}
	t.R = new(big.Int).SetBytes(sig[0:32])
	t.S = new(big.Int).SetBytes(sig[32:64])
	if t.Type == TxLegacy {
		t.V = new(big.Int).SetUint64(uint64(recID) + chainID*2 + 35)
	} else {
		t.V = new(big.Int).SetUint64(uint64(recID))
	}
	return nil
}
