package autodoc

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/muralx/codedoc/internal/docs/write"
)

// markerName is the file in a run's journal directory that marks a run
// not finished yet: written before claude starts, removed once the run
// committed its files or restored them. Its owner holds an exclusive
// flock on it while the run is live, so a marker nobody holds locked was
// left by a run that died midway.
const markerName = "autodoc.running"

// marker is a live run's locked marker.
type marker struct {
	path string
	f    *os.File
}

// markRun creates and locks the marker of run id. It locks a temporary
// file before moving it into place, so nobody ever sees the marker
// unlocked while its run lives.
func markRun(id string) (*marker, error) {
	dir, err := write.RunDir(id)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, markerName+".*")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, markerName)
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, errors.Join(err, f.Close(), os.Remove(f.Name()))
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return nil, errors.Join(err, f.Close(), os.Remove(f.Name()))
	}
	return &marker{path: path, f: f}, nil
}

// done removes the marker: the run ended cleanly.
func (m *marker) done() error {
	if err := os.Remove(m.path); !os.IsNotExist(err) {
		return err
	}
	return nil
}

// release unlocks the marker, leaving it in place unless done was
// called.
func (m *marker) release() error { return m.f.Close() }

// RecoverLeftovers restores the journal of every run that died before
// its clean end, a killed codedoc or a crashed machine, logging each
// file it put back and each it kept because it changed since. Runs
// still live are left alone. It runs before anything else of an autodoc
// run, so the run starts from the tree as it was before the dead one,
// and newest journal first: the groups of a held run (see RunHeld) wrote
// one after the other, each on top of the earlier ones.
func RecoverLeftovers(log Logger) {
	runs, err := write.RunsDir()
	if err != nil {
		log.Printf("autodoc: looking for unfinished runs: %v", err)
		return
	}
	list, err := os.ReadDir(runs)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("autodoc: looking for unfinished runs: %v", err)
		}
		return
	}
	for _, e := range slices.Backward(list) {
		if e.IsDir() && strings.HasPrefix(e.Name(), "autodoc-") {
			if err := recoverRun(e.Name(), log); err != nil {
				log.Printf("autodoc: restoring unfinished run %s: %v", e.Name(), err)
			}
		}
	}
}

// recoverRun restores run id when it left an unlocked marker.
func recoverRun(id string, log Logger) error {
	dir, err := write.RunDir(id)
	if err != nil {
		return err
	}
	f, err := os.Open(filepath.Join(dir, markerName))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil // live
		}
		return err
	}
	files, err := write.Files(id)
	if err != nil {
		return err
	}
	kept, err := write.Restore(id, false)
	if err != nil {
		return err
	}
	logRecovered(id, files, kept, log)
	return (&marker{path: filepath.Join(dir, markerName)}).done()
}

// logRecovered logs what restoring run id did to each of its files.
func logRecovered(id string, files, kept []string, log Logger) {
	for _, p := range files {
		if slices.Contains(kept, p) {
			log.Printf("autodoc: kept %s, edited after unfinished run %s (its original stays in the journal)", p, id)
			continue
		}
		log.Printf("autodoc: restored %s from unfinished run %s", p, id)
	}
}
