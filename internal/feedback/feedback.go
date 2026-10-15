// Package feedback keeps the reports agents file when codedoc disagrees
// with a file (documents spec section 4.2): one JSON line per report in
// a file under the state directory. Nothing leaves the machine. The file
// is bounded: the text and the fields of an entry are cut to MaxText and
// MaxField bytes, and a file that would grow past MaxFileSize first
// drops its oldest reports (see limits.go).
package feedback

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/muralx/codedoc/internal/xdg"
)

// Entry is one report.
type Entry struct {
	// Time is when the report was filed, RFC 3339 in UTC.
	Time string `json:"time"`
	// Workspace, Repo and Head name the index the answer came from; Head
	// is branch@commit. Dirty tells that the repo had uncommitted
	// changes. They are empty outside a workspace.
	Workspace string `json:"workspace"`
	Repo      string `json:"repo"`
	Head      string `json:"head"`
	Dirty     bool   `json:"dirty"`
	// Build and Extractor are the codedoc build and the extractor
	// version that produced the answer.
	Build     string `json:"build"`
	Extractor string `json:"extractor"`
	// Text says what was asked, what came back and what the file says.
	Text string `json:"text"`
	// Key, Path and Cmd are the optional symbol key, file and command
	// the report is about.
	Key  string `json:"key,omitempty"`
	Path string `json:"path,omitempty"`
	Cmd  string `json:"cmd,omitempty"`
	// IndexedHash and CurrentHash are the content hash the index holds
	// for the file and the one of the file on disk, when Path or Key
	// names a file; they differ when the index is behind the file.
	IndexedHash string `json:"indexed_hash,omitempty"`
	CurrentHash string `json:"current_hash,omitempty"`
}

// File returns the path of the feedback file:
// $XDG_STATE_HOME/codedoc/feedback.jsonl.
func File() (string, error) {
	dir, err := xdg.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "feedback.jsonl"), nil
}

// Append adds an entry to the feedback file, creating it when needed.
// The entry is cut to the size limits first (see Clamp), and a file the
// entry would take past MaxFileSize first drops its oldest reports (see
// rotate). An entry always starts on a line of its own, also after a
// last line that lost its line break.
func Append(e Entry) (err error) {
	p, err := File()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	line, err := json.Marshal(Clamp(e))
	if err != nil {
		return err
	}
	if err := rotate(p, int64(len(line))+2); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	if !endsLine(f) {
		line = append([]byte{'\n'}, line...)
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

// endsLine reports whether f is empty or ends with a line break; a file
// that cannot be read counts as ending with one.
func endsLine(f *os.File) bool {
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return true
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, st.Size()-1); err != nil {
		return true
	}
	return last[0] == '\n'
}

// List returns the entries filed at or after since (the zero time for
// all), oldest first. Lines that are no entry are skipped, whatever
// their length or content; a missing file holds none.
func List(since time.Time) ([]Entry, error) {
	p, err := File()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []Entry
	br := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := readLine(br)
		if e, ok := entryOf(line, since); ok {
			out = append(out, e)
		}
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
	}
}

// entryOf decodes a line of the feedback file; ok is false for a line
// that is no entry (nil, not JSON, without a text) and for an entry
// filed before since.
func entryOf(line []byte, since time.Time) (e Entry, ok bool) {
	if line == nil || json.Unmarshal(line, &e) != nil || e.Text == "" {
		return e, false
	}
	at, err := time.Parse(time.RFC3339, e.Time)
	return e, since.IsZero() || (err == nil && !at.Before(since))
}

// Clear empties the feedback file and returns how many entries it held.
// The file is truncated whatever it holds: the count is of the entries
// that could be read, zero when the file cannot be read at all.
func Clear() (int, error) {
	p, err := File()
	if err != nil {
		return 0, err
	}
	entries, _ := List(time.Time{}) // a file that cannot be read is emptied all the same
	if err := os.Truncate(p, 0); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	return len(entries), nil
}
