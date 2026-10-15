package query_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

// kitTest is a Go test of the deps-go fixture: it calls kit.New itself
// and reaches Client.Do and Client.Close through app.run.
const kitTest = `package main

import (
	"testing"

	"github.com/acme/kit"
)

func TestRun(t *testing.T) {
	a := &app{client: kit.New("test")}
	if err := a.run(); err != nil {
		t.Fatal(err)
	}
}
`

// testedDepsWorkspace is depsWorkspace with kitTest added to deps-go as
// main_test.go, the working directory in that repo.
func testedDepsWorkspace(t *testing.T) *query.Ctx {
	t.Helper()
	c := depsWorkspace(t)
	goRepo := c.Workspace.Repos[1]
	if err := os.WriteFile(filepath.Join(goRepo.Path, "main_test.go"), []byte(kitTest), 0o644); err != nil {
		t.Fatal(err)
	}
	c.Repo, c.Cwd = goRepo, goRepo.Path
	return c
}

// tests of a dependency symbol are the tests of the workspace that use
// it: directly, and with --transitive through the workspace code that
// does. Never the nearest workspace names.
func TestTestsOfADependencySymbol(t *testing.T) {
	c := testedDepsWorkspace(t)
	r, err := query.Tests(c, kitKey+"New", query.GraphOpts{})
	got := text(t, r, err)
	wants(t, got, "== tests of New [dep github.com/acme/kit@v1.4.0] (1)\nTestRun  main_test.go:10  a := &app{client: kit.New(\"test\")}  [test]\n")
	// Close is reached only through app.run: no test uses it directly.
	r, err = query.Tests(c, "Client.Close", query.GraphOpts{})
	wants(t, text(t, r, err), "== tests of Base.Close [dep github.com/acme/kit@v1.4.0] (0)\n", "Client.Close is declared on Base")
	r, err = query.Tests(c, "Client.Close", query.GraphOpts{Transitive: true})
	got = text(t, r, err)
	wants(t, got, "== tests of Base.Close [dep github.com/acme/kit@v1.4.0] (1)\nTestRun  main_test.go:11  ", "via app.run")
	if strings.Contains(got, "no exact match") {
		t.Fatalf("workspace candidates for a dependency symbol:\n%s", got)
	}
}

// impact of a dependency symbol is the workspace code a new version of
// it would touch: its callers, the other references, and their tests.
func TestImpactOfADependencySymbol(t *testing.T) {
	c := testedDepsWorkspace(t)
	c.Command = "codedoc impact Client.Close"
	r, err := query.Impact(c, "Client.Close", query.ImpactOpts{})
	got := text(t, r, err)
	wants(t, got, "Base.Close  ", "[dep github.com/acme/kit@v1.4.0]\n",
		"== callers (1)\napp.run  main.go:16  return a.client.Close()\n",
		"== tests (1)\nTestRun  main_test.go:11  via app.run\n", "Client.Close is declared on Base")
	if strings.Contains(got, "no exact match") || strings.Contains(got, "exported") {
		t.Fatalf("got\n%s", got)
	}
	// A type: the references to it.
	r, err = query.Impact(c, kitKey+"Client", query.ImpactOpts{})
	wants(t, text(t, r, err), "== references (1)\napp.client  main.go:8  client *kit.Client\n")
	// One section alone, and a section a dependency symbol has none of.
	r, err = query.Impact(c, "Client.Close", query.ImpactOpts{Only: "callers"})
	if got = text(t, r, err); !strings.Contains(got, "== callers (1)\n") || strings.Contains(got, "== tests") {
		t.Fatalf("got\n%s", got)
	}
	r, err = query.Impact(c, "Client.Close", query.ImpactOpts{Only: "config"})
	if got = text(t, r, err); strings.Contains(got, "== ") {
		t.Fatalf("got\n%s", got)
	}
}
