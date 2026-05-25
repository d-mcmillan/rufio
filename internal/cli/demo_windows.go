//go:build windows

package cli

import (
	"os/exec"
	"syscall"
)

// newProcessGroupAttr is a no-op on Windows — process groups in the
// POSIX sense don't exist. signalProcessGroup falls back to
// per-process delivery.
func newProcessGroupAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}

// signalProcessGroup signals the lead process directly. Windows does
// not have POSIX process groups; the orchestrator only spawns single
// processes today, so this is a best-effort fallback.
func signalProcessGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(sig)
}
