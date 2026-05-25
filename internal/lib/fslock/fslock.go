// Package fslock implements mkdir-based locking for cross-process write
// coordination. Mirrors src/lib/fs-lock.ts.
//
// The contract: WithLock atomically acquires lockDir by creating it; runs
// fn; releases by removing it. If lockDir already exists (held by another
// process), polls every 25ms until acquired or timeout.
package fslock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	defaultTimeout = 5 * time.Second
	pollInterval   = 25 * time.Millisecond
)

// LockBusyError is returned when the lock could not be acquired before
// the timeout elapsed. Satisfies internal/lib/errors.RufioError so
// HandleError maps it to exit code 1 (per design §4.A roster).
type LockBusyError struct{ LockDir string }

func (e *LockBusyError) Error() string {
	return fmt.Sprintf("failed to acquire lock at %s after timeout", e.LockDir)
}

func (e *LockBusyError) ExitCode() int { return 1 }

// WithLock acquires lockDir, runs fn, releases lockDir. Returns whatever
// fn returns. If timeout is zero, defaults to 5s.
//
// Generic over the function's return type so callers don't need to wrap
// in interface{}.
func WithLock[T any](lockDir string, timeout time.Duration, fn func() (T, error)) (T, error) {
	var zero T
	if timeout == 0 {
		timeout = defaultTimeout
	}
	if err := os.MkdirAll(filepath.Dir(lockDir), 0o755); err != nil {
		return zero, err
	}
	start := time.Now()
	for {
		err := os.Mkdir(lockDir, 0o755)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return zero, err
		}
		if time.Since(start) > timeout {
			return zero, &LockBusyError{LockDir: lockDir}
		}
		time.Sleep(pollInterval)
	}
	defer os.RemoveAll(lockDir)
	return fn()
}
