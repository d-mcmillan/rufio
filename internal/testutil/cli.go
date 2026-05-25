// Package testutil holds helpers for integration tests. The runner builds
// the rufio binary once per test process (via TestMain in each integration
// test package) and execs it against per-test temp workdirs.
//
// Mirrors test/helpers/cli.ts from the TS week-1 reference.
package testutil

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// CLIResult captures the stdout, stderr, and exit code of a rufio invocation.
type CLIResult struct {
	Stdout string
	Stderr string
	Code   int
}

var (
	binaryPath  string
	buildOnce   sync.Once
	buildErr    error
	binaryDir   string
	binaryDirMu sync.Mutex
)

// repoRoot finds the rufio repo root by walking up from this source file's
// directory until rufio.gdl is NOT present (we are in a Go module, so look
// for go.mod). Sufficient because testutil/cli.go has a stable location
// at internal/testutil/cli.go.
func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// BuildBinary builds (or returns the cached path of) the rufio binary used
// for integration tests. Idempotent — uses sync.Once.
func BuildBinary() (string, error) {
	buildOnce.Do(func() {
		binaryDirMu.Lock()
		dir, err := os.MkdirTemp("", "rufio-bin-")
		binaryDirMu.Unlock()
		if err != nil {
			buildErr = err
			return
		}
		binaryDir = dir
		binaryPath = filepath.Join(dir, "rufio")
		root := repoRoot()
		cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/rufio")
		cmd.Dir = root
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			buildErr = &buildError{stderr: stderr.String(), wrapped: err}
		}
	})
	return binaryPath, buildErr
}

type buildError struct {
	stderr  string
	wrapped error
}

func (e *buildError) Error() string {
	return "build failed: " + e.wrapped.Error() + "\n" + e.stderr
}

// RunCLI execs the rufio binary in workdir with args + extraEnv.
// Defaults set: NO_COLOR=1 (test assertions don't strip ANSI escapes) and
// RUFIO_FULL_IDS=1 (text-mode IDs render fully, preserving the pre-H1
// "grep stdout for fullID" invariant most integration tests rely on).
// Tests that specifically verify short-ID rendering opt out by passing
// RUFIO_FULL_IDS="" in extraEnv (later append wins).
func RunCLI(t *testing.T, args []string, workdir string, extraEnv map[string]string) CLIResult {
	return RunCLIWithStdin(t, args, workdir, extraEnv, "")
}

// RunCLIWithStdin is RunCLI with an attached stdin buffer. Used by
// import-style commands that read from stdin (rufio import --format=jsonl).
func RunCLIWithStdin(t *testing.T, args []string, workdir string, extraEnv map[string]string, stdin string) CLIResult {
	t.Helper()
	bin, err := BuildBinary()
	if err != nil {
		t.Fatalf("BuildBinary: %v", err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "RUFIO_FULL_IDS=1")
	for k, v := range extraEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if stdin != "" {
		cmd.Stdin = bytes.NewReader([]byte(stdin))
	}
	runErr := cmd.Run()
	code := 0
	if exitErr, ok := runErr.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if runErr != nil {
		code = -1
	}
	return CLIResult{Stdout: stdout.String(), Stderr: stderr.String(), Code: code}
}
