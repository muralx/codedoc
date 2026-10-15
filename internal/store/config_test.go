package store_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

type configFixture struct {
	repo, other, envFile, yamlFile, codeFile, ref int64
}

// seedConfig stores two config files of one repo, a definition of the
// same key in a second repo, and a code file with an accessor read of
// LIB_MODE and a key-like literal LIT_MODE.
func seedConfig(t *testing.T, s *store.Store) configFixture {
	t.Helper()
	var fx configFixture
	fx.repo, _ = s.UpsertRepo(store.Repo{Workspace: "w", Name: "cfg", Path: "/cfg"})
	fx.other, _ = s.UpsertRepo(store.Repo{Workspace: "w", Name: "app", Path: "/app"})
	file := func(repo int64, path, lang string) int64 {
		id, err := s.UpsertFile(store.File{RepoID: repo, Path: path, Lang: lang, Size: 1, Mtime: 1, Hash: "h"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	fx.envFile, fx.yamlFile = file(fx.repo, ".env", "env"), file(fx.repo, "app.yaml", "yaml")
	fx.codeFile = file(fx.other, "main.go", "go")
	otherEnv := file(fx.other, ".env", "env")
	err := s.Batch(func(b *store.Batch) error {
		if err := b.ReplaceFileConfig(fx.envFile, fx.repo, "env", []store.ConfigEntry{{Key: "LIB_MODE", Line: 1, Preview: "dev"}},
			[]store.ConfigEntry{{Key: "API_URL", Line: 2}}); err != nil {
			return err
		}
		if err := b.ReplaceFileConfig(fx.yamlFile, fx.repo, "yaml",
			[]store.ConfigEntry{{Key: "LIB_MODE", Line: 3, Preview: "prod"}, {Key: "lib.mode", Line: 4}}, nil); err != nil {
			return err
		}
		if err := b.ReplaceFileConfig(otherEnv, fx.other, "env", []store.ConfigEntry{{Key: "LIB_MODE", Line: 1}}, nil); err != nil {
			return err
		}
		return b.ReplaceFileReferences(fx.codeFile, []store.Reference{
			{Kind: "config", Name: "LIB_MODE", Qualifier: store.ConfigAccessor, Line: 7, Arity: -1},
			{Kind: "config", Name: "LIT_MODE", Qualifier: "literal", Line: 8, Arity: -1}})
	})
	if err != nil {
		t.Fatal(err)
	}
	refs, err := s.UnresolvedReferences(fx.codeFile)
	if err != nil || len(refs) != 2 || refs[0].Name != "LIB_MODE" {
		t.Fatalf("%+v %v", refs, err)
	}
	fx.ref = refs[0].ID
	return fx
}

func TestConfigKeysAndUses(t *testing.T) {
	s := openTemp(t)
	fx := seedConfig(t, s)
	keys := checkKeyLookups(t, s, fx)
	err := s.Batch(func(b *store.Batch) error {
		defined, err := b.DefinedConfigKeys([]string{"LIB_MODE", "API_URL"})
		if err != nil || len(defined) != 2 {
			t.Errorf("API_URL is only referenced: %+v %v", defined, err)
		}
		if all, err := b.DefinedConfigKeys(nil); err != nil || len(all) != 3 {
			t.Errorf("all defined %+v %v", all, err)
		}
		return b.InsertConfigRead(store.ConfigRead{KeyID: keys[0].ID, FileID: fx.codeFile, ReferenceID: fx.ref, Line: 7, Format: "go"})
	})
	if err != nil {
		t.Fatal(err)
	}
	uses, err := s.ConfigUses([]int64{keys[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, u := range uses {
		got = append(got, u.Kind+" "+u.Repo+":"+u.Path+" "+u.Format+" "+u.Preview)
	}
	want := []string{"defines cfg:.env env dev", "defines cfg:app.yaml yaml prod", "reads app:main.go go "}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("uses %q", got)
	}
	checkConfigStats(t, s, fx, 2, 0)
}

// checkKeyLookups checks the key lookups by name and glob and returns the
// LIB_MODE key rows.
func checkKeyLookups(t *testing.T, s *store.Store, fx configFixture) []store.ConfigKey {
	t.Helper()
	keys, err := s.ConfigKeysByName("LIB_MODE", 0)
	if err != nil || len(keys) != 2 || keys[0].Repo != "cfg" || keys[1].Repo != "app" {
		t.Fatalf("by name %+v %v", keys, err)
	}
	if only, _ := s.ConfigKeysByName("LIB_MODE", fx.other); len(only) != 1 || only[0].RepoID != fx.other {
		t.Fatalf("by name in repo %+v", only)
	}
	if glob, _ := s.ConfigKeysMatching("*_*", fx.repo); len(glob) != 2 || glob[0].Key != "API_URL" {
		t.Fatalf("glob %+v", glob)
	}
	return keys
}

func checkConfigStats(t *testing.T, s *store.Store, fx configFixture, keys, unresolved int) {
	t.Helper()
	k, _, err := s.ConfigStats(fx.repo)
	if err != nil || k != keys {
		t.Errorf("cfg stats %d %v, want %d keys", k, err, keys)
	}
	if _, u, err := s.ConfigStats(fx.other); err != nil || u != unresolved {
		t.Errorf("app stats %d %v, want %d unresolved", u, err, unresolved)
	}
}

func TestConfigClearAndOrphans(t *testing.T) {
	s := openTemp(t)
	fx := seedConfig(t, s)
	checkConfigStats(t, s, fx, 2, 1)
	keys, _ := s.ConfigKeysByName("LIB_MODE", fx.repo)
	err := s.Batch(func(b *store.Batch) error {
		if err := b.InsertConfigRead(store.ConfigRead{KeyID: keys[0].ID, FileID: fx.codeFile, ReferenceID: fx.ref, Line: 7,
			Format: "go"}); err != nil {
			return err
		}
		names, err := b.ClearFileConfig(fx.envFile)
		if err != nil || !reflect.DeepEqual(names, []string{"LIB_MODE"}) {
			t.Errorf("cleared %v %v", names, err)
		}
		return b.DeleteOrphanConfigKeys(fx.repo)
	})
	if err != nil {
		t.Fatal(err)
	}
	if left, _ := s.ConfigKeysMatching("*", fx.repo); len(left) != 2 || left[0].Key != "LIB_MODE" || left[1].Key != "lib.mode" {
		t.Fatalf("API_URL was only referenced by .env: %+v", left)
	}
	checkConfigStats(t, s, fx, 2, 0)
	for _, ids := range [][]int64{{fx.ref}, nil} {
		if err := s.Batch(func(b *store.Batch) error { return b.DeleteConfigReads(ids) }); err != nil {
			t.Fatal(err)
		}
		checkConfigStats(t, s, fx, 2, 1)
	}
}

func TestUnmatchedConfigReads(t *testing.T) {
	s := openTemp(t)
	fx := seedConfig(t, s)
	names := func(pattern string, glob bool, repoID int64) []string {
		t.Helper()
		reads, err := s.UnmatchedConfigReads(pattern, glob, repoID)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range reads {
			out = append(out, fmt.Sprintf("%s %s:%s:%d", r.Name, r.Repo, r.Path, r.Line))
		}
		return out
	}
	if got := names("LIT_MODE", false, 0); !reflect.DeepEqual(got, []string{"LIT_MODE app:main.go:8"}) {
		t.Errorf("exact name lists a literal: %v", got)
	}
	if got := names("*_MODE", true, 0); !reflect.DeepEqual(got, []string{"LIB_MODE app:main.go:7"}) {
		t.Errorf("glob lists accessor reads only: %v", got)
	}
	if got := names("LIT_MODE", false, fx.repo); got != nil {
		t.Errorf("repo filter: %v", got)
	}
	err := s.Batch(func(b *store.Batch) error {
		keys, _ := b.DefinedConfigKeys([]string{"LIB_MODE"})
		return b.InsertConfigRead(store.ConfigRead{KeyID: keys[0].ID, FileID: fx.codeFile, ReferenceID: fx.ref, Line: 7,
			Format: "go"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := names("*_MODE", true, 0); got != nil {
		t.Errorf("a matched read is no undefined read: %v", got)
	}
}
