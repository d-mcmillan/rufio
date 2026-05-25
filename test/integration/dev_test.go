package integration_test

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// devProcess wraps a long-running `rufio dev` subprocess + stdout/stderr
// drainage. Caller is expected to terminate it via Kill or signal.
type devProcess struct {
	cmd       *exec.Cmd
	stdoutBuf *strings.Builder
	stderrBuf *strings.Builder
	wg        *sync.WaitGroup
	mu        *sync.Mutex
}

func spawnDev(t *testing.T, workdir string) *devProcess {
	t.Helper()
	bin, err := testutil.BuildBinary()
	if err != nil {
		t.Fatalf("BuildBinary: %v", err)
	}
	cmd := exec.Command(bin, "dev")
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	stdoutPipe, _ := cmd.StdoutPipe()
	stderrPipe, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	dp := &devProcess{
		cmd:       cmd,
		stdoutBuf: &strings.Builder{},
		stderrBuf: &strings.Builder{},
		wg:        &sync.WaitGroup{},
		mu:        &sync.Mutex{},
	}
	dp.wg.Add(2)
	go drainPipe(stdoutPipe, dp.stdoutBuf, dp.mu, dp.wg)
	go drainPipe(stderrPipe, dp.stderrBuf, dp.mu, dp.wg)
	return dp
}

func drainPipe(p io.ReadCloser, sink *strings.Builder, mu *sync.Mutex, wg *sync.WaitGroup) {
	defer wg.Done()
	scanner := bufio.NewScanner(p)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		mu.Lock()
		sink.WriteString(scanner.Text())
		sink.WriteByte('\n')
		mu.Unlock()
	}
}

func (d *devProcess) wait() (stdout, stderr string, code int) {
	_ = d.cmd.Wait()
	d.wg.Wait()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cmd.ProcessState != nil {
		code = d.cmd.ProcessState.ExitCode()
	}
	return d.stdoutBuf.String(), d.stderrBuf.String(), code
}

func (d *devProcess) signal(sig os.Signal) {
	_ = d.cmd.Process.Signal(sig)
}

