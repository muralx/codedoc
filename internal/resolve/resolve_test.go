package resolve_test

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

const (
	lib    = "go-lib:go:github.com/fixture/lib/"
	app    = "go-app:go:github.com/fixture/app:"
	domain = "ts-domain:ts:@fixture/domain:src/records.ts/"
	tsApp  = "ts-app:ts:@fixture/app:src/main.ts/"
)

// sym returns the id of the symbol with a stable key.
func (w *ws) sym(key string) int64 {
	w.t.Helper()
	sy, err := w.s.SymbolByKey(key)
	if err != nil {
		w.t.Fatalf("%s: %v", key, err)
	}
	return sy.ID
}

// keyOf returns the stable key of a symbol id.
func (w *ws) keyOf(id int64) string {
	w.t.Helper()
	sy, err := w.s.SymbolByID(id)
	if err != nil {
		w.t.Fatalf("%d: %v", id, err)
	}
	return sy.StableKey
}

// edge describes an edge as src -> dst [kind ref_kind confidence], with
// dst listing the candidate keys of an ambiguous edge joined by '|'.
func (w *ws) edge(e store.Edge) string {
	dst := ""
	if e.Dst != 0 {
		dst = w.keyOf(e.Dst)
	} else {
		var cands []string
		for _, c := range e.Candidates {
			cands = append(cands, w.keyOf(c))
		}
		sort.Strings(cands)
		dst = strings.Join(cands, "|")
	}
	return w.keyOf(e.Src) + " -> " + dst + " [" + e.Kind + " " + e.RefKind + " " + e.Confidence + "]"
}

// out returns the described edges leaving a symbol.
func (w *ws) out(key string, kinds ...string) []string {
	w.t.Helper()
	es, err := w.s.EdgesFrom(w.sym(key), kinds...)
	if err != nil {
		w.t.Fatal(err)
	}
	var got []string
	for _, e := range es {
		got = append(got, w.edge(e))
	}
	return got
}

// in returns the described edges ending at a symbol.
func (w *ws) in(key string, kinds ...string) []string {
	w.t.Helper()
	es, err := w.s.EdgesTo(w.sym(key), kinds...)
	if err != nil {
		w.t.Fatal(err)
	}
	var got []string
	for _, e := range es {
		got = append(got, w.edge(e))
	}
	return got
}

func (w *ws) want(got []string, want ...string) {
	w.t.Helper()
	for _, s := range want {
		if !slices.Contains(got, s) {
			w.t.Errorf("missing %s in\n%s", s, strings.Join(got, "\n"))
		}
	}
}

func TestResolveGoTiers(t *testing.T) {
	w := fixtureWS(t)
	w.indexAll()
	w.want(w.in(lib+"service:fallback", "calls"),
		lib+"service:Service.Lookup -> "+lib+"service:fallback [calls call local]")
	w.want(w.out(lib+"service:Service.Lookup", "calls"),
		lib+"service:Service.Lookup -> "+lib+"store:Store.Get [calls call import]")
	w.want(w.out(app+"run", "calls"), app+"run -> "+lib+"service:Service.Lookup [calls call import]")
	w.want(w.out(app+"main", "calls"),
		app+"main -> "+lib+"store:NewMemStore [calls call import]",
		app+"main -> "+lib+"service:New [calls call import]",
		app+"main -> "+app+"run [calls call local]")
	w.want(w.out(lib+"service:Service.Lookup", "references"),
		lib+"service:Service.Lookup -> "+lib+"service:Service.s [references field local]")
	w.want(w.out(app+"run", "references"), app+"run -> "+lib+"service:Service [references type import]")
	for _, impl := range []string{"MemStore", "NullStore"} {
		w.want(w.out(lib+"store:"+impl, "implements"),
			lib+"store:"+impl+" -> "+lib+"store:Store [implements implement local]")
	}
	if got := w.out(lib+"service:Service", "implements"); len(got) != 0 {
		t.Errorf("Service implements nothing: %v", got)
	}
}

// TestReceiverCallsDoNotResolveLocally checks that a call through a
// receiver never lands on an unrelated method of the same package
// (service.Cache.Get) but goes through the receiver: the enclosing
// method's receiver names its type, and a field of it the field's
// declared type.
func TestReceiverCallsDoNotResolveLocally(t *testing.T) {
	w := fixtureWS(t)
	w.indexAll()
	lookup := lib + "service:Service.Lookup"
	for _, e := range w.out(lookup, "calls") {
		if strings.Contains(e, "Cache.Get") {
			t.Errorf("svc.s.Get resolved to Cache.Get: %s", e)
		}
	}
	w.want(w.out(lookup, "calls"), lookup+" -> "+lib+"store:Store.Get [calls call import]")
	has := lib + "service:Cache.Has"
	w.want(w.out(has, "calls"), has+" -> "+lib+"service:Cache.Get [calls call local]")
	w.want(w.out(lib+"service:Cache.Get", "references"),
		lib+"service:Cache.Get -> "+lib+"service:Cache.items [references field local]")
}

