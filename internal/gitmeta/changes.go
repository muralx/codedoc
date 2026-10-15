package gitmeta

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Changed is a file that differs between a ref and the working tree.
type Changed struct {
	// Status is 'A' (added, untracked files included), 'D' (deleted),
	// 'M' (modified, type changes included) or 'R' (renamed).
	Status byte
	// Path is the path now, relative to the directory asked about; for a
	// deleted file the path it had.
	Path string
	// OldPath is the path at the ref of a renamed file.
	OldPath string
}

// ChangesSince returns the files under dir (paths relative to it) that
// differ between ref (a commit or tree) and the working tree, uncommitted
// changes included, plus untracked files that are not ignored, as added.
// Renames are detected (git diff -M) and listed once. The list is sorted
// by path.
func ChangesSince(dir, ref string) ([]Changed, error) {
	diff, err := run(dir, "diff", "--name-status", "-M", "--relative", "-z", ref, "--", ".")
	if err != nil {
		return nil, err
	}
	out := parseNameStatus(string(diff))
	others, err := run(dir, "ls-files", "-z", "--others", "--exclude-standard", "--", ".")
	if err != nil {
		return nil, err
	}
	for _, p := range strings.Split(string(others), "\x00") {
		if p != "" {
			out = append(out, Changed{Status: 'A', Path: p})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// parseNameStatus reads git diff --name-status -z output: a status
// field, then one path, or two for renames and copies.
func parseNameStatus(out string) []Changed {
	f := strings.Split(out, "\x00")
	var res []Changed
	for i := 0; i+1 < len(f); i += 2 {
		st := f[i]
		if st == "" {
			break
		}
		c := Changed{Status: st[0], Path: f[i+1]}
		switch c.Status {
		case 'R', 'C':
			if i+2 >= len(f) {
				return res
			}
			c.OldPath, c.Path = f[i+1], f[i+2]
			i++
			if c.Status == 'C' {
				c.Status, c.OldPath = 'A', ""
			}
		case 'T', 'U', 'X':
			c.Status = 'M'
		}
		res = append(res, c)
	}
	return res
}

// Blobs reads files as they are at one revision through a single git
// cat-file --batch process. Close it when done.
type Blobs struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	rev    string // full hash of the commit or tree
	prefix string // the directory's path inside the repository, with a trailing slash
}

// OpenBlobs resolves rev once (a commit or tree) and starts the batch
// reader for paths relative to dir.
func OpenBlobs(dir, rev string) (*Blobs, error) {
	full, err := resolveCommit(dir, rev)
	if err == nil && full == "" {
		full, err = resolveTree(dir, rev)
	}
	if err != nil {
		return nil, err
	}
	if full == "" {
		return nil, fmt.Errorf("gitmeta: unknown ref %q", rev)
	}
	prefix, err := run(dir, "rev-parse", "--show-prefix")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Dir = dir
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Blobs{cmd: cmd, in: in, out: bufio.NewReader(out), rev: full,
		prefix: strings.TrimSpace(string(prefix))}, nil
}

// Read returns the content of path (relative to the directory) at the
// revision; ok is false when it does not exist there.
func (b *Blobs) Read(path string) ([]byte, bool, error) {
	if _, err := fmt.Fprintf(b.in, "%s:%s%s\n", b.rev, b.prefix, path); err != nil {
		return nil, false, err
	}
	header, err := b.out.ReadString('\n')
	if err != nil {
		return nil, false, err
	}
	f := strings.Fields(header)
	if len(f) != 3 {
		return nil, false, nil // "<name> missing" or "<name> ambiguous"
	}
	size, err := strconv.Atoi(f[2])
	if err != nil {
		return nil, false, fmt.Errorf("gitmeta: cat-file header %q", header)
	}
	data := make([]byte, size+1) // the content and its trailing newline
	if _, err := io.ReadFull(b.out, data); err != nil {
		return nil, false, err
	}
	return data[:size], f[1] == "blob", nil
}

// Close stops the batch reader.
func (b *Blobs) Close() error {
	return errors.Join(b.in.Close(), b.cmd.Wait())
}
