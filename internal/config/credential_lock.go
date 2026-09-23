package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

// WithCredentialLock runs fn while holding the process-wide OpenCode
// credential lock. Callers should keep network requests outside fn.
func WithCredentialLock(fn func() error) error {
	if fn == nil {
		return fmt.Errorf("credential lock callback is nil")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot get home dir: %w", err)
	}
	parentDir := filepath.Join(home, ".config")
	configDir := filepath.Join(parentDir, "opentracker")
	if err := ensureConfigDir(parentDir, configDir); err != nil {
		return fmt.Errorf("cannot create config dir: %w", err)
	}

	lockPath := filepath.Join(configDir, ".credentials.lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("cannot open credential lock: %w", err)
	}
	info, statErr := os.Lstat(lockPath)
	if statErr != nil {
		_ = file.Close()
		return fmt.Errorf("cannot inspect credential lock: %w", statErr)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		_ = file.Close()
		return fmt.Errorf("credential lock path is not a regular file")
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return fmt.Errorf("cannot set credential lock permissions: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("cannot close credential lock file: %w", err)
	}

	lock := flock.New(lockPath)
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("cannot acquire credential lock: %w", err)
	}

	callbackErr := fn()
	unlockErr := lock.Unlock()
	if callbackErr != nil {
		callbackErr = fmt.Errorf("credential lock callback: %w", callbackErr)
	}
	if unlockErr != nil {
		unlockErr = fmt.Errorf("cannot release credential lock: %w", unlockErr)
	}
	return errors.Join(callbackErr, unlockErr)
}
