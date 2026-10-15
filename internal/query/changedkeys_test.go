package query_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

func sortedKeys(m map[string]bool) string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, " ")
}

func TestChangedKeysNamesChangedSymbolsAndTheirDirectories(t *testing.T) {
	c := workspaceOf(t, "go-lib")
	r := c.Workspace.Repos[0]
	base := gitIn(t, r.Path, "2026-01-01T00:00:00Z", "rev-parse", "HEAD")
	unchanged := &query.Ctx{Store: c.Store, Workspace: c.Workspace}
	if keys, err := query.ChangedKeys(unchanged, r, base, nil); keys != nil || err != nil {
		t.Fatalf("nothing changed: %v %v", keys, err)
	}
	editRepo(t, r.Path, "store/store.go", "return v, nil", "return v + \"\", nil")
	added := "package service\n\nfunc Added() int {\n\treturn 1\n}\n"
	if err := os.WriteFile(filepath.Join(r.Path, "service", "added.go"), []byte(added), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Path, "NOTES.md"), []byte("# Notes\n\ntext\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	keys, err := query.ChangedKeys(c, r, base, nil)
	want := libService + "Added " + libStore + "MemStore.Get go-lib:service/ go-lib:store/"
	if err != nil || sortedKeys(keys) != want {
		t.Fatalf("got  %s\nwant %s (%v)", sortedKeys(keys), want, err)
	}
	keys, err = query.ChangedKeys(c, r, base, func(p string) bool { return strings.HasPrefix(p, "service/") })
	if err != nil || sortedKeys(keys) != libService+"Added go-lib:service/" {
		t.Fatalf("kept service only: %s (%v)", sortedKeys(keys), err)
	}
	if _, err := query.ChangedKeys(c, r, "no-such-ref", nil); err == nil {
		t.Fatal("an unknown ref answered")
	}
}
