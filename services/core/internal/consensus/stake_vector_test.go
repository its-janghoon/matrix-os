package consensus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pinning a browser's spelling of a stake recipient to this one.
//
// A recipient is PARSED, not matched against a list. A prefix that differs by a
// character does not fail - it becomes a transfer to an ordinary account whose
// name happens to look like an operation, and the money moves to an account
// nobody holds a key for. Nothing anywhere reports it.
//
// So the web client's strings are generated from here rather than typed from
// memory. Regenerate with `go test ./internal/consensus -run TestWriteStakeRecipientVectors`.
func TestWriteStakeRecipientVectors(t *testing.T) {
	accounts := []string{
		// The two account kinds, since a wallet-held provider is an address and
		// the older ids are still valid.
		"eth:0x00000000000000000000000000000000000000aa",
		strings.Repeat("ab", 32),
		// Nothing stops an id containing characters a naive join would mangle.
		"eth:0xAbCdEf0123456789aBcDeF0123456789AbCdEf01",
	}

	type vector struct {
		Account  string `json:"account"`
		Bond     string `json:"bond"`
		Withdraw string `json:"withdraw"`
	}
	out := make([]vector, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, vector{
			Account:  a,
			Bond:     BondAccount(a),
			Withdraw: WithdrawRecipient(a),
		})
	}

	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body = append(body, '\n')

	path := filepath.Join("..", "..", "..", "..", "packages", "protocol", "src", "stake-recipients.json")
	if existing, err := os.ReadFile(path); err == nil && string(existing) == string(body) {
		return
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write vectors: %v", err)
	}
	t.Logf("wrote %d stake recipient vectors to %s", len(out), path)
}
