package index_test

import "testing"

// TestEnsureDropsStoredLockFileRows checks that a lock file is never
// discovered and that a row an older build stored for one goes with the
// next freshness check, without an extractor version bump.
func TestEnsureDropsStoredLockFileRows(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	writeFile(t, rep.Path, "pnpm-lock.yaml", []byte("lockfileVersion: '9.0'\npackages:\n  a: 1\n"))
	indexRepo(t, s, rep)
	r, err := s.RepoByPath(rep.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.FileByPath(r.ID, "pnpm-lock.yaml"); err == nil {
		t.Fatal("a lock file was indexed")
	}
	if keys, err := s.ConfigKeysMatching("lockfileVersion", r.ID); err != nil || len(keys) != 0 {
		t.Fatalf("config keys from a lock file: %+v (%v)", keys, err)
	}
	old := fileRow(t, s, rep.Path, "go.mod")
	old.Path = "pnpm-lock.yaml"
	if _, err := s.UpsertFile(old); err != nil {
		t.Fatal(err)
	}
	if f := ensure(t, s, rep, 200); !f.Fresh || f.Changed != 0 {
		t.Fatalf("%+v", f)
	}
	if _, err := s.FileByPath(r.ID, "pnpm-lock.yaml"); err == nil {
		t.Fatal("the stored lock file row survived a freshness check")
	}
}
