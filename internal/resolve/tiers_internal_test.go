package resolve

import (
	"math"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

func TestParamRange(t *testing.T) {
	for _, tc := range []struct {
		sig, name string
		lo, hi    int
		ok        bool
	}{
		{"func (m *MemStore) Put(key, value string) error", "Put", 2, 2, true},
		{"func Printf(format string, args ...any)", "Printf", 1, math.MaxInt, true},
		{"func Map[K comparable, V any](m map[K]V, f func(K, V) bool) []K", "Map", 2, 2, true},
		{"func NewMemStore() *MemStore", "NewMemStore", 0, 0, true},
		{"load(name: string): Environment | undefined", "load", 1, 1, true},
		{"export function f<T extends Map<string, number>>(a: T, b?: number, c = 1, ...rest: T[])", "f", 1, math.MaxInt, true},
		{"function g(this: Window, cb: (x: number) => void)", "g", 1, 1, true},
		{"func Reload() error", "Load", 0, 0, false},
		{"type T struct", "T", 0, 0, false},
		{"func Broken(a, b", "Broken", 0, 0, false},
		{"def get(self, key, default=None, *args, **kwargs)", "get", 2, math.MaxInt, true},
		{"def f(a, /, b, *, c: int = 1)", "f", 2, 3, true},
		{"public void move(List<? extends T> items, String... rest)", "move", 1, math.MaxInt, true},
		{"public void put(String key, int value)", "put", 2, 2, true},
	} {
		params, ok := paramList(tc.sig, tc.name)
		lo, hi := 0, 0
		if ok {
			lo, hi = paramRange(params)
		}
		if lo != tc.lo || hi != tc.hi || ok != tc.ok {
			t.Errorf("%s: got %d %d %v", tc.sig, lo, hi, ok)
		}
	}
}

func TestAdmits(t *testing.T) {
	fn := store.Target{Kind: "function", Name: "F", Signature: "func F(a int)"}
	if !admits(fn, 1) || admits(fn, 2) || !admits(fn, -1) {
		t.Fatal("arity of F")
	}
	if !admits(store.Target{Kind: "field", Name: "F"}, 3) {
		t.Fatal("a field admits any arity")
	}
	if !admits(store.Target{Kind: "method", Name: "F", Signature: "F"}, 3) {
		t.Fatal("an unparseable signature admits any arity")
	}
	py := store.Target{Kind: "method", Name: "get", Signature: "def get(self, key)", Lang: "python"}
	if !admits(py, 1) || admits(py, 2) {
		t.Fatal("self is not an argument")
	}
	if py.Kind = "function"; admits(py, 1) {
		t.Fatal("a function's first parameter counts")
	}
	ctor := store.Target{Kind: "method", Name: "<init>", Qualified: "Outer.MemStore.<init>",
		Signature: "public MemStore(int size)", Lang: "java"}
	if !admits(ctor, 1) || admits(ctor, 0) {
		t.Fatal("constructor arity")
	}
}

func TestGoPackageName(t *testing.T) {
	for raw, want := range map[string]string{
		"github.com/fixture/lib/store": "store", "gopkg.in/yaml.v3": "yaml", "github.com/x/y/v2": "y",
		"github.com/mattn/go-sqlite3": "sqlite3", "fmt": "fmt",
	} {
		if got := goPackageName(store.Import{Raw: raw}); got != want {
			t.Errorf("%s: %s", raw, got)
		}
	}
	for alias, want := range map[string]string{"st": "st", "_": "", ".": ""} {
		if got := goPackageName(store.Import{Raw: "a/b", Alias: alias}); got != want {
			t.Errorf("alias %s: %s", alias, got)
		}
	}
}

func TestPickCapsAmbiguousCandidates(t *testing.T) {
	many := make([]store.Target, MaxCandidates+1)
	for i := range many {
		many[i].ID = int64(i + 1)
	}
	if out := pick(many, ConfidenceUnique); out.confidence != "" || out.count != MaxCandidates+1 {
		t.Fatalf("%+v", out)
	}
	if out := pick(many[:2], ConfidenceUnique); out.confidence != ConfidenceAmbiguous || len(out.dst) != 2 {
		t.Fatalf("%+v", out)
	}
	if out := pick(nil, ConfidenceUnique); out.confidence != "" || out.count != 0 {
		t.Fatalf("%+v", out)
	}
}

// indexOf indexes the targets of a map by name.
func indexOf(byName map[string][]store.Target) *targetIndex {
	var all []store.Target
	for _, ts := range byName {
		all = append(all, ts...)
	}
	return newTargetIndex(byID(all))
}

// memResolver is a resolver over in-memory symbols and imports; the
// member cache answers the one lookup of a package variable (x in
// module 7) the cases make, so no store is read.
func memResolver() *refResolver {
	return &refResolver{children: map[int64][]int64{1: {2}}, subtrees: map[int64]map[int64]bool{},
		memberCache: map[string][]store.Target{"x\x007": nil},
		uniques:     map[uniqueKey]outcome{}, idx: indexOf(map[string][]store.Target{
			"Get": {
				{ID: 10, FileID: 1, ModuleID: 1, Kind: "method", Name: "Get", Qualified: "A.Get", Lang: "go"},
				{ID: 11, FileID: 2, ModuleID: 2, Kind: "method", Name: "Get", Qualified: "B.Get", Lang: "go"},
				{ID: 12, FileID: 3, ModuleID: 3, Kind: "function", Name: "Get", Qualified: "Get", Lang: "go"},
				{ID: 13, FileID: 4, ModuleID: 4, Kind: "method", Name: "Get", Qualified: "x.ts/C.Get", Lang: "typescript"},
			},
			"Load": {
				{ID: 30, FileID: 1, ModuleID: 1, Kind: "function", Name: "Load", Qualified: "Load", Lang: "go"},
				{ID: 31, FileID: 2, ModuleID: 2, Kind: "var", Name: "Load", Qualified: "Load", Lang: "go"},
				{ID: 32, FileID: 1, ModuleID: 1, Kind: "method", Name: "Load", Qualified: "A.Load", Lang: "go"},
			},
			"run": {{ID: 20, FileID: 4, ModuleID: 4, Kind: "method", Name: "run", Qualified: "x.ts/C.run", Lang: "typescript"},
				{ID: 21, FileID: 4, ModuleID: 4, Kind: "method", Name: "run", Qualified: "x.ts/D.run", Lang: "typescript"}},
		}),
		imports: map[int64][]store.Import{
			5: {{Raw: "example.com/one", ResolvedModuleID: 1}, {Raw: "fmt"}},
			6: {{Raw: "./x", Alias: "ns", ResolvedModuleID: 4}},
		}}
}

func TestResolveTiersInMemory(t *testing.T) {
	r := memResolver()
	for name, tc := range map[string]struct {
		ref  store.Reference
		conf string
		dst  int
	}{
		"import with descendants": {store.Reference{Kind: "call", Name: "Load", Qualifier: "one", FileID: 5, Lang: "go"}, ConfidenceAmbiguous, 2},
		"pkg.Name is no method":   {store.Reference{Kind: "call", Name: "Get", Qualifier: "one", FileID: 5, Lang: "go"}, "", 0},
		"pkg.Var.Method":          {store.Reference{Kind: "call", Name: "Get", Qualifier: "one.Default", FileID: 5, Lang: "go"}, ConfidenceAmbiguous, 2},
		"external import":         {store.Reference{Kind: "call", Name: "Get", Qualifier: "fmt", FileID: 5, Lang: "go"}, "", 0},
		"go bare local":           {store.Reference{Kind: "call", Name: "Get", FileID: 9, ModuleID: 3, Lang: "go"}, ConfidenceLocal, 1},
		"go bare elsewhere":       {store.Reference{Kind: "call", Name: "Get", FileID: 9, ModuleID: 7, Lang: "go"}, "", 0},
		"ts receiver is no file":  {store.Reference{Kind: "call", Name: "run", Qualifier: "repo", Enclosing: "x.ts/D.go", FileID: 4, ModuleID: 4, Lang: "typescript"}, ConfidenceAmbiguous, 2},
		"receiver methods only":   {store.Reference{Kind: "call", Name: "Get", Qualifier: "x", FileID: 9, ModuleID: 7, Lang: "go"}, ConfidenceAmbiguous, 2},
		"ts namespace import":     {store.Reference{Kind: "call", Name: "Get", Qualifier: "ns.inner", FileID: 6, Lang: "typescript"}, ConfidenceImport, 1},
		"ts this member":          {store.Reference{Kind: "call", Name: "run", Qualifier: "this", Enclosing: "x.ts/D.go", FileID: 4, ModuleID: 4, Lang: "typescript"}, ConfidenceLocal, 1},
		"config kind":             {store.Reference{Kind: "config", Name: "Get", Lang: "go"}, "", 0},
		"no candidate":            {store.Reference{Kind: "call", Name: "Nope", Qualifier: "x", Lang: "go"}, "", 0},
	} {
		out := r.resolve(tc.ref)
		if out.confidence != tc.conf || len(out.dst) != tc.dst {
			t.Errorf("%s: %+v", name, out)
		}
	}
}
