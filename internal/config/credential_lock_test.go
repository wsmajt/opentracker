package config

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func setCredentialLockHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return filepath.Join(home, ".config", "opentracker", ".credentials.lock")
}

func TestWithCredentialLockSerializesGoroutines(t *testing.T) {
	lockPath := setCredentialLockHome(t)
	var active atomic.Int32
	var overlap atomic.Bool
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := WithCredentialLock(func() error {
				if active.Add(1) != 1 {
					overlap.Store(true)
				}
				time.Sleep(10 * time.Millisecond)
				active.Add(-1)
				return nil
			}); err != nil {
				t.Errorf("WithCredentialLock() error = %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if overlap.Load() {
		t.Fatal("credential lock allowed callbacks to overlap")
	}

	info, err := os.Stat(lockPath)
	if err != nil {
		t.Fatalf("stat lock file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("lock file mode = %04o, want 0600", got)
	}
}

func TestWithCredentialLockCorrectsLockFileMode(t *testing.T) {
	lockPath := setCredentialLockHome(t)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := WithCredentialLock(func() error { return nil }); err != nil {
		t.Fatalf("WithCredentialLock() error = %v", err)
	}
	info, err := os.Stat(lockPath)
	if err != nil {
		t.Fatalf("stat lock file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("lock file mode = %04o, want 0600", got)
	}
}

func TestWithCredentialLockRejectsSymlink(t *testing.T) {
	lockPath := setCredentialLockHome(t)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, lockPath); err != nil {
		t.Fatal(err)
	}
	if err := WithCredentialLock(func() error { return nil }); err == nil {
		t.Fatal("WithCredentialLock() error = nil, want symlink rejection")
	}
}

func TestWithCredentialLockPropagatesCallbackError(t *testing.T) {
	setCredentialLockHome(t)
	want := errors.New("callback failed")
	err := WithCredentialLock(func() error { return want })
	if !errors.Is(err, want) {
		t.Errorf("WithCredentialLock() error = %v, want wrapped callback error", err)
	}
}
