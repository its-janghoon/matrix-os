package cli

import (
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/consensus"
)

// TestStakeCommandsCloseTheWithdrawalGap is the case that made this command
// necessary: a validator could bond and had no way to get the bond back.
// SubmitWithdrawBond existed in Go and on no RPC or command, and the obvious
// workaround failed too - a withdrawal must carry NO amount and `wallet
// transfer` refuses --amount 0 - so an operator could put stake at risk with no
// exit, which on a network anyone may join is a reason not to join.
func TestStakeCommandsCloseTheWithdrawalGap(t *testing.T) {
	addr, mkt, _ := startServer(t)

	walletPath := t.TempDir() + "/validator.json"
	validator, err := createWallet(walletPath)
	if err != nil {
		t.Fatalf("createWallet: %v", err)
	}
	if err := mkt.Ledger().Credit(validator.AccountID(), 1000); err != nil {
		t.Fatalf("Credit: %v", err)
	}

	// Bond: coins leave the spendable balance for a reserved account the wallet
	// cannot spend from.
	out, err := run(t, addr, "stake", "bond", "--wallet", walletPath, "--amount", "600")
	if err != nil {
		t.Fatalf("stake bond: %v (%s)", err, out)
	}

	bondAccount := consensus.BondAccount(validator.AccountID())
	bonded, err := mkt.Ledger().Balance(bondAccount)
	if err != nil {
		t.Fatalf("Balance(bond): %v", err)
	}
	if bonded != 600 {
		t.Fatalf("bonded = %d, want 600", bonded)
	}
	spendable, err := mkt.Ledger().Balance(validator.AccountID())
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if spendable != 400 {
		t.Fatalf("spendable = %d, want 400", spendable)
	}

	// status reports both numbers, which is the whole question an operator has.
	out, err = run(t, addr, "stake", "status", "--wallet", walletPath)
	if err != nil {
		t.Fatalf("stake status: %v (%s)", err, out)
	}
	if !strings.Contains(out, "bonded:    600") || !strings.Contains(out, "spendable: 400") {
		t.Fatalf("stake status did not report both balances:\n%s", out)
	}

	// The withdrawal submits, carrying no amount. Whether it COMMITS is a
	// consensus decision about the unbonding clock; what this proves is that an
	// operator has a way to ask at all, which is what was missing.
	out, err = run(t, addr, "stake", "withdraw", "--wallet", walletPath)
	if err != nil {
		t.Fatalf("stake withdraw: %v (%s)", err, out)
	}
}

// TestStakeStatusReadsAnyAccount covers the operator looking at a node they do
// not hold the wallet for, which is how a set is checked from one machine.
func TestStakeStatusReadsAnyAccount(t *testing.T) {
	addr, mkt, _ := startServer(t)

	walletPath := t.TempDir() + "/other.json"
	other, err := createWallet(walletPath)
	if err != nil {
		t.Fatalf("createWallet: %v", err)
	}
	if err := mkt.Ledger().Credit(consensus.BondAccount(other.AccountID()), 7000); err != nil {
		t.Fatalf("Credit bond: %v", err)
	}

	out, err := run(t, addr, "stake", "status", "--account", other.AccountID())
	if err != nil {
		t.Fatalf("stake status --account: %v (%s)", err, out)
	}
	if !strings.Contains(out, "bonded:    7000") {
		t.Fatalf("status did not report the bond:\n%s", out)
	}
	// An account with no bond reads as zero rather than as an error: "nothing
	// bonded" is an answer, not a failure.
	out, err = run(t, addr, "stake", "status", "--account", strings.Repeat("ab", 32))
	if err != nil {
		t.Fatalf("stake status for an unknown account: %v (%s)", err, out)
	}
	if !strings.Contains(out, "bonded:    0") {
		t.Fatalf("an unbonded account did not read as zero:\n%s", out)
	}
}

// TestStakeBondRefusesZero guards the one argument that would silently do
// nothing. A zero bond is a transaction that costs a nonce and changes no
// balance, and an operator who typed it would believe they had staked.
func TestStakeBondRefusesZero(t *testing.T) {
	addr, _, _ := startServer(t)
	walletPath := t.TempDir() + "/w.json"
	if _, err := createWallet(walletPath); err != nil {
		t.Fatalf("createWallet: %v", err)
	}
	if _, err := run(t, addr, "stake", "bond", "--wallet", walletPath, "--amount", "0"); err == nil {
		t.Fatal("a zero bond should be refused")
	}
}
