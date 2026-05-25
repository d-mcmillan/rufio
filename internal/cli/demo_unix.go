//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
)

// newProcessGroupAttr returns a SysProcAttr that puts the spawned
// process in its own process group. signalProcessGroup then targets
// `-pgid` to deliver the signal to the whole group, catching any
// helpers the child may have spawned. On platforms without process
// groups (Windows) we fall back to signalling the lead process only.
func newProcessGroupAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// signalProcessGroup delivers sig to the process group led by cmd.
// Falls back to the lead process if the group lookup fails (the
// process may have already exited).
func signalProcessGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		// Process may already be gone, or never made it into its
		// own group. Fall through to direct signal.
		return cmd.Process.Signal(sig)
	}
	return syscall.Kill(-pgid, sig)
}
