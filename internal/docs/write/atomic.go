package write

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/muralx/codedoc/internal/xdg"
)

// hook is called at each stage of Apply ("planned", under the file's
// lock, and "written") with the file's absolute path; tests use it to
// widen races.
var hook = func(_, _ string) {}

// renameFunc is os.Rename; tests replace it to simulate a crash between
// writing the temporary file and renaming it into place.
var renameFunc = os.Rename

// lockFile takes codedoc's exclusive lock of the file at abs, an flock
// of a lock file under the codedoc state directory named by a hash of
// the file's resolved path, so nothing is ever created next to the
// source. Every writer of docs holds it from reading the file to its
// last write, so writes of separate processes never interleave. The
// returned function releases it.
func lockFile(abs string) (func() error, error) {
	state, err := xdg.StateDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(state, "locks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(resolve(abs)))
	f, err := os.OpenFile(filepath.Join(dir, hex.EncodeToString(sum[:16])+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return func() error {
		return errors.Join(syscall.Flock(int(f.Fd()), syscall.LOCK_UN), f.Close())
	}, nil
}

// resolve returns abs with symbolic links resolved, as far as they
// exist: a file not created yet resolves through its directory.
func resolve(abs string) string {
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	if dir, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		return filepath.Join(dir, filepath.Base(abs))
	}
	return abs
}

// writeFile replaces the content of the file at abs (the target of a
// symbolic link), keeping its mode, or creates it with mode 0644. It
// writes a temporary file in the same directory, syncs it and renames
// it into place, so a crash leaves either the old or the new content,
// never a mix.
func writeFile(abs string, data []byte) error {
	abs = resolve(abs)
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(abs); err == nil {
		mode = fi.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(abs), "."+filepath.Base(abs)+".codedoc-*")
	if err != nil {
		return err
	}
	err = writeSynced(f, data, mode)
	if err == nil {
		err = renameFunc(f.Name(), abs)
	}
	if err != nil {
		return errors.Join(err, os.Remove(f.Name()))
	}
	return nil
}

// writeSynced writes data to f, sets its mode, syncs and closes it.
func writeSynced(f *os.File, data []byte, mode os.FileMode) error {
	_, err := f.Write(data)
	if err == nil {
		err = f.Chmod(mode)
	}
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}
