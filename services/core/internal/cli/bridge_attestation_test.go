package cli

import (
	"strings"
	"testing"
)

// The disagreement guards exist because the failure they prevent is expensive
// and mute: the mint reverts after the gas is spent, and the revert says
// "threshold not met" no matter which of these went wrong.

func TestTwoValidatorsNamingDifferentRecipientsIsRefused(t *testing.T) {
	err := checkAttestationsAgree([]attestationOut{
		{Validator: "a:9091", LockID: "ab", Recipient: "0xaa", ERC20Amount: "1", Attestor: "0x01"},
		{Validator: "b:9091", LockID: "ab", Recipient: "0xbb", ERC20Amount: "1", Attestor: "0x02"},
	})
	if err == nil {
		t.Fatal("two validators minting one lock to different addresses was accepted")
	}
	if !strings.Contains(err.Error(), "not on the same chain") {
		t.Fatalf("error = %v, want it to name the cause", err)
	}
}

func TestTwoValidatorsNamingDifferentAmountsIsRefused(t *testing.T) {
	err := checkAttestationsAgree([]attestationOut{
		{Validator: "a:9091", LockID: "ab", Recipient: "0xaa", ERC20Amount: "1", Attestor: "0x01"},
		{Validator: "b:9091", LockID: "ab", Recipient: "0xaa", ERC20Amount: "2", Attestor: "0x02"},
	})
	if err == nil {
		t.Fatal("two validators valuing one lock differently was accepted")
	}
}

// The one an operator hits by accident: naming the same node twice, or two
// nodes that were configured from the same keystore file. The contract counts
// distinct signers, so this is one signature dressed as two - and on a 2-of-3
// bridge it is a mint that cannot succeed.
func TestTheSameAttestorTwiceIsRefused(t *testing.T) {
	err := checkAttestationsAgree([]attestationOut{
		{Validator: "a:9091", LockID: "ab", Recipient: "0xaa", ERC20Amount: "1", Attestor: "0xAB"},
		{Validator: "b:9091", LockID: "ab", Recipient: "0xaa", ERC20Amount: "1", Attestor: "0xab"},
	})
	if err == nil {
		t.Fatal("one attestor counted twice was accepted, which cannot reach a threshold of 2")
	}
	if !strings.Contains(err.Error(), "DISTINCT") {
		t.Fatalf("error = %v, want it to explain that signers must be distinct", err)
	}
}

// Case must not decide agreement: the node returns lowercase hex and an
// operator may paste a checksummed address, and refusing that would be a false
// alarm on a set that is perfectly fine.
func TestRecipientComparisonIsCaseInsensitive(t *testing.T) {
	if err := checkAttestationsAgree([]attestationOut{
		{Validator: "a:9091", LockID: "ab", Recipient: "0xAaBb", ERC20Amount: "1", Attestor: "0x01"},
		{Validator: "b:9091", LockID: "ab", Recipient: "0xaabb", ERC20Amount: "1", Attestor: "0x02"},
	}); err != nil {
		t.Fatalf("a case difference in the recipient was treated as disagreement: %v", err)
	}
}

func TestASingleAttestationNeedsNoAgreement(t *testing.T) {
	if err := checkAttestationsAgree([]attestationOut{
		{Validator: "a:9091", LockID: "ab", Recipient: "0xaa", ERC20Amount: "1", Attestor: "0x01"},
	}); err != nil {
		t.Fatalf("a one-validator set was refused: %v", err)
	}
}
