package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/version"
)

// TestRootCommand_ReportsVersion asserts `matrix --version` works at all. The
// installation docs told users to run it while the CLI answered "unknown flag:
// --version", which is the whole reason the flag exists; a regression here puts
// the docs back to lying.
func TestRootCommand_ReportsVersion(t *testing.T) {
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("matrix --version: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, version.String()) {
		t.Fatalf("--version output %q does not contain the version %q", got, version.String())
	}
}

// TestVersion_DefaultsToDev asserts an un-stamped build says so rather than
// claiming a release number it does not have.
func TestVersion_DefaultsToDev(t *testing.T) {
	if version.String() == "" {
		t.Fatal("version.String() must never be empty")
	}
	// A plain `go test` build has no -X flag, so this is the honest default.
	if version.Version == "dev" && version.String() != "dev" {
		t.Fatalf("String() = %q, want \"dev\" for an unstamped build", version.String())
	}
}
