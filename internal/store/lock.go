package store

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// WriteLock takes the cross-process write lock of the database, an
// exclusive flock on "<db path>.lock", waiting until it is free for at
// most the lock timeout (see LockTimeout); a lock still held then fails
// with ErrLockTimeout naming the holder. Index
// writers (sync, freshness updates, resolution) hold it for their whole
// write phase so concurrent processes serialize their updates instead of
// interleaving them; readers never take it.
//
// Vector writes (symbol_vec, module_vec) are exempt: an embedding pass
// runs without the lock, since a model call can take seconds. Each
// vector is stored with the hash of the text it was computed from, only
// while its row exists (ReplaceSymbolVecs, UpsertModuleVec), so the writes
// are idempotent: two passes store the same vectors, and a vector a pass
// computed before a concurrent sync changed its row carries the old hash
// and is embedded again by the next pass. The returned function
// releases the lock. Locks taken through separate Store handles exclude
// each other even within one process. Before returning it finishes a
// bulk write a dead writer left unfinished (see SuspendFTS).
func (s *Store) WriteLock() (func() error, error) {
	unlock, err := LockPath(s.path)
	if err != nil {
		return nil, err
	}
	if err := s.recoverFTS(); err != nil {
		return nil, errors.Join(err, unlock())
	}
	return unlock, nil
}

// LockTimeoutEnv names the environment variable that sets how long
// WriteLock waits for the lock: a Go duration (90s, 2m) or a number of
// seconds. The default is DefaultLockTimeout.
const LockTimeoutEnv = "CODEDOC_LOCK_TIMEOUT"

// DefaultLockTimeout is how long WriteLock waits without LockTimeoutEnv.
const DefaultLockTimeout = 60 * time.Second

// ErrLockTimeout reports a write lock still held when the wait ended.
var ErrLockTimeout = errors.New("the database write lock is still held")

// LockTimeout returns the wait LockTimeoutEnv sets, else
// DefaultLockTimeout.
func LockTimeout() (time.Duration, error) {
	v := os.Getenv(LockTimeoutEnv)
	if v == "" {
		return DefaultLockTimeout, nil
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(n) * time.Second, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%s=%q is no duration (90s, 2m) or number of seconds", LockTimeoutEnv, v)
	}
	return d, nil
}

// LockPath takes the write lock of the database at path, as WriteLock
// does, without opening the database: deleting a database holds it so
// no writer is midway through an update of the file. The holder writes
// its process id and command line into the lock file, so a writer that
// gives up can name it.
func LockPath(path string) (func() error, error) {
	timeout, err := LockTimeout()
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := waitFlock(f, timeout); err != nil {
		holder := lockHolder(f)
		_ = f.Close()
		if errors.Is(err, ErrLockTimeout) {
			return nil, fmt.Errorf("%w after %v%s; retry later or raise %s", err, timeout, holder, LockTimeoutEnv)
		}
		return nil, err
	}
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(fmt.Sprintf("%d %s\n", os.Getpid(), strings.Join(os.Args, " "))), 0)
	}
	return func() error {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return err
	}, nil
}

// waitFlock takes an exclusive flock on f, retrying a held lock with a
// growing pause until timeout has passed; then it returns
// ErrLockTimeout.
func waitFlock(f *os.File, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	pause := 10 * time.Millisecond
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		left := time.Until(deadline)
		if left <= 0 {
			return ErrLockTimeout
		}
		time.Sleep(min(pause, left))
		pause = min(2*pause, 500*time.Millisecond)
	}
}

// lockHolder describes the process the lock file names, as
// " (held by pid N: command)", or "" when it names none.
func lockHolder(f *os.File) string {
	b := make([]byte, 512)
	n, _ := f.ReadAt(b, 0)
	line, _, _ := strings.Cut(string(b[:n]), "\n")
	pid, cmd, ok := strings.Cut(strings.TrimSpace(line), " ")
	if _, err := strconv.Atoi(pid); err != nil || !ok {
		return ""
	}
	return fmt.Sprintf(" (held by pid %s: %s)", pid, cmd)
}
