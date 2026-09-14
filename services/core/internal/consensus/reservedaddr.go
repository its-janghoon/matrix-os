package consensus

import (
	"github.com/ecirlabs/matrix-core/internal/ethsig"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// Reaching a consensus operation from a wallet.
//
// A consensus operation is addressed by a string - "consensus/stake/bond/<id>" -
// and an Ethereum transaction's `to` is twenty bytes with no room for one. So
// the operations an account performs ON ITSELF get a fixed address each, and the
// account that would not fit in the address comes from the SENDER, recovered
// from the signature and therefore not something a caller can claim.
//
// SELF-TARGETED IS THE WHOLE SET, and that is not a simplification. Bonding,
// withdrawing a bond and leaving the validator set are already required to be
// self-signed by the account they name - a bond moves the sender's own coins,
// and bonded-open refuses an admission or exit signed by anyone but its subject.
// So there is nothing for the address to carry: the sender IS the parameter.
//
// WHAT IS DELIBERATELY ABSENT. Admission to the validator set is not here,
// because "consensus/validator/add/<key>" names an ed25519 CONSENSUS key, which
// is thirty-two bytes and cannot be derived from the sender's address. Giving it
// an address would mean inventing a registry mapping an account to a consensus
// key, which is a real decision about how an operator key and a consensus key
// relate, and not one to smuggle in through an address table. A node already
// admits itself once its bond is visible (see maybeMaintainOpenMembership), so
// what an operator needs from a wallet is the bond - which is here.
//
// The addresses are in a range no real account can occupy in practice: an
// account address is the tail of a keccak hash, so one with eighteen leading
// zero bytes is not something anyone finds.

var (
	// ReservedBondAddress bonds the value sent, from the sender, for the sender.
	ReservedBondAddress = reservedAddress(0x01)
	// ReservedWithdrawBondAddress returns the sender's whole bond. It carries no
	// value; the amount is not the caller's to choose.
	ReservedWithdrawBondAddress = reservedAddress(0x02)
	// ReservedValidatorExitAddress is a voluntary exit from the validator set.
	ReservedValidatorExitAddress = reservedAddress(0x03)
)

// reservedAddress builds 0x00..00<n>, the shape every reserved address takes.
func reservedAddress(n byte) ethsig.Address {
	var addr ethsig.Address
	addr[ethsig.AddressLen-1] = n
	return addr
}

// ReservedRecipientFor translates a reserved address into the recipient string
// its operation is spelled as, for a given sender.
//
// The sender must be an Ethereum-controlled account: these addresses only ever
// arrive inside an envelope, whose sender is an address by construction, and an
// account id of another kind here would mean the caller had chosen it rather
// than the signature having produced it.
func ReservedRecipientFor(to ethsig.Address, sender string) (string, bool) {
	if !token.IsEthAccountID(sender) {
		return "", false
	}
	switch to {
	case ReservedBondAddress:
		return BondAccount(sender), true
	case ReservedWithdrawBondAddress:
		return WithdrawRecipient(sender), true
	case ReservedValidatorExitAddress:
		return RemoveValidatorRecipient(sender), true
	default:
		return "", false
	}
}

// IsReservedAddress reports whether an address names a consensus operation
// rather than an account.
func IsReservedAddress(to ethsig.Address) bool {
	switch to {
	case ReservedBondAddress, ReservedWithdrawBondAddress, ReservedValidatorExitAddress:
		return true
	default:
		return false
	}
}
