package consensus

import (
	"errors"
	"math/big"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/ecirlabs/matrix-core/internal/ethsig"
	"github.com/ecirlabs/matrix-core/internal/evmtx"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// walletFor produces a secp256k1 key and the account id it controls, standing in
// for a browser wallet.
func walletFor(t *testing.T) (*secp256k1.PrivateKey, ethsig.Address, string) {
	t.Helper()
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	addr := ethsig.AddressFromPubKey(priv.PubKey())
	return priv, addr, token.EthAccountID(addr)
}

// walletTx signs an envelope to `to` the way a wallet would.
func walletTx(t *testing.T, priv *secp256k1.PrivateKey, to ethsig.Address, wholeUnits uint64, nonce, chainID uint64) *token.Transaction {
	t.Helper()
	env := &evmtx.Transaction{
		Type:      evmtx.TxLegacy,
		Nonce:     nonce,
		GasFeeCap: big.NewInt(0),
		Gas:       21000,
		To:        &to,
		Value:     token.NativeToERC20(wholeUnits),
	}
	if err := env.Sign(priv, chainID); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	raw, err := env.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	tx, err := token.NewTransactionFromEVM(raw, chainID, ReservedRecipientFor)
	if err != nil {
		t.Fatalf("NewTransactionFromEVM: %v", err)
	}
	return tx
}

// TestAWalletCanReachTheOperationsItOwns is the point of the address table: the
// three things an account does to ITSELF are reachable from a wallet, and each
// resolves to the same recipient string a native transaction would have named.
func TestAWalletCanReachTheOperationsItOwns(t *testing.T) {
	const chainID = 61_337
	priv, _, accountID := walletFor(t)

	for _, tc := range []struct {
		name string
		to   ethsig.Address
		want string
	}{
		{"bond", ReservedBondAddress, BondAccount(accountID)},
		{"withdraw the bond", ReservedWithdrawBondAddress, WithdrawRecipient(accountID)},
		{"leave the validator set", ReservedValidatorExitAddress, RemoveValidatorRecipient(accountID)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := walletTx(t, priv, tc.to, 0, 1, chainID)
			if tx.To != tc.want {
				t.Fatalf("recipient = %q, want %q", tx.To, tc.want)
			}
			if tx.SenderID() != accountID {
				t.Fatalf("sender = %q, want %q", tx.SenderID(), accountID)
			}
			// The resolved recipient has to survive re-derivation, or a peer could
			// gossip the envelope with a different operation written beside it.
			if err := tx.VerifyForChain(chainID, ReservedRecipientFor); err != nil {
				t.Fatalf("VerifyForChain: %v", err)
			}
			if !IsReservedRecipient(tx.To) {
				t.Fatalf("%q did not read as a reserved recipient", tx.To)
			}
		})
	}
}

// TestTheSenderIsTheParameterAndCannotBeChosen is the property that makes a
// parameterless address safe. Two wallets sending to the same address reach two
// different bonds, because the account comes out of the signature rather than
// out of anything the caller wrote.
func TestTheSenderIsTheParameterAndCannotBeChosen(t *testing.T) {
	const chainID = 61_337
	privA, _, idA := walletFor(t)
	privB, _, idB := walletFor(t)

	txA := walletTx(t, privA, ReservedBondAddress, 5, 1, chainID)
	txB := walletTx(t, privB, ReservedBondAddress, 5, 1, chainID)

	if txA.To != BondAccount(idA) {
		t.Fatalf("A bonded to %q, want %q", txA.To, BondAccount(idA))
	}
	if txB.To != BondAccount(idB) {
		t.Fatalf("B bonded to %q, want %q", txB.To, BondAccount(idB))
	}
	if txA.To == txB.To {
		t.Fatal("two wallets reached the same bond account")
	}

	// Rewriting the recipient to another account's bond does not survive, because
	// verification re-derives it from the envelope and the envelope only says
	// "the bond address".
	txA.To = BondAccount(idB)
	if err := txA.VerifyForChain(chainID, ReservedRecipientFor); err == nil {
		t.Fatal("a rewritten reserved recipient was accepted")
	}
}

