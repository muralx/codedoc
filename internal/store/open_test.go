package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRejectsANewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, s, `UPDATE meta SET value = ? WHERE key = 'schema_version'`, fmt.Sprint(len(migrations)+3))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = Open(path)
	want := fmt.Sprintf("database was created by a newer codedoc (schema %d > %d); upgrade codedoc or run codedoc index --rebuild",
		len(migrations)+3, len(migrations))
	if err == nil || err.Error() != want || !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("got %v, want %s", err, want)
	}
}

func TestOpenErrorsNameTheFix(t *testing.T) {
	for in, want := range map[error]string{
		errors.New("migration 3: no such module: fts5"):   "built without FTS5 support: rebuild with -tags sqlite_fts5",
		errors.New("migration 4: table x already exists"): "open /d/ws.db: migration 4: table x already exists (run codedoc index --rebuild to recreate it)",
		errors.New("database is locked"):                  "open /d/ws.db: database is locked",
		fmt.Errorf("%w (schema 9 > 8)", ErrNewerSchema):   "database was created by a newer codedoc (schema 9 > 8)",
	} {
		if got := openError("/d/ws.db", in).Error(); got != want {
			t.Errorf("%v: got %q, want %q", in, got, want)
		}
	}
}

func TestOpenFailureCarriesTheRebuildHint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, s, `UPDATE meta SET value = '2' WHERE key = 'schema_version'`)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil || !strings.HasSuffix(err.Error(), "(run codedoc index --rebuild to recreate it)") {
		t.Fatalf("got %v", err)
	}
}
