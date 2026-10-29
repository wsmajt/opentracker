//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package resetcodex

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func acquireLock(dir string) (int, error) {
	p := filepath.Join(dir, "reset.lock")
	fd, err := unix.Open(p, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return -1, errors.New("cannot open reset lock")
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0077 != 0 {
		if closeErr := unix.Close(fd); closeErr != nil {
			return -1, errors.New("cannot close reset lock descriptor")
		}
		return -1, errors.New("reset lock is not a private regular file")
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if closeErr := unix.Close(fd); closeErr != nil {
			return -1, errors.New("cannot close reset lock descriptor")
		}
		return -1, errors.New("another reset operation is active; no request was sent")
	}
	if err := unix.Fsync(fd); err != nil {
		_ = unix.Flock(fd, unix.LOCK_UN)
		if closeErr := unix.Close(fd); closeErr != nil {
			return -1, errors.New("cannot close reset lock descriptor")
		}
		return -1, errors.New("cannot persist reset lock file")
	}
	if err := syncStateParents(dir); err != nil {
		_ = unix.Flock(fd, unix.LOCK_UN)
		if closeErr := unix.Close(fd); closeErr != nil {
			return -1, errors.New("cannot close reset lock descriptor")
		}
		return -1, errors.New("cannot persist reset lock directory")
	}
	return fd, nil
}

func syncStateParents(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		fd, err := unix.Open(current, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		syncErr := unix.Fsync(fd)
		closeErr := unix.Close(fd)
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
	}
}

func releaseLock(fd int) error {
	unlockErr := unix.Flock(fd, unix.LOCK_UN)
	closeErr := unix.Close(fd)
	if unlockErr != nil || closeErr != nil {
		return errors.New("reset lock cleanup failed")
	}
	return nil
}

func loadJournal(path string) (journal, bool, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err == unix.ENOENT {
		return journal{}, false, nil
	}
	if err != nil {
		return journal{}, false, errors.New("cannot safely read reset state")
	}
	f := os.NewFile(uintptr(fd), path)
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0077 != 0 {
		if closeErr := f.Close(); closeErr != nil {
			return journal{}, false, errors.New("cannot close reset state file")
		}
		return journal{}, false, errors.New("reset state is not a private regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, 64<<10))
	closeErr := f.Close()
	if err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil || len(b) >= 64<<10 {
		return journal{}, false, errors.New("cannot read reset state")
	}
	var entry journal
	if json.Unmarshal(b, &entry) != nil || !validJournal(entry) {
		return journal{}, false, errors.New("reset state is corrupt; manual intervention required")
	}
	return entry, true, nil
}

func saveJournal(path string, entry journal) (retErr error) {
	b, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	var nonce [8]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".reset-"+hex.EncodeToString(nonce[:])+".tmp")
	fd, err := unix.Open(tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), tmp)
	renamed, closed := false, false
	defer func() {
		if !closed {
			if closeErr := f.Close(); retErr == nil {
				retErr = closeErr
			}
		}
		if !renamed {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	renamed = true
	dfd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	fsyncErr := unix.Fsync(dfd)
	closeErr := unix.Close(dfd)
	if fsyncErr != nil {
		return fsyncErr
	}
	if closeErr != nil {
		return closeErr
	}
	return nil
}