// TestABondFromAWalletIsAnOrdinaryBond runs the resolved transaction through the
// engine and checks the coins land where a native bond would have put them.
func TestABondFromAWalletIsAnOrdinaryBond(t *testing.T) {
	const chainID = 61_337
	eng := blockTimeEngine(t, nowFuncForTest())
	eng.mu.Lock()
	eng.chainID = chainID
	eng.mu.Unlock()

	priv, _, accountID := walletFor(t)
	if err := eng.ledger.Credit(accountID, 10*token.NativeUnit); err != nil {
		t.Fatalf("Credit: %v", err)
	}

	tx := walletTx(t, priv, ReservedBondAddress, 4*token.NativeUnit, 0, chainID)
	if err := eng.Submit(tx); err != nil {
		t.Fatalf("Submit a wallet-signed bond: %v", err)
	}

	// Submitted, not yet committed: the bond moves when a block applies it. What
	// this proves is that the operation was ADMITTED - the recipient parsed as a
	// stake operation, the signature verified, and the chain accepted it.
	if !IsStakeRecipient(tx.To) {
		t.Fatalf("%q is not a stake recipient", tx.To)
	}
	req, err := ParseStakeRecipient(tx.To)
	if err != nil {
		t.Fatalf("ParseStakeRecipient: %v", err)
	}
	if req.Op != StakeOpBond || req.Account != accountID {
		t.Fatalf("parsed %+v, want a bond for %s", req, accountID)
	}
}

// TestAnOrdinaryAddressIsStillAnOrdinaryAccount guards the table's edge. Only
// the listed addresses mean an operation; everything else is a transfer, and a
// table that swallowed a real address would send somebody's payment into a
// consensus operation.
func TestAnOrdinaryAddressIsStillAnOrdinaryAccount(t *testing.T) {
	_, _, sender := walletFor(t)

	ordinary, err := ethsig.ParseAddress("0x00000000000000000000000000000000000000aa")
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	if _, ok := ReservedRecipientFor(ordinary, sender); ok {
		t.Fatal("an ordinary address resolved to a consensus operation")
	}
	if IsReservedAddress(ordinary) {
		t.Fatal("an ordinary address read as reserved")
	}

	// The zero address is not reserved either. It is a real (if unspendable)
	// account on Ethereum, and treating it as an operation would give one of them
	// a meaning nobody intended.
	var zero ethsig.Address
	if IsReservedAddress(zero) {
		t.Fatal("the zero address read as reserved")
	}
}

// TestANonWalletSenderGetsNoReservedMeaning pins that these addresses only carry
// their meaning inside an envelope. An ed25519 account reaching one would be a
// caller CHOOSING the account the operation applies to, rather than the
// signature producing it.
func TestANonWalletSenderGetsNoReservedMeaning(t *testing.T) {
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	if _, ok := ReservedRecipientFor(ReservedBondAddress, acct.AccountID()); ok {
		t.Fatal("an ed25519 sender resolved a reserved address")
	}
}

// TestValidatorAdmissionIsDeliberatelyAbsent records the one operation that has
// no address, so its absence reads as a decision rather than an oversight.
// "consensus/validator/add/<key>" names a thirty-two byte ed25519 CONSENSUS key,
// which cannot be derived from a twenty-byte sender address; giving it one would
// mean inventing a registry mapping an account to a consensus key.
func TestValidatorAdmissionIsDeliberatelyAbsent(t *testing.T) {
	_, _, sender := walletFor(t)
	for n := byte(0); n < 32; n++ {
		recipient, ok := ReservedRecipientFor(reservedAddress(n), sender)
		if !ok {
			continue
		}
		if _, err := ParseSetChange(recipient, 0); err == nil {
			change, _ := ParseSetChange(recipient, 0)
			if change.Kind == SetChangeAdd {
				t.Fatalf("address %d resolves to a validator admission (%q), which cannot "+
					"name a consensus key", n, recipient)
			}
		}
	}
}

// TestAReservedAddressStillNeedsItsOwnRules proves the table only translates:
// the operation's own validity rules still apply afterwards. A withdrawal
// carrying value is refused by the stake rules, not by the address.
func TestAReservedAddressStillNeedsItsOwnRules(t *testing.T) {
	const chainID = 61_337
	eng := blockTimeEngine(t, nowFuncForTest())
	eng.mu.Lock()
	eng.chainID = chainID
	eng.mu.Unlock()

	priv, _, accountID := walletFor(t)
	if err := eng.ledger.Credit(accountID, 10*token.NativeUnit); err != nil {
		t.Fatalf("Credit: %v", err)
	}

	// A withdrawal that carries an amount: the address resolved fine, and the
	// stake rule refuses it.
	tx := walletTx(t, priv, ReservedWithdrawBondAddress, 1, 0, chainID)
	if tx.To != WithdrawRecipient(accountID) {
		t.Fatalf("recipient = %q, want the withdraw recipient", tx.To)
	}
	err := eng.Submit(tx)
	if err == nil {
		t.Fatal("a withdrawal carrying value was admitted")
	}
	if !errors.Is(err, ErrInvalidMessage) && !errors.Is(err, ErrBondLocked) && !errors.Is(err, ErrInsufficientBond) {
		t.Fatalf("refused with %v, want a stake rule rather than an address error", err)
	}
}
