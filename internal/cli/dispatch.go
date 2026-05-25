// Package cli wires the rufio CLI subcommands. Single-binary architecture:
// every command lives in its own file in this package; main.go's only job
// is `fang.Execute(NewRootCmd(version))`.
//
// Error → exit-code dispatcher: HandleError prints `rufio <cmd>: <msg>` to
// stderr and exits with the typed exit code (UsageError=2, RufioError
// subclasses use their declared code, unknown errors=1). Each command's
// RunE calls HandleError on failure; HandleError calls os.Exit and never
// returns.
package cli

import (
	"errors"
	"fmt"
	"os"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
)

// HandleError prints the canonical "rufio <name>: <message>" line and
// exits with the appropriate code. Never returns.
//
// - UsageError → exit 2 (Unix convention for usage errors)
// - any other RufioError → its declared ExitCode()
// - any unknown error → exit 1
//
// Single sink: parseArgs and command bodies should both throw RufioErrors
// (UsageError for user mistakes, typed RufioErrors for failures); HandleError
// is the only place that adds the "rufio <cmd>: " prefix.
func HandleError(cmdName string, err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "rufio %s: %s\n", cmdName, err.Error())
	var rufio rufioerr.RufioError
	if errors.As(err, &rufio) {
		os.Exit(rufio.ExitCode())
	}
	os.Exit(1)
}