func TestRufioDev_AddEventUnderGiven(t *testing.T) {
	workdir := mkProject(t)
	if r := testutil.RunCLI(t, []string{"init"}, workdir, nil); r.Code != 0 {
		t.Fatalf("init: %s", r.Stderr)
	}

	dev := spawnDev(t, workdir)
	time.Sleep(400 * time.Millisecond) // let watcher initialise

	if err := os.WriteFile(filepath.Join(workdir, "given", "x.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond) // chokidar/fsnotify latency

	dev.signal(syscall.SIGINT)
	stdout, _, code := dev.wait()
	if code != 0 {
		t.Errorf("expected exit 0 on SIGINT, got %d", code)
	}
	if !regexp.MustCompile(`add\s+given/x\.md`).MatchString(stdout) {
		t.Errorf("expected 'add given/x.md' in stdout; got:\n%s", stdout)
	}
}

func TestRufioDev_ChangeEventOnExistingFile(t *testing.T) {
	workdir := mkProject(t)
	if r := testutil.RunCLI(t, []string{"init"}, workdir, nil); r.Code != 0 {
		t.Fatalf("init: %s", r.Stderr)
	}
	// Pre-create file BEFORE the watcher starts so add doesn't fire on it.
	if err := os.WriteFile(filepath.Join(workdir, "given", "y.md"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dev := spawnDev(t, workdir)
	time.Sleep(400 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(workdir, "given", "y.md"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)

	dev.signal(syscall.SIGINT)
	stdout, _, _ := dev.wait()
	if !regexp.MustCompile(`change\s+given/y\.md`).MatchString(stdout) {
		t.Errorf("expected 'change given/y.md' in stdout; got:\n%s", stdout)
	}
}

func TestRufioDev_OutboxAdd(t *testing.T) {
	workdir := mkProject(t)
	if r := testutil.RunCLI(t, []string{"init"}, workdir, nil); r.Code != 0 {
		t.Fatalf("init: %s", r.Stderr)
	}

	dev := spawnDev(t, workdir)
	time.Sleep(400 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(workdir, "live", "outbox", "msg-001.gdl"), []byte("@thought|x:1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)

	dev.signal(syscall.SIGINT)
	stdout, _, _ := dev.wait()
	if !regexp.MustCompile(`add\s+live/outbox/msg-001\.gdl`).MatchString(stdout) {
		t.Errorf("expected 'add live/outbox/msg-001.gdl' in stdout; got:\n%s", stdout)
	}
}

func TestRufioDev_DoesNotWatchInternalOrRufio(t *testing.T) {
	workdir := mkProject(t)
	if r := testutil.RunCLI(t, []string{"init"}, workdir, nil); r.Code != 0 {
		t.Fatalf("init: %s", r.Stderr)
	}
	_ = os.MkdirAll(filepath.Join(workdir, "internal"), 0o755)

	dev := spawnDev(t, workdir)
	time.Sleep(400 * time.Millisecond)

	_ = os.WriteFile(filepath.Join(workdir, "internal", "secret.md"), []byte("private\n"), 0o644)
	_ = os.WriteFile(filepath.Join(workdir, ".rufio", "snapshots", "marker"), []byte("x\n"), 0o644)
	time.Sleep(700 * time.Millisecond)

	dev.signal(syscall.SIGINT)
	stdout, _, _ := dev.wait()
	if strings.Contains(stdout, "internal/") {
		t.Errorf("internal/ events leaked: %s", stdout)
	}
	if strings.Contains(stdout, ".rufio/") {
		t.Errorf(".rufio/ events leaked: %s", stdout)
	}
}

func TestRufioDev_SIGINTExits0(t *testing.T) {
	workdir := mkProject(t)
	_ = testutil.RunCLI(t, []string{"init"}, workdir, nil)

	dev := spawnDev(t, workdir)
	time.Sleep(300 * time.Millisecond)
	dev.signal(syscall.SIGINT)
	_, _, code := dev.wait()
	if code != 0 {
		t.Errorf("expected exit 0 on SIGINT, got %d", code)
	}
}

func TestRufioDev_SIGTERMExits0(t *testing.T) {
	workdir := mkProject(t)
	_ = testutil.RunCLI(t, []string{"init"}, workdir, nil)

	dev := spawnDev(t, workdir)
	time.Sleep(300 * time.Millisecond)
	dev.signal(syscall.SIGTERM)
	_, _, code := dev.wait()
	if code != 0 {
		t.Errorf("expected exit 0 on SIGTERM, got %d", code)
	}
}

func TestRufioDev_RejectsUnknownFlag(t *testing.T) {
	workdir := mkProject(t)
	_ = testutil.RunCLI(t, []string{"init"}, workdir, nil)
	r := testutil.RunCLI(t, []string{"dev", "--bogus"}, workdir, nil)
	if r.Code != 2 {
		t.Errorf("exit code: got %d, want 2", r.Code)
	}
}

func TestRufioDev_RejectsJSON(t *testing.T) {
	// dev does not yet have a JSONL event-stream mode. Per CLAUDE.md
	// (no half-built features), --json must be rejected, not silently
	// no-op'd. Will be re-introduced when the daemon grows a JSONL mode.
	workdir := mkProject(t)
	_ = testutil.RunCLI(t, []string{"init"}, workdir, nil)
	r := testutil.RunCLI(t, []string{"dev", "--json"}, workdir, nil)
	if r.Code != 2 {
		t.Errorf("exit code: got %d, want 2", r.Code)
	}
	mustMatch(t, r.Stderr, `(?i)unknown flag.*--json`)
}

func TestRufioDev_NotInProject(t *testing.T) {
	orphan, _ := os.MkdirTemp("", "rufio-dev-orphan-")
	defer os.RemoveAll(orphan)

	r := testutil.RunCLI(t, []string{"dev"}, orphan, nil)
	if r.Code == 0 {
		t.Fatal("expected non-zero exit")
	}
	mustMatch(t, r.Stderr, `(?i)not inside a Rufio project`)
}
