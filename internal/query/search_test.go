package query_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
)

// keys returns the entry keys of the first section.
func keys(r *output.Response) []string {
	out := make([]string, len(r.Sections[0].Entries))
	for i, e := range r.Sections[0].Entries {
		out[i] = e.Key
	}
	return out
}

func search(t *testing.T, c *query.Ctx, text string, o query.SearchOpts) *output.Response {
	t.Helper()
	r, err := query.Search(c, text, o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSearchRanksDocMatchFirst(t *testing.T) {
	c := workspaceOf(t, "go-lib", "go-app")
	c.Embedder = embed.NewFake(64)
	r := search(t, c, "answers lookups", query.SearchOpts{})
	if got := keys(r); len(got) == 0 || got[0] != libService+"Service" {
		t.Fatalf("%v\n%s", got, render(t, r))
	}
	if strings.Contains(r.Note, "text search only") {
		t.Fatal(r.Note)
	}
	e := r.Sections[0].Entries[0]
	if e.Doc != "Service answers lookups through a Store." || e.Loc != "go-lib:service/service.go:11" {
		t.Fatalf("%+v", e)
	}
	r = search(t, c, "persist values by key", query.SearchOpts{Kind: "interface"})
	if got := keys(r); len(got) == 0 || got[0] != libStore+"Store" {
		t.Fatalf("%v", got)
	}
}

func TestSearchFilters(t *testing.T) {
	c := workspaceOf(t, "go-lib", "py-lib")
	c.Embedder = embed.NewFake(64)
	py, err := c.Store.RepoByPath(c.Workspace.Repos[1].Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range search(t, c, "store value key", query.SearchOpts{Lang: "py"}).Sections[0].Entries {
		if e.RepoID != py.ID {
			t.Fatalf("lang filter let %+v through", e)
		}
	}
	r := search(t, c, "store value key", query.SearchOpts{Kind: "module"})
	for _, e := range r.Sections[0].Entries {
		if e.Extra["kind"] != "module" {
			t.Fatalf("kind module let %+v through", e)
		}
	}
	c.Repo, c.Filter = c.Workspace.Repos[0], true
	lib, err := c.Store.RepoByPath(c.Repo.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range search(t, c, "store value key", query.SearchOpts{}).Sections[0].Entries {
		if e.RepoID != lib.ID {
			t.Fatalf("repo filter let %+v through", e)
		}
	}
	if n := len(search(t, c, "store value key", query.SearchOpts{Limit: 3}).Sections[0].Entries); n != 3 {
		t.Fatalf("limit: %d", n)
	}
	if _, err := query.Search(c, "x", query.SearchOpts{Limit: -1}); err == nil {
		t.Fatal("negative limit accepted")
	}
}

func TestSearchTextOnly(t *testing.T) {
	c := workspaceOf(t, "go-lib")
	// The name Lookup matches the stem of lookups before the doc
	// "answers lookups" does.
	r := search(t, c, "lookups", query.SearchOpts{})
	if got := keys(r); len(got) < 2 || got[0] != libService+"Service.Lookup" || got[1] != libService+"Service" ||
		!strings.Contains(r.Note, "no embedding model") {
		t.Fatalf("%v %q", got, r.Note)
	}
	c.Embedder = embed.NewLazy(64, func() (embed.Embedder, error) { return nil, errors.New("no runtime") }, nil)
	r = search(t, c, "lookups", query.SearchOpts{})
	if got := keys(r); len(got) == 0 || !strings.Contains(r.Note, "embedding model unavailable") {
		t.Fatalf("%v %q", got, r.Note)
	}
	if r := search(t, c, "!!!", query.SearchOpts{}); len(r.Sections[0].Entries) != 0 {
		t.Fatalf("%s", render(t, r))
	}
}

func TestSearchWithoutWordsOrWithUnknownFilters(t *testing.T) {
	c := workspaceOf(t, "go-lib")
	c.Embedder = embed.NewFake(64)
	r := search(t, c, " *() ", query.SearchOpts{})
	if len(r.Sections[0].Entries) != 0 || !strings.Contains(r.Note, "query has no searchable words") {
		t.Fatalf("%+v %q", r.Sections[0].Entries, r.Note)
	}
	for _, o := range []query.SearchOpts{{Kind: "functions"}, {Lang: "golang"}} {
		_, err := query.Search(c, "store", o)
		if err == nil || !strings.Contains(err.Error(), "valid: ") {
			t.Fatalf("%+v: %v", o, err)
		}
	}
}
