package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestVersionSubcommand_PrintsCanonicalForm asserts `rufio version`
// prints the exact same canonical form as Cobra's --version flag:
// "rufio version <version>\n". The two surfaces MUST stay byte-identical
// so a future format change touches only one place.
func TestVersionSubcommand_PrintsCanonicalForm(t *testing.T) {
	root := NewRootCmd("v1.0.6.2")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := strings.TrimRight(out.String(), "\n")
	want := "rufio version v1.0.6.2"
	if got != want {
		t.Fatalf("version subcommand: got %q, want %q", got, want)
	}
}

// TestVersionSubcommand_MatchesVersionFlag asserts the subcommand and
// the --version flag print byte-identical output (the symmetry contract
// the subcommand was added to honour).
func TestVersionSubcommand_MatchesVersionFlag(t *testing.T) {
	want := captureCommand(t, "v1.0.6.2", "--version")
	got := captureCommand(t, "v1.0.6.2", "version")

	if got != want {
		t.Fatalf("subcommand vs --version flag drift:\n  subcommand: %q\n  --version:  %q", got, want)
	}
}

// TestVersionSubcommand_DevDefault asserts the dev-build default
// propagates correctly when no -ldflags=-X main.version=... is injected.
func TestVersionSubcommand_DevDefault(t *testing.T) {
	got := captureCommand(t, "dev", "version")
	want := "rufio version dev\n"
	if got != want {
		t.Fatalf("dev-default: got %q, want %q", got, want)
	}
}

func captureCommand(t *testing.T, version string, args ...string) string {
	t.Helper()
	root := NewRootCmd(version)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("execute(%v): %v", args, err)
	}
	return out.String()
}
