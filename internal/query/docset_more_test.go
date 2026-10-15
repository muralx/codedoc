package query_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

func TestDocSetOnAMultiNameSpecNamesTheOthers(t *testing.T) {
	c := tempWorkspace(t, map[string]string{"go.mod": "module x/m\n", "a.go": "package m\n\nvar A, B, C = 1, 2, 3\n"})
	r, err := query.DocSet(c, "A", "Counts things.")
	mustContain(t, run(t, r, err), "the doc of A also documents B, C (declared together)")
}

func TestDocSetHeaderIsDirtyAfterTheWrite(t *testing.T) {
	c := workspaceOf(t, "go-lib")
	r, err := query.DocSet(c, "go-lib:store/store.go#MemStore.Get", "Get returns the value.")
	if text := run(t, r, err); !strings.HasPrefix(text, "dirty\n") || r.Header.Dirty != 1 || !r.Header.Fresh {
		t.Errorf("header %+v:\n%s", r.Header, text)
	}
}

func TestDocCheckGoFirstWordOnlyForExportedTopLevel(t *testing.T) {
	c := tempWorkspace(t, map[string]string{"go.mod": "module x/m\n",
		"a.go": "package m\n\n// Helper for one.\nfunc one() int { return 1 }\n\n// T is t.\ntype T struct {\n\t// Holds the count.\n\tN int\n}\n\n" +
			"// Counts things.\nfunc (T) Count() int { return 1 }\n\n// Wrong start.\nfunc Exported() {}\n"})
	r, err := query.DocCheck(c)
	text := run(t, r, err)
	mustContain(t, text, "go doc does not start with Exported")
	for _, name := range []string{"one", "N", "Count"} {
		if strings.Contains(text, "go doc does not start with "+name+" ") {
			t.Errorf("%s is no exported top-level declaration:\n%s", name, text)
		}
	}
}

func TestOverviewPackageMainAndOrder(t *testing.T) {
	c := tempWorkspace(t, map[string]string{
		"go.mod":         "module x/m\n",
		"tools/gen/a.go": "package main\n\nfunc main() {}\n",
		"tools/gen/b.go": "package main\n\nfunc helper() {}\n",
	})
	r, err := query.Overview(c)
	text := run(t, r, err)
	mustContain(t, text, "r:tools/gen/  package main, 2 files")
	if strings.Index(text, "== doc coverage") > strings.Index(text, "== recent commits") &&
		strings.Contains(text, "== recent commits") {
		t.Fatalf("doc coverage comes before recent commits:\n%s", text)
	}
	c.Offset = 100
	r, err = query.Overview(c)
	text = run(t, r, err)
	if n := strings.Count(text, "offset 100 is past the end"); n != 1 {
		t.Fatalf("offset note %d times:\n%s", n, text)
	}
}

func TestDocSetModuleAddressWithoutSlash(t *testing.T) {
	c := workspaceOf(t, "go-lib", "go-app")
	r, err := query.DocSet(c, "go-lib:service", "Package service runs the fixture use cases.")
	mustContain(t, run(t, r, err), "github.com/fixture/lib/service  service/service.go:1  doc ok")
}

func TestDocSetAddressErrorsCarryAFreshHeader(t *testing.T) {
	c := workspaceOf(t, "go-lib", "go-app")
	for _, addr := range []string{"go-lib:nope/", "go-lib:nope", "nope/"} {
		r, err := query.DocSet(c, addr, "x")
		if !errors.Is(err, query.ErrNotFound) || r == nil || !r.Header.Fresh || len(r.Header.Stale) > 0 {
			t.Fatalf("%s: %v %+v", addr, err, r)
		}
	}
}

func TestDocSetGoModuleNotesOtherPackageComments(t *testing.T) {
	c := tempWorkspace(t, map[string]string{
		"go.mod": "module x/m\n",
		"doc.go": "package m\n",
		"a.go":   "// Package m is old.\npackage m\n",
	})
	r, err := query.DocSet(c, "x/m", "Package m is new.")
	mustContain(t, run(t, r, err), "doc.go:1  doc ok  Package m is new.",
		"a.go also carries a package comment; keep one (the doc.go one is the package doc)")
}
