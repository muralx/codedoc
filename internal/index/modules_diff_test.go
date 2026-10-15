package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/resolve"
	"github.com/muralx/codedoc/internal/store"
)

func identitiesOf(t *testing.T, files map[string]string) *resolve.Map {
	t.Helper()
	root := t.TempDir()
	var paths []string
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	m, err := resolve.Identities(root, paths)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// stored mimics the rows prepareModules writes for a map.
func stored(m *resolve.Map) ([]store.Module, []store.RepoIdentity) {
	var mods []store.Module
	ids := map[string]int64{}
	for i, mod := range m.Modules {
		id := int64(i + 1)
		ids[mod.Lang+"\x00"+mod.Path] = id
		mods = append(mods, store.Module{ID: id, Path: mod.Path, Lang: mod.Lang, Kind: mod.Kind, Identity: mod.Identity})
	}
	for i, mod := range m.Modules {
		if mod.Parent != "" {
			mods[i].ParentID = ids[mod.Lang+"\x00"+mod.Parent]
		}
	}
	return mods, repoIdentities(m.Repo)
}

func TestDiffDuplicateIdentitiesAreStable(t *testing.T) {
	files := map[string]string{
		"go.mod": "module example.com/m\n", "a.go": "package m\n",
		"examples/copy/go.mod": "module example.com/m\n", "examples/copy/b.go": "package m\n",
		"web/package.json": `{"name": "web"}`, "web/i.ts": "export const i = 1;\n",
	}
	m := identitiesOf(t, files)
	mods, ids := stored(m)
	if len(ids) != 2 {
		t.Fatalf("one identity per (lang, identity): %+v", ids)
	}
	// Stored rows come back in another order; that is no change.
	ids[0], ids[1] = ids[1], ids[0]
	ms := &moduleState{ids: m, moved: map[string]bool{}}
	ms.diff(mods, ids)
	if ms.changed || len(ms.moved) != 0 {
		t.Errorf("unchanged manifests reported as changed: %+v", ms)
	}
}

func TestDiffDetectsPackageRootMove(t *testing.T) {
	before := identitiesOf(t, map[string]string{
		"a/package.json": `{"name": "x"}`, "a/b/c/f.ts": "export const f = 1;\n",
	})
	after := identitiesOf(t, map[string]string{
		"a/b/package.json": `{"name": "x/b"}`, "a/b/c/f.ts": "export const f = 1;\n",
	})
	mods, ids := stored(before)
	ms := &moduleState{ids: after, moved: map[string]bool{}}
	ms.diff(mods, ids)
	if !ms.moved["ts\x00a/b/c"] || !ms.changed {
		t.Errorf("a/b/c keeps its identity but changes package root: %+v", ms.moved)
	}
}
