package query_test

import (
	"errors"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

func TestBareNamePrefersTopLevelOverMembers(t *testing.T) {
	c := tempWorkspace(t, map[string]string{
		"go.mod": "module example.com/a\n",
		"a.go":   "package a\n\n// Find finds.\nfunc Find() {}\n\ntype S struct{}\n\n// Find finds in S.\nfunc (S) Find() {}\n",
		"b/b.go": "package b\n\ntype T struct{}\n\n// Get gets.\nfunc (T) Get() {}\n\ntype U struct{}\n\n// Get gets.\nfunc (U) Get() {}\n",
	})
	r, err := query.Symbol(c, "Find", query.SymbolOpts{})
	if err != nil || r.Sections[0].Entries[0].Key != "r:go:example.com/a:Find" {
		t.Fatalf("%v\n%s", err, render(t, r))
	}
	c.Strict = true
	if _, err := query.Symbol(c, "Get", query.SymbolOpts{}); !errors.Is(err, query.ErrAmbiguous) {
		t.Fatalf("two members stay ambiguous: %v", err)
	}
	if r, err := query.Symbol(c, "S.Find", query.SymbolOpts{}); err != nil || r.Sections[0].Entries[0].Key != "r:go:example.com/a:S.Find" {
		t.Fatalf("%v", err)
	}
}

// Addresses of dependency symbols carry a package id where a repo name
// goes: it holds '/' and '@' but no colon (deps spec section 3).
func TestParseAddrAcceptsPackageIDs(t *testing.T) {
	key := "npm/@scope/pkg@1.2.0:ts:@scope/pkg/server:dist/server.d.ts/Server.notify"
	if got := query.ParseAddr(key); got.Kind != query.AddrKey || got.Key != key {
		t.Errorf("stable key: %+v", got)
	}
	want := query.Addr{Kind: query.AddrPath, Repo: "go/github.com/spf13/cobra@v1.9.1", Path: "command.go", Name: "Command.Execute"}
	if got := query.ParseAddr("go/github.com/spf13/cobra@v1.9.1:command.go#Command.Execute"); got != want {
		t.Errorf("path form: %+v", got)
	}
}
