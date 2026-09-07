package cli

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// TestADeadlineIsNotReportedAsAFailedTransfer is the regression guard for the
// message that caused a double payment. A DeadlineExceeded means the client
// stopped waiting, not that the transfer was rejected, and the message has to
// say so - and must not invite a re-run, which is what signed a second transfer
// at the same nonce.
func TestADeadlineIsNotReportedAsAFailedTransfer(t *testing.T) {
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	tx := &token.Transaction{From: acct.PublicKey, To: "recipient", Amount: 250, Nonce: 7}
	opts := &globalOptions{Addr: "127.0.0.1:9091", Timeout: 30 * time.Second}

	err = transferSubmitError(opts, tx, status.Error(codes.DeadlineExceeded, "context deadline exceeded"))
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()

	for _, want := range []string{
		"NOT rejected",    // it did not fail
		"mempool",         // where it actually is
		"nonce  7",        // which nonce is held
		"Do NOT run this", // do not re-sign
		"matrix tx list",  // how to check instead
		"30s",             // the wait actually used
		"recipient",       // who it was going to
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message is missing %q:\n%s", want, msg)
		}
	}
	// The bare gRPC rendering is what made a committed transfer look failed.
	if strings.Contains(msg, "DeadlineExceeded:") {
		t.Fatalf("message still leads with the raw gRPC code:\n%s", msg)
	}
}

// TestOtherTransferErrorsStillMapNormally: only the deadline gets the special
// treatment. A real rejection must still read as a rejection.
func TestOtherTransferErrorsStillMapNormally(t *testing.T) {
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	tx := &token.Transaction{From: acct.PublicKey, To: "recipient", Amount: 250, Nonce: 7}
	opts := &globalOptions{Addr: "127.0.0.1:9091"}

	err = transferSubmitError(opts, tx,
		status.Error(codes.FailedPrecondition, "consensus: sender nonce already used"))
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "nonce already used") {
		t.Fatalf("the real reason was lost:\n%s", msg)
	}
	if strings.Contains(msg, "NOT rejected") {
		t.Fatalf("a genuine rejection was reported as still pending:\n%s", msg)
	}
}

// TestTheDefaultWaitOutlastsACommit. The number itself is the fix: a wait
// shorter than a commit turns every slow block into an apparent failure.
func TestTheDefaultWaitOutlastsACommit(t *testing.T) {
	if defaultTimeout < 30*time.Second {
		t.Fatalf("defaultTimeout is %s; a signed transfer waits on consensus, and a wait "+
			"shorter than a commit reports a committed transfer as a failure", defaultTimeout)
	}
}
