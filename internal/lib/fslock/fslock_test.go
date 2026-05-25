package fslock

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWithLock_AcquiresAndReleases(t *testing.T) {
	dir := t.TempDir()
	lockDir := filepath.Join(dir, "x.lock")

	got, err := WithLock(lockDir, 0, func() (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ok" {
		t.Errorf("got %q, want %q", got, "ok")
	}
	if _, err := os.Stat(lockDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("lockDir should be removed; err=%v", err)
	}
}

func TestWithLock_SerialisesConcurrentAttempts(t *testing.T) {
	dir := t.TempDir()
	lockDir := filepath.Join(dir, "y.lock")

	var order []int
	var orderMu sync.Mutex
	record := func(n int) {
		orderMu.Lock()
		defer orderMu.Unlock()
		order = append(order, n)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine A holds lock for 50ms.
	go func() {
		defer wg.Done()
		if _, err := WithLock(lockDir, time.Second, func() (struct{}, error) {
			record(1)
			time.Sleep(50 * time.Millisecond)
			record(2)
			return struct{}{}, nil
		}); err != nil {
			t.Errorf("A: %v", err)
		}
	}()

	// Goroutine B starts ~10ms later, must wait for A to finish.
	time.Sleep(10 * time.Millisecond)
	go func() {
		defer wg.Done()
		if _, err := WithLock(lockDir, time.Second, func() (struct{}, error) {
			record(3)
			return struct{}{}, nil
		}); err != nil {
			t.Errorf("B: %v", err)
		}
	}()

	wg.Wait()

	want := []int{1, 2, 3}
	if len(order) != 3 || order[0] != want[0] || order[1] != want[1] || order[2] != want[2] {
		t.Errorf("got %v, want %v", order, want)
	}
}

func TestWithLock_ThrowsOnTimeout(t *testing.T) {
	dir := t.TempDir()
	lockDir := filepath.Join(dir, "z.lock")

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine A holds the lock for 200ms.
	go func() {
		defer wg.Done()
		_, _ = WithLock(lockDir, 5*time.Second, func() (struct{}, error) {
			time.Sleep(200 * time.Millisecond)
			return struct{}{}, nil
		})
	}()

	// Goroutine B tries with a 50ms timeout — should fail with LockBusyError.
	time.Sleep(20 * time.Millisecond)
	go func() {
		defer wg.Done()
		_, err := WithLock(lockDir, 50*time.Millisecond, func() (struct{}, error) {
			t.Error("B's fn should never run")
			return struct{}{}, nil
		})
		var lockErr *LockBusyError
		if !errors.As(err, &lockErr) {
			t.Errorf("got %T, want *LockBusyError", err)
		}
	}()

	wg.Wait()
}

func TestWithLock_ReleasesOnError(t *testing.T) {
	dir := t.TempDir()
	lockDir := filepath.Join(dir, "w.lock")

	innerErr := errors.New("boom")
	_, err := WithLock(lockDir, 0, func() (struct{}, error) {
		return struct{}{}, innerErr
	})
	if !errors.Is(err, innerErr) {
		t.Errorf("got %v, want innerErr", err)
	}
	if _, statErr := os.Stat(lockDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("lockDir should be removed even on error; statErr=%v", statErr)
	}
}
