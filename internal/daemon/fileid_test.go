package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFreshQueryCarriesTheDatabaseFileID(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "fx.db")
	if err := os.WriteFile(db, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	q := NewFreshQuery("fx", "lib", "/src/lib", db)
	if q.Workspace != "fx" || q.Repo != "lib" || q.Root != "/src/lib" || q.DB != db || q.DBID.Ino == 0 {
		t.Fatalf("%+v", q)
	}
	if err := os.Rename(db, db+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if again := NewFreshQuery("fx", "lib", "/src/lib", db); again.DBID == q.DBID {
		t.Fatalf("a recreated database kept its id %+v", again.DBID)
	}
	if missing := NewFreshQuery("fx", "lib", "/src/lib", filepath.Join(dir, "none.db")); missing.DBID != (FileID{}) {
		t.Fatalf("a missing database has an id: %+v", missing.DBID)
	}
}