func TestResolveUnresolvedReferencesStayInTheView(t *testing.T) {
	w := fixtureWS(t)
	w.indexAll()
	f, err := w.s.FileByPath(w.repo("go-app"), "main.go")
	if err != nil {
		t.Fatal(err)
	}
	refs, err := w.s.UnresolvedReferences(f.ID)
	if err != nil || len(refs) != 1 || refs[0].Name != "Println" {
		t.Fatalf("fmt.Println names no workspace symbol: %+v %v", refs, err)
	}
}

func TestResolveTSTiers(t *testing.T) {
	w := fixtureWS(t)
	w.indexAll()
	w.want(w.out(tsApp+"check", "calls"),
		tsApp+"check -> "+domain+"isHealthy [calls call import]",
		tsApp+"check -> "+domain+"MemoryRepo.load [calls call import]")
	w.want(w.out(tsApp+"check", "references"), tsApp+"check -> "+domain+"MemoryRepo [references instantiate import]")
	w.want(w.out(domain+"MemoryRepo", "implements"),
		domain+"MemoryRepo -> "+domain+"EnvironmentRepo [implements implement local]")
	test := "ts-domain:ts:@fixture/domain:src/records.test.ts/it:stores and checks an environment"
	w.want(w.out(test, "calls"),
		test+" -> "+domain+"isHealthy [calls call import]",
		test+" -> "+domain+"MemoryRepo.save [calls call import]",
		test+" -> "+domain+"MemoryRepo.load [calls call import]")
}

// usePut adds to go-app a function calling Put through a parameter of
// a type codedoc cannot see: the unique tier decides.
func usePut(t *testing.T, w *ws) string {
	t.Helper()
	write(t, w.repos[1].Path, "use.go", "package main\n\nfunc use(c client) error { return c.Put(\"k\", \"v\") }\n")
	return app + "use"
}

