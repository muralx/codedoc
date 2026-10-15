package feedback

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"unicode/utf8"
)

const (
	// MaxText is the most bytes of a report's text that are kept.
	MaxText = 8 << 10
	// MaxField is the most bytes kept of every other field of a report.
	MaxField = 1 << 10
	// MaxFileSize is the size the feedback file never exceeds: a report
	// that would take it further first drops the oldest reports, keeping
	// the newest half of the file.
	MaxFileSize = 4 << 20
	// Truncated ends a text or a field that was cut to its limit.
	Truncated = " [truncated]"
	// maxLine is the longest line read as an entry. The JSON encoding
	// of an entry cut to the limits stays far below it, so a longer line
	// is no entry and is skipped without being held in memory.
	maxLine = 1 << 20
)

// Clamp returns e with its text cut to MaxText bytes and every other
// field to MaxField bytes. A cut value ends with Truncated and is cut at
// a character boundary.
func Clamp(e Entry) Entry {
	e.Text = clamp(e.Text, MaxText)
	for _, f := range []*string{&e.Time, &e.Workspace, &e.Repo, &e.Head, &e.Build, &e.Extractor, &e.Key, &e.Path, &e.Cmd,
		&e.IndexedHash, &e.CurrentHash} {
		*f = clamp(*f, MaxField)
	}
	return e
}

// clamp cuts s to at most limit bytes, ending a cut value with Truncated.
func clamp(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	s = s[:limit-len(Truncated)]
	for range utf8.UTFMax - 1 { // the bytes of a character the cut went through
		if r, n := utf8.DecodeLastRuneInString(s); r != utf8.RuneError || n != 1 {
			break
		}
		s = s[:len(s)-1]
	}
	return s + Truncated
}

// readLine returns the next line of br without its line break, or nil
// for a line longer than maxLine, which is read to its end and dropped.
// The last line comes with io.EOF, whether or not a line break ends it.
func readLine(br *bufio.Reader) ([]byte, error) {
	line, long := []byte{}, false
	for {
		chunk, err := br.ReadSlice('\n')
		if !long {
			line = append(line, chunk...)
		}
		if len(line) > maxLine {
			line, long = nil, true
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return bytes.TrimSuffix(line, []byte{'\n'}), err
		}
	}
}

// rotate makes room in the feedback file at p for extra more bytes: when
// they would take it past MaxFileSize, the file is replaced by its
// newest lines, at most MaxFileSize/2 bytes of them. Only whole lines
// are kept, so a file without a line break in that tail is emptied. The
// new content is written beside the file and renamed over it; a report
// another process appends in between is lost with the old file.
func rotate(p string, extra int64) (err error) {
	st, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && st.Size()+extra <= MaxFileSize) {
		return nil
	}
	if err != nil {
		return err
	}
	tail, err := newestLines(p, st.Size(), MaxFileSize/2)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), filepath.Base(p)+".*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	_, err = tmp.Write(tail)
	if err = errors.Join(err, tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// newestLines returns the whole lines within the last keep bytes of the
// file at p, which is size bytes long: what follows the first line break
// of that tail.
func newestLines(p string, size, keep int64) (tail []byte, err error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(max(0, size-keep), io.SeekStart); err != nil {
		return nil, err
	}
	if tail, err = io.ReadAll(io.LimitReader(f, keep)); err != nil {
		return nil, err
	}
	_, tail, _ = bytes.Cut(tail, []byte{'\n'})
	return tail, nil
}
