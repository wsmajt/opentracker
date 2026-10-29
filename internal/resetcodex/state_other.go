//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package resetcodex

import "errors"

func acquireLock(string) (int, error) {
	return -1, errors.New("safe reset state locking is unavailable on this platform")
}

func releaseLock(int) error { return nil }

func syncStateParents(string) error {
	return errors.New("durable reset state is unavailable on this platform")
}

func loadJournal(string) (journal, bool, error) {
	return journal{}, false, errors.New("safe reset state access is unavailable on this platform")
}

func saveJournal(string, journal) error {
	return errors.New("safe reset state access is unavailable on this platform")
}
