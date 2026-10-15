package write

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/muralx/codedoc/internal/xdg"
)

// RunIDEnv names the environment variable that carries the unattended run
// id; when set, Apply journals every file before its first change.
const RunIDEnv = "CODEDOC_RUN_ID"

// manifestName is the journal's index of saved files, one JSON entry per
// line.
const manifestName = "manifest.jsonl"

// entry is one journaled file.
type entry struct {
	Root    string `json:"root"`    // absolute repo root
	Path    string `json:"path"`    // repo-relative path
	Existed bool   `json:"existed"` // false when the run created the file
	// Copy is the saved copy, relative to the run directory; older
	// journals left it empty and saved under the root's base name.
	Copy string `json:"copy,omitempty"`
	// After is the SHA-256 (hex) of the file as the run's last write left
	// it, or absentHash when that write removed it; empty until the run
	// wrote the file. A later manifest line for the same file updates it.
	After string `json:"after,omitempty"`
}

// absentHash is the After of a file the run's last write removed.
const absentHash = "absent"

// RunDir returns the journal directory of run id:
// ${XDG_STATE_HOME:-~/.local/state}/codedoc/runs/<id>.
func RunDir(id string) (string, error) {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return "", fmt.Errorf("write: invalid run id %q", id)
	}
	runs, err := RunsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(runs, id), nil
}

// RunsDir returns the directory holding every run's journal:
// ${XDG_STATE_HOME:-~/.local/state}/codedoc/runs.
func RunsDir() (string, error) {
	state, err := xdg.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(state, "runs"), nil
}

// journalFile saves root/path under the run's journal unless the
// manifest already lists it: its bytes go to <run>/<base>-<hash8>/<path>
// (see journalDir) and the manifest records the copy and whether the
// file existed. Only the first change of a file in a run is journaled; a
// copy the manifest does not list (left by a run that stopped between
// the two writes) is overwritten. Processes of one run journal one at a
// time, under an exclusive lock of <run>/journal.lock.
func journalFile(id, root, path string) (err error) {
	dir, err := RunDir(id)
	if err != nil {
		return err
	}
	unlock, err := lockRun(dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	_, list, err := entries(id)
	if err != nil {
		return err
	}
	for _, e := range list {
		if e.Root == root && e.Path == path {
			return nil
		}
	}
	rel := filepath.Join(journalDir(root), filepath.FromSlash(path))
	copyPath := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(copyPath), 0o755); err != nil {
		return err
	}
	data, rerr := os.ReadFile(filepath.Join(root, path))
	if rerr != nil && !os.IsNotExist(rerr) {
		return rerr
	}
	if err := os.WriteFile(copyPath, data, 0o600); err != nil {
		return err
	}
	return appendEntry(filepath.Join(dir, manifestName),
		entry{Root: root, Path: path, Existed: rerr == nil, Copy: filepath.ToSlash(rel)})
}

// lockRun takes the exclusive journal lock of a run directory, creating
// the directory, and returns its release.
func lockRun(dir string) (func() error, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "journal.lock"), os.O_CREATE|os.O_RDWR, 0o600)
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

// journalDir names the journal directory of a repo root: its base name,
// for readability, and the first eight hex digits of the SHA-256 of the
// absolute root, so two repos with the same base name do not collide.
func journalDir(root string) string {
	sum := sha256.Sum256([]byte(root))
	return filepath.Base(root) + "-" + hex.EncodeToString(sum[:4])
}

// appendEntry adds one manifest line; appends of one short line are atomic.
func appendEntry(manifest string, e entry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(manifest, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	return errors.Join(werr, f.Close())
}

// entries reads the manifest of run id; a run that journaled nothing has
// none.
func entries(id string) (string, []entry, error) {
	dir, err := RunDir(id)
	if err != nil {
		return "", nil, err
	}
	f, err := os.Open(filepath.Join(dir, manifestName))
	if os.IsNotExist(err) {
		return dir, nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = f.Close() }()
	var out []entry
	at := map[[2]string]int{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return "", nil, fmt.Errorf("write: journal %s: %w", id, err)
		}
		k := [2]string{e.Root, e.Path}
		if i, seen := at[k]; seen {
			out[i].After = e.After
			continue
		}
		at[k] = len(out)
		out = append(out, e)
	}
	return dir, out, sc.Err()
}

// recordWrite notes in the manifest of run id what the run's latest
// write left at root/path (data, or nothing when absent), so Restore can
// tell a later change by someone else.
func recordWrite(id, root, path string, data []byte, absent bool) (err error) {
	dir, err := RunDir(id)
	if err != nil {
		return err
	}
	unlock, err := lockRun(dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	after := absentHash
	if !absent {
		after = hashBytes(data)
	}
	return appendEntry(filepath.Join(dir, manifestName), entry{Root: root, Path: path, After: after})
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// currentHash returns the After form of the file at abs now.
func currentHash(abs string) (string, error) {
	data, err := os.ReadFile(abs)
	if os.IsNotExist(err) {
		return absentHash, nil
	}
	if err != nil {
		return "", err
	}
	return hashBytes(data), nil
}

// Files returns the absolute paths of the files journaled in run id,
// sorted.
func Files(id string) ([]string, error) {
	_, list, err := entries(id)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(list))
	for i, e := range list {
		out[i] = filepath.Join(e.Root, filepath.FromSlash(e.Path))
	}
	sort.Strings(out)
	return out, nil
}

// Restore puts every file journaled in run id back as it was before the
// run (removing files the run created), except a file that changed
// after the run's last write to it: someone else edited it during the
// run, so it is left as it is and returned in kept (absolute paths).
// The journal is deleted unless keep is set or a file was kept, so the
// originals of kept files stay recoverable.
func Restore(id string, keep bool) (kept []string, err error) {
	return RestoreUnless(id, keep, nil)
}

// RestoreUnless is Restore leaving alone, and out of kept, the files
// skip accepts (absolute paths): the caller found them settled some
// other way, such as committed while the run was going.
func RestoreUnless(id string, keep bool, skip func(abs string) bool) (kept []string, err error) {
	dir, list, err := entries(id)
	if err != nil {
		return nil, err
	}
	for _, e := range list {
		if skip != nil && skip(filepath.Join(e.Root, filepath.FromSlash(e.Path))) {
			continue
		}
		changed, err := restoreEntry(dir, e)
		if err != nil {
			return kept, err
		}
		if changed {
			kept = append(kept, filepath.Join(e.Root, filepath.FromSlash(e.Path)))
		}
	}
	if keep || len(kept) > 0 {
		return kept, nil
	}
	return nil, os.RemoveAll(dir)
}

// restoreEntry restores one file, under its lock (see lockFile);
// changed reports a file left alone because it no longer holds what the
// run's last write left.
func restoreEntry(dir string, e entry) (changed bool, err error) {
	abs := filepath.Join(e.Root, filepath.FromSlash(e.Path))
	unlock, err := lockFile(abs)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	if e.After != "" {
		cur, err := currentHash(abs)
		if err != nil || cur != e.After {
			return true, err
		}
	}
	return false, putBack(dir, e, abs)
}

// putBack writes the saved original of e to abs, or removes abs when
// the run created it.
func putBack(dir string, e entry, abs string) error {
	if !e.Existed {
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	saved := filepath.Join(dir, filepath.Base(e.Root), filepath.FromSlash(e.Path))
	if e.Copy != "" {
		saved = filepath.Join(dir, filepath.FromSlash(e.Copy))
	}
	data, err := os.ReadFile(saved)
	if err != nil {
		return err
	}
	return writeFile(abs, data)
}
