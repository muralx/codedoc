package main

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/muralx/codedoc/internal/daemon"
	"github.com/muralx/codedoc/internal/store"
)

// withRebuildLocks runs remove, which deletes the database at path, only
// while no daemon serves its data directory: a daemon keeps the file
// open and would go on serving, and answering fresh from, the deleted
// one. It holds the daemon's instance lock, so none starts meanwhile,
// and the database's write lock, so no writer is midway through an
// update.
func withRebuildLocks(path string, remove func() error) (err error) {
	dir := filepath.Dir(path)
	release, err := daemon.LockInstance(dir)
	if errors.Is(err, daemon.ErrRunning) {
		return fmt.Errorf("--rebuild: the codedoc daemon serves %s and keeps the database open; "+
			"stop it first (systemctl --user stop codedoc, or kill the codedoc daemon process), "+
			"rebuild, then start it again", dir)
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	unlock, err := store.LockPath(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return remove()
}