func TestReresolveAfterRemovingACandidate(t *testing.T) {
	w := fixtureWS(t)
	use := usePut(t, w)
	w.indexAll()
	w.want(w.out(use, "calls"), use+" -> "+lib+"store:MemStore.Put|"+lib+"store:NullStore.Put|"+lib+
		"store:Store.Put [calls call ambiguous]")
	if err := os.Remove(filepath.Join(w.repos[0].Path, "store", "null.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Ensure(w.s, w.repos[0], w.opts()); err != nil {
		t.Fatal(err)
	}
	w.want(w.out(use, "calls"), use+" -> "+lib+"store:MemStore.Put|"+lib+"store:Store.Put [calls call ambiguous]")
}

func TestReresolveTurnsALoneCandidateIntoAnEdge(t *testing.T) {
	w := fixtureWS(t)
	use := usePut(t, w)
	w.indexAll()
	root := w.repos[0].Path
	for _, f := range []string{"null.go", "store.go"} {
		if err := os.Remove(filepath.Join(root, "store", f)); err != nil {
			t.Fatal(err)
		}
	}
	src := "package store\n\ntype MemStore struct{}\n\nfunc (MemStore) Put(key, value string) error { return nil }\n"
	if err := os.WriteFile(filepath.Join(root, "store", "mem.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Ensure(w.s, w.repos[0], w.opts()); err != nil {
		t.Fatal(err)
	}
	w.want(w.out(use, "calls"), use+" -> "+lib+"store:MemStore.Put [calls call unique]")
}

func TestReresolveRestoresEdgesIntoARewrittenFile(t *testing.T) {
	w := fixtureWS(t)
	w.indexAll()
	p := filepath.Join(w.repos[0].Path, "service", "service.go")
	src, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(src, []byte("\nfunc Extra() {}\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Ensure(w.s, w.repos[0], w.opts()); err != nil {
		t.Fatal(err)
	}
	// run in go-app calls Service.Lookup, whose row was rewritten.
	w.want(w.out(app+"run", "calls"), app+"run -> "+lib+"service:Service.Lookup [calls call import]")
	w.want(w.out(lib+"store:MemStore", "implements"), lib+"store:MemStore -> "+lib+"store:Store [implements implement local]")
}

func TestReresolveImplementsWhenAnInterfaceChanges(t *testing.T) {
	w := fixtureWS(t)
	w.indexAll()
	p := filepath.Join(w.repos[0].Path, "store", "store.go")
	src, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	src = []byte(strings.Replace(string(src), "Put(key, value string) error\n}", "Put(key, value string) error\n\tDelete(key string)\n}", 1))
	if err := os.WriteFile(p, src, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Ensure(w.s, w.repos[0], w.opts()); err != nil {
		t.Fatal(err)
	}
	if got := w.in(lib+"store:Store", "implements"); len(got) != 0 {
		t.Fatalf("no struct has Delete: %v", got)
	}
}

// ensure runs the freshness check of every repo of the workspace.
func (w *ws) ensure() {
	w.t.Helper()
	for _, r := range w.repos {
		if _, err := index.Ensure(w.s, r, w.opts()); err != nil {
			w.t.Fatal(err)
		}
	}
}

// relay calls Lookup on the result of a call, a receiver whose type no
// declaration makes evident: the unique tier decides.
const relaySrc = `package main

import "github.com/fixture/lib/service"

func current() *service.Service { return nil }

func relay() string { return current().Lookup("k") }
`

func TestAddingASymbolTurnsAUniqueEdgeAmbiguousAndBack(t *testing.T) {
	w := fixtureWS(t)
	write(t, w.repos[1].Path, "relay.go", relaySrc)
	w.indexAll()
	lookup := lib + "service:Service.Lookup"
	unique := app + "relay -> " + lookup + " [calls call unique]"
	typed := app + "run -> " + lookup + " [calls call import]"
	w.want(w.out(app+"relay", "calls"), unique)
	other := filepath.Join(w.repos[0].Path, "service", "other.go")
	write(t, w.repos[0].Path, "service/other.go", "package service\n\ntype Other struct{}\n\n"+
		"func (Other) Lookup(key string) string { return key }\n")
	w.ensure()
	w.want(w.out(app+"relay", "calls"), app+"relay -> "+lib+"service:Other.Lookup|"+lookup+" [calls call ambiguous]")
	// run calls it through a parameter of type *service.Service: the
	// second Lookup does not make that call ambiguous.
	w.want(w.out(app+"run", "calls"), typed)
	if err := os.Remove(other); err != nil {
		t.Fatal(err)
	}
	w.ensure()
	w.want(w.out(app+"relay", "calls"), unique)
}

func TestBodyEditKeepsSymbolIDsAndEdgesInto(t *testing.T) {
	w := fixtureWS(t)
	w.indexAll()
	lookup := lib + "service:Service.Lookup"
	id := w.sym(lookup)
	edges, err := w.s.EdgesTo(id, "calls")
	if err != nil || len(edges) != 1 {
		t.Fatalf("%v %v", edges, err)
	}
	p := filepath.Join(w.repos[0].Path, "service", "service.go")
	src, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(src), "return fallback(key)", "return fallback(key + \"\")", 1)
	if err := os.WriteFile(p, []byte("\n"+edited), 0o644); err != nil {
		t.Fatal(err)
	}
	w.ensure()
	if got := w.sym(lookup); got != id {
		t.Fatalf("Service.Lookup id moved from %d to %d", id, got)
	}
	after, err := w.s.EdgesTo(id, "calls")
	if err != nil || len(after) != 1 || after[0].ID != edges[0].ID {
		t.Fatalf("edge into Service.Lookup was rebuilt: %v -> %v %v", edges, after, err)
	}
	impl := w.out(lib+"store:MemStore", "implements")
	w.want(impl, lib+"store:MemStore -> "+lib+"store:Store [implements implement local]")
}

func TestFieldTypeChangeReresolvesCallsThroughTheField(t *testing.T) {
	w := fixtureWS(t)
	write(t, w.repos[0].Path, "service/peek.go",
		"package service\n\nfunc (svc *Service) Peek(k string) { svc.s.Get(k) }\n")
	w.indexAll()
	peek := lib + "service:Service.Peek"
	w.want(w.out(peek, "calls"), peek+" -> "+lib+"store:Store.Get [calls call import]")
	p := filepath.Join(w.repos[0].Path, "service", "service.go")
	src, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(src), "struct{ s store.Store }", "struct{ s *Cache }", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	w.ensure()
	w.want(w.out(peek, "calls"), peek+" -> "+lib+"service:Cache.Get [calls call local]")
}
