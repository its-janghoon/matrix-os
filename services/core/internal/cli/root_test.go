package cli

import (
	"bytes"
	"strings"
	"testing"
)

// executeArgs runs the root command with the given args, capturing stdout+stderr
// into a single buffer, and returns the combined output and any error.
func executeArgs(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCommand()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

// TestRootHelpListsSubcommands asserts `matrix --help` lists every top-level
// subcommand with usage text.
func TestRootHelpListsSubcommands(t *testing.T) {
	out, err := executeArgs(t, "--help")
	if err != nil {
		t.Fatalf("--help returned error: %v", err)
	}
	for _, want := range []string{"status", "health", "provider", "job", "balance", "tx", "wallet"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected --help output to list subcommand %q; got:\n%s", want, out)
		}
	}
}

// TestPersistentFlagsRegistered asserts the global flags are wired on the root.
func TestPersistentFlagsRegistered(t *testing.T) {
	root := NewRootCommand()
	pf := root.PersistentFlags()
	for _, name := range []string{"addr", "api-key", "timeout", "json"} {
		if pf.Lookup(name) == nil {
			t.Errorf("expected persistent flag --%s to be registered", name)
		}
	}
	if got := pf.Lookup("addr").DefValue; got != defaultAddr {
		t.Errorf("expected --addr default %q, got %q", defaultAddr, got)
	}
}

// TestSubcommandHelp asserts nested command help renders (job/wallet/provider/tx).
func TestSubcommandHelp(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"wallet", "--help"}, []string{"create", "show", "balance", "transfer"}},
		{[]string{"job", "--help"}, []string{"submit", "get", "list", "complete", "cancel"}},
		{[]string{"provider", "--help"}, []string{"register", "list"}},
		{[]string{"tx", "--help"}, []string{"get", "list"}},
	}
	for _, tc := range cases {
		out, err := executeArgs(t, tc.args...)
		if err != nil {
			t.Fatalf("%v returned error: %v", tc.args, err)
		}
		for _, w := range tc.want {
			if !strings.Contains(out, w) {
				t.Errorf("%v help missing %q; got:\n%s", tc.args, w, out)
			}
		}
	}
}

// TestArgValidationErrors asserts required-flag validation fires before any
// network call (so these run without a server).
func TestArgValidationErrors(t *testing.T) {
	cases := [][]string{
		{"provider", "register"},                    // missing --id/--capacity/--price
		{"job", "submit", "--provider", "p"},         // missing --buyer/--units
		{"job", "get"},                               // missing --id
		{"balance"},                                  // missing --account
		{"wallet", "transfer", "--amount", "1"},      // missing --to
	}
	for _, args := range cases {
		_, err := executeArgs(t, args...)
		if err == nil {
			t.Errorf("expected validation error for args %v, got nil", args)
		}
	}
}

// TestUnknownCommand asserts an unknown subcommand is an error.
func TestUnknownCommand(t *testing.T) {
	if _, err := executeArgs(t, "does-not-exist"); err == nil {
		t.Error("expected error for unknown subcommand, got nil")
	}
}
