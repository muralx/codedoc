package write

import (
	"os"
	"path/filepath"
	"testing"
)

// repo writes files into a fresh directory and returns it.
func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// read returns the content of a repo file.
func read(t *testing.T, root, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// roundTrip is one Apply call and the expected file content afterwards.
type roundTrip struct {
	name  string
	files map[string]string
	tg    Target
	text  string
	file  string // file to compare, relative to the repo
	want  string
	doc   string // expected re-extracted doc text
}

// run applies every case in a fresh repo, compares the written file, then
// applies the same text again and expects no change.
func runRoundTrips(t *testing.T, cases []roundTrip) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := repo(t, c.files)
			c.tg.Root = root
			res, err := Apply(c.tg, c.text)
			if err != nil {
				t.Fatal(err)
			}
			if got := read(t, root, c.file); got != c.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, c.want)
			}
			if res.Path != c.file || res.Doc.Text != c.doc {
				t.Fatalf("result %s %q, want %s %q", res.Path, res.Doc.Text, c.file, c.doc)
			}
			if _, err := Apply(c.tg, c.text); err != nil {
				t.Fatal(err)
			}
			if got := read(t, root, c.file); got != c.want {
				t.Fatalf("second apply changed the file:\n%s", got)
			}
		})
	}
}
