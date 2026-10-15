// Package session holds the session locks of interactive agent sessions:
// an MCP server locks every repo of its workspace, and the daemon's
// autodoc scheduler skips repos whose lock is held (spec section 11.2).
package session

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// HeadlessEnv names the environment variable that marks processes started
// by an unattended autodoc run: when set, the MCP server takes no session
// locks (spec section 11.2).
const HeadlessEnv = "CODEDOC_HEADLESS"

// Headless reports whether this process runs under an unattended run.
func Headless() bool { return os.Getenv(HeadlessEnv) != "" }

// LockPath returns the session lock file of a repo:
// <dataDir>/<workspace>.locks/<repo>.
func LockPath(dataDir, workspace, repo string) string {
	return filepath.Join(dataDir, workspace+".locks", repo)
}

// Lock takes an exclusive non-blocking flock on the session lock of a repo,
// creating the file and its directory. The lock lasts until the returned
// file is closed. A lock held by another session fails with an error
// wrapping syscall.EWOULDBLOCK.
func Lock(dataDir, workspace, repo string) (*os.File, error) {
	p := LockPath(dataDir, workspace, repo)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, &os.PathError{Op: "flock", Path: p, Err: err}
	}
	return f, nil
}

// Held reports whether a session holds the lock of a repo. It tries the
// lock without blocking and releases it at once; a missing lock file means
// no session ever took it. The autodoc scheduler skips repos where it is
// true.
func Held(dataDir, workspace, repo string) (bool, error) {
	f, err := os.OpenFile(LockPath(dataDir, workspace, repo), os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
