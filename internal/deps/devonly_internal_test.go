package deps

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/store"
)

func TestDevOnlyIsDevDependenciesAndNowhereElse(t *testing.T) {
	pj := packageJSON{
		Dependencies:     map[string]string{"run": "1", "both": "1"},
		DevDependencies:  map[string]string{"lint": "1", "both": "1", "peer": "1"},
		PeerDependencies: map[string]string{"peer": "1"},
	}
	for name, want := range map[string]bool{"run": false, "lint": true, "both": false, "peer": false, "absent": false} {
		if got := pj.devOnly(name); got != want {
			t.Errorf("%s: dev only %v, want %v", name, got, want)
		}
	}
}

// The packages a repo runs with are embedded before those it only
// develops with; a package some manifest runs with is a runtime one.
func TestUsedPackagesPutRuntimeFirst(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	id, err := s.UpsertRepo(store.Repo{Workspace: "w", Name: "r", Path: "/r"})
	if err != nil {
		t.Fatal(err)
	}
	use := func(manifest, name string, dev bool, state string) store.DepUse {
		return store.DepUse{Manifest: manifest, Eco: "npm", Name: name, Version: "1.0.0", State: state, Dev: dev}
	}
	err = s.Batch(func(b *store.Batch) error {
		return b.ReplaceDepUses(id, []store.DepUse{
			use("package.json", "zod", false, store.DepIndexed),
			use("package.json", "eslint", true, store.DepIndexed),
			use("package.json", "shared", true, store.DepIndexed),
			use("web/package.json", "shared", false, store.DepIndexed),
			use("package.json", "absent", false, store.DepMissing),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	uses, err := s.DepUses(id)
	if err != nil || len(uses) != 5 || !uses[1].Dev || uses[4].Dev {
		t.Fatalf("the dev flag is stored: %+v %v", uses, err)
	}
	runtime, dev, err := usedPackages(s, []*config.Repo{{Path: "/r"}, {Path: "/unknown"}})
	if err != nil || !reflect.DeepEqual(runtime, []string{"npm/shared@1.0.0", "npm/zod@1.0.0"}) ||
		!reflect.DeepEqual(dev, []string{"npm/eslint@1.0.0"}) {
		t.Fatalf("runtime %v dev %v: %v", runtime, dev, err)
	}
}
