// Command rufio is the substrate CLI entrypoint. It dispatches to per-command
// handlers in internal/cli, which compose the foundation libs in
// internal/lib. The TUI subcommand (week 3) plugs in alongside via Bubble Tea.
//
// Note (followup GO-P3-1): we use plain Cobra here instead of
// charmbracelet/fang because Fang's transitive dep on charmbracelet/x/ansi
// is currently incompatible with cellbuf's expected API. The visual
// difference is the styled --help; the dispatch contract (subcommands,
// exit codes, RunE) is identical. Re-evaluate Fang once its dep tree is
// consistent again.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/d-mcmillan/rufio/internal/cli"
	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
)

// version is overridden via -ldflags at build time.
//
// Default is "dev" so a local `go build ./cmd/rufio/` reports honestly
// instead of claiming to be a stale tag. Release builds inject the
// real tag via:
//
//	go build -ldflags="-X main.version=v1.0.6" -o rufio ./cmd/rufio/
//
// CI release workflow + the global-binary runbook (CLAUDE.md
// STALE-GLOBAL-BINARY TRAP) MUST pass the flag.
var version = "dev"

func main() {
	root := cli.NewRootCmd(version)
	err := root.Execute()
	if err == nil {
		return
	}
	// Each command's RunE prints + exits via HandleError on its own typed
	// errors, so we usually never see typed RufioErrors here. But in case
	// Cobra surfaces an unknown-flag or pflag error before any RunE runs,
	// give the user a clean line and exit 2 (usage error per Unix
	// convention).
	var rufio rufioerr.RufioError
	if errors.As(err, &rufio) {
		os.Exit(rufio.ExitCode())
	}
	// Cobra prints unknown-flag errors with "Error: ..." to stderr by
	// default. Since root has SilenceErrors=true, that didn't happen, so
	// we print our own canonical line.
	fmt.Fprintf(os.Stderr, "rufio: %s\n", err.Error())
	os.Exit(2)
}
