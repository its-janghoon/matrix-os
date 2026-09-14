package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

// The key the CLI sends when ACLs are on.
//
// `matrixd -init` has always told the operator to "Pass it to the CLI with
// --api-key, or export MATRIX_ADMIN_API_KEY". Only the first half was true.
// Exporting the variable did nothing, no key was sent, the node answered
// Unauthenticated, and nothing in either message connected the two.
//
// It matters beyond the broken promise: a flag puts a secret in argv, where ps
// shows it to every other user on the box, and in shell history.

// resolvedAPIKey runs the root command through a no-op subcommand so its
// PersistentPreRunE fires, then reports what the CLI would actually send.
func resolvedAPIKey(t *testing.T, args ...string) string {
	t.Helper()
	root := NewRootCommand()
	root.AddCommand(&cobra.Command{
		Use:  "probe",
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	root.SetArgs(append([]string{"probe"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("probe: %v", err)
	}
	return root.PersistentFlags().Lookup("api-key").Value.String()
}

func TestAPIKeyComesFromTheEnvironmentWhenTheFlagIsAbsent(t *testing.T) {
	t.Setenv(apiKeyEnv, "from-env")
	if got := resolvedAPIKey(t); got != "from-env" {
		t.Errorf("expected the CLI to use $%s; sent %q", apiKeyEnv, got)
	}
}

func TestAPIKeyFlagWinsOverTheEnvironment(t *testing.T) {
	t.Setenv(apiKeyEnv, "from-env")
	if got := resolvedAPIKey(t, "--api-key", "from-flag"); got != "from-flag" {
		t.Errorf("an explicit --api-key must win over $%s; sent %q", apiKeyEnv, got)
	}
}

// An operator who passes --api-key="" is saying to send nothing. Substituting
// the environment there would make that unsayable, which is why the fallback
// checks whether the flag was CHANGED rather than whether it is empty.
func TestAnExplicitlyEmptyAPIKeyStaysEmpty(t *testing.T) {
	t.Setenv(apiKeyEnv, "from-env")
	if got := resolvedAPIKey(t, "--api-key", ""); got != "" {
		t.Errorf(`--api-key="" must send no key even with $%s set; sent %q`, apiKeyEnv, got)
	}
}

func TestNoFlagAndNoEnvironmentSendsNoKey(t *testing.T) {
	t.Setenv(apiKeyEnv, "")
	if got := resolvedAPIKey(t); got != "" {
		t.Errorf("expected no key; sent %q", got)
	}
}
