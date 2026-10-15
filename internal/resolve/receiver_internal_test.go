package resolve

import (
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/store"
)

func TestGoReceiver(t *testing.T) {
	for sig, want := range map[string][2]string{
		"func (svc *Service) Lookup(key string) string": {"svc", "Service"},
		"func (s Set[T]) Len() int":                     {"s", "Set"},
		"func (Service) Name() string":                  {"", ""},
		"func (_ *Service) Name() string":               {"", ""},
		"func New() *Service":                           {"", ""},
	} {
		if name, typ := goReceiver(sig); name != want[0] || typ != want[1] {
			t.Errorf("%s: %s %s", sig, name, typ)
		}
	}
}

func TestFieldTypeName(t *testing.T) {
	for _, tc := range []struct{ fam, sig, want string }{
		{"go", "s store.Store", "store.Store"},
		{"go", "a, b *Node", "Node"},
		{"go", "store.Store", "store.Store"},
		{"go", "items Set[string]", "Set"},
		{"go", "data map[string]string", ""},
		{"go", "ch chan int", ""},
		{"java", "private final Store store", "Store"},
		{"java", "private final Map<String, String> data", "Map"},
		{"java", `static final String KIND = "memory"`, "String"},
		{"java", "int[] counts", ""},
		{"java", "x", ""},
		{"py", "self.store = store", ""},
	} {
		if got := typeNameOf(tc.fam, declaredText(tc.fam, extract.StepField, "field", tc.sig)); got != tc.want {
			t.Errorf("%s %q: %q", tc.fam, tc.sig, got)
		}
	}
}

func TestEnclosingClass(t *testing.T) {
	for _, tc := range []struct {
		ref  store.Reference
		want string
	}{
		{store.Reference{Enclosing: "Service.lookup", EnclosingKind: "method"}, "Service"},
		{store.Reference{Enclosing: "Service", EnclosingKind: "class"}, "Service"},
		{store.Reference{Enclosing: "src/x.ts/C.m", EnclosingKind: "method"}, "src/x.ts/C"},
		{store.Reference{Enclosing: "src/x.ts/f", EnclosingKind: "function"}, ""},
		{store.Reference{Enclosing: "f", EnclosingKind: "function"}, ""},
	} {
		if got := enclosingClass(tc.ref); got != tc.want {
			t.Errorf("%+v: %q", tc.ref, got)
		}
	}
}
