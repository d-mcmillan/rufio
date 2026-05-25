package mcp

import (
	"errors"
	"fmt"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
)

// toolErr maps an internal/lib error to the error returned from a tool handler.
// Returning a non-nil error makes the SDK emit an MCP tool error WITHOUT
// terminating the server (the explicit anti-os.Exit requirement). Typed
// rufioerr values keep their message; the ExitCode (1=runtime/not-found,
// 2=usage/validation) is surfaced as a machine-readable prefix.
func toolErr(err error) error {
	if err == nil {
		return nil
	}
	var re rufioerr.RufioError
	if errors.As(err, &re) {
		return fmt.Errorf("[rufio:%d] %s", re.ExitCode(), re.Error())
	}
	return fmt.Errorf("[rufio:1] %s", err.Error())
}
