package token

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/ecirlabs/matrix-core/internal/ethsig"
	"github.com/ecirlabs/matrix-core/internal/evmtx"
)

// This file lets a transaction signed by an ordinary Ethereum wallet be a
// transaction on this chain.
//
// It is the third signing scheme, and unlike the second it is not a different
// payload for the same struct: the ENVELOPE is the authority. A transaction that
// arrives this way carries the exact bytes the wallet signed, and every native
// field on the struct is DERIVED from those bytes and re-derived on verification
// rather than trusted. A peer that gossips an envelope with the amount field
// quietly changed gets the block rejected, because the amount is read out of the
// envelope both times.
//
// WHAT IS DELIBERATELY NOT HERE. Reserved recipients - bonding, validator set
// changes, bridge locks - are addressed by strings like
// "consensus/stake/bond/<id>", and an Ethereum transaction's `to` is twenty
// bytes with no room for one. They stay on the ed25519 and EIP-712 paths, which
// is where operators already drive them. Reaching them from a wallet needs
// reserved ADDRESSES that decode to those recipients, which is a separate
// decision about the chain's address space and not something to improvise here.
// Contract creation is refused for the plainer reason that there is no EVM.

// EVMDecimals is the number of decimals this chain reports to Ethereum tooling.
//
// It is 18, not the native ledger's 9, because every wallet assumes 18 for a
// chain's own coin and a wallet that assumes wrong shows the user a balance off
// by a factor of a billion. The two scales already have an exact integer
// relationship (see supply.go); an amount that is not an exact multiple of
// ERC20PerNativeUnit is refused rather than rounded, so no dust is ever created
// or destroyed in the conversion.
const EVMDecimals = 18

// NewTransactionFromEVM builds a chain transaction from the raw bytes an
// Ethereum wallet produced, after checking they authorise exactly what the
// resulting transaction says.
//
// chainID is this chain's id. A transaction signed for any other chain is
// refused here, which is the whole reason EIP-155 exists and the property the
// layout this supplements did not have.
func NewTransactionFromEVM(raw []byte, chainID uint64) (*Transaction, error) {
	parsed, err := evmtx.DecodeRaw(raw)
	if err != nil {
		return nil, err
	}
	sender, err := parsed.Sender(chainID)
	if err != nil {
		return nil, err
	}
	to, amount, err := evmRecipientAndAmount(parsed)
	if err != nil {
		return nil, err
	}

	from := make([]byte, ethsig.AddressLen)
	copy(from, sender[:])
	rawCopy := make([]byte, len(raw))
	copy(rawCopy, raw)

	return &Transaction{
		From:    from,
		To:      to,
		Amount:  amount,
		Nonce:   parsed.Nonce,
		ChainID: chainID,
		Raw:     rawCopy,
		// Timestamp and PrevHash carry no authority on this path: the envelope
		// covers neither, so leaving them zero is honest. The chain orders the
		// transaction; the wallet does not date it.
	}, nil
}

// evmRecipientAndAmount translates an envelope's recipient and value into the
// ledger's account id and base units.
func evmRecipientAndAmount(parsed *evmtx.Transaction) (to string, amount uint64, err error) {
	if parsed.To == nil {
		return "", 0, fmt.Errorf("%w: contract creation is not supported: this chain runs no EVM",
			ErrInvalidTransaction)
	}
	if len(parsed.Data) > 0 {
		// Refused rather than ignored. A wallet that attaches calldata is asking
		// for something to happen, and silently moving the value while dropping
		// the request is the worst of the available answers.
		return "", 0, fmt.Errorf("%w: transaction carries %d bytes of calldata, and this chain runs no EVM",
			ErrInvalidTransaction, len(parsed.Data))
	}
	value := parsed.Value
	if value == nil {
		value = new(big.Int)
	}
	amount, err = ERC20ToNative(value)
	if err != nil {
		return "", 0, fmt.Errorf("%w: value %s: %v", ErrInvalidTransaction, value, err)
	}
	return EthAccountID(*parsed.To), amount, nil
}

// IsEVM reports whether this transaction's authority is an Ethereum envelope.
func (t *Transaction) IsEVM() bool { return len(t.Raw) > 0 }

// EVMHash returns the transaction id Ethereum tooling uses: keccak256 over the
// signed envelope. It is what an explorer shows, what a wallet reports back, and
// what an exchange records against a deposit.
func (t *Transaction) EVMHash() ([]byte, error) {
	if !t.IsEVM() {
		return nil, fmt.Errorf("%w: not an ethereum-enveloped transaction", ErrInvalidTransaction)
	}
	return ethsig.Keccak256(t.Raw), nil
}

// VerifyEVM re-derives every field from the envelope and requires the struct to
// agree, then confirms the signature and the chain.
//
// Re-deriving rather than checking a signature over the struct is what makes
// this safe to receive from a peer. The envelope is the only thing signed, so a
// field that disagrees with it is a field nobody authorised, whether it was
// changed in transit or by a malicious proposer.
func (t *Transaction) VerifyEVM(chainID uint64) error {
	if !t.IsEVM() {
		return fmt.Errorf("%w: not an ethereum-enveloped transaction", ErrInvalidTransaction)
	}
	derived, err := NewTransactionFromEVM(t.Raw, chainID)
	if err != nil {
		return err
	}
	if !strings.EqualFold(derived.SenderID(), t.SenderID()) {
		return fmt.Errorf("%w: envelope is signed by %s but the transaction says %s",
			ErrInvalidSignature, derived.SenderID(), t.SenderID())
	}
	if derived.To != t.To {
		return fmt.Errorf("%w: envelope pays %s but the transaction says %s",
			ErrInvalidTransaction, derived.To, t.To)
	}
	if derived.Amount != t.Amount {
		return fmt.Errorf("%w: envelope moves %d but the transaction says %d",
			ErrInvalidTransaction, derived.Amount, t.Amount)
	}
	if derived.Nonce != t.Nonce {
		return fmt.Errorf("%w: envelope uses nonce %d but the transaction says %d",
			ErrInvalidTransaction, derived.Nonce, t.Nonce)
	}
	if t.ChainID != 0 && t.ChainID != chainID {
		return fmt.Errorf("%w: transaction declares chain %d, this chain is %d",
			ErrInvalidTransaction, t.ChainID, chainID)
	}
	return nil
}

// VerifyForChain is the verification every consensus path uses. It is Verify
// plus the chain check that Verify alone cannot make, because an envelope's
// signature proves which chain it was signed FOR but only config knows which
// chain this IS.
//
// Non-EVM transactions fall through to Verify unchanged. They carry no chain id
// at all, which is a gap this scheme closes only for transactions that use it;
// see the migration note in docs.
func (t *Transaction) VerifyForChain(chainID uint64) error {
	if t.IsEVM() {
		return t.VerifyEVM(chainID)
	}
	return t.Verify()
}
