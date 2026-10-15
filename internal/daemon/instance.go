package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrRunning reports that another daemon serves the same data directory.
var ErrRunning = errors.New("daemon: another codedoc daemon is running")

// LockInstance takes the single-instance lock of a data directory, an
// exclusive flock of <dir>/daemon.lock held until release is called (or
// the process exits). It fails at once, with ErrRunning, while another
// process holds it.
func LockInstance(dir string) (release func() error, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "daemon.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w (lock %s)", ErrRunning, path)
		}
		return nil, err
	}
	return f.Close, nil
}
