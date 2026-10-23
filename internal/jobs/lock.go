package jobs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// ErrBusy means another job holds the job lock.
var ErrBusy = errors.New("jobs: another job runs")

// TryLock takes the job lock: flock LOCK_EX|LOCK_NB on LockPath (made 0644, its directories 0755). It is ErrBusy
// while another job holds it. Closing the file – and of every process it was handed on to – releases it.
func (s Store) TryLock() (*os.File, error) {
	if err := mkdirs(filepath.Dir(s.LockPath)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.LockPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	switch {
	case errors.Is(err, fs.ErrExist):
		f, err = os.OpenFile(s.LockPath, os.O_RDWR, 0)
	case err == nil:
		err = f.Chmod(0o644) // OpenFile's mode is subject to the umask; the views of every user read the file
	}
	if err != nil {
		if f != nil {
			f.Close()
		}
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return f, nil
}

// Locked reports whether a job holds the lock. It opens the file read-only: a missing file is free, and is not
// created.
func (s Store) Locked() bool {
	f, err := os.Open(s.LockPath)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return errors.Is(err, unix.EWOULDBLOCK)
	}
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	return false
}

// holder is the id of the job that holds the lock: what the lock file names while the lock is held; "" when it is
// free.
func (s Store) holder() string {
	if !s.Locked() {
		return ""
	}
	f, err := os.Open(s.LockPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	id, _ := named(f)
	return id
}

// holds reports an error unless lock is the lock file and holds the lock (flock on a descriptor that holds it
// already succeeds; on a free lock it takes it).
func (s Store) holds(lock *os.File) error {
	if lock == nil {
		return errors.New("jobs: no job lock")
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return ErrBusy
		}
		return fmt.Errorf("jobs: the job lock: %w", err)
	}
	return sameFile(lock, s.LockPath)
}

// claim names job id in the lock file, through lock: the id and a newline.
func claim(lock *os.File, id string) error {
	line := []byte(id + "\n")
	if _, err := lock.WriteAt(line, 0); err != nil { // all ids have one length: no moment without a name
		return fmt.Errorf("jobs: the job lock: %w", err)
	}
	if err := lock.Truncate(int64(len(line))); err != nil {
		return fmt.Errorf("jobs: the job lock: %w", err)
	}
	return nil
}

// named is the job id the lock file names, "" when it names none.
func named(lock io.ReaderAt) (string, error) {
	buf := make([]byte, 64)
	n, err := lock.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if id := strings.TrimSpace(string(buf[:n])); idPattern.MatchString(id) {
		return id, nil
	}
	return "", nil
}

// sameFile reports an error unless f is the file at path (the same device and inode).
func sameFile(f *os.File, path string) error {
	var have, want unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &have); err != nil {
		return fmt.Errorf("jobs: the job lock: %w", err)
	}
	if err := unix.Stat(path, &want); err != nil {
		return fmt.Errorf("jobs: the job lock: %w", err)
	}
	if have.Dev != want.Dev || have.Ino != want.Ino {
		return fmt.Errorf("jobs: the job lock: the descriptor is not %s", path)
	}
	return nil
}
