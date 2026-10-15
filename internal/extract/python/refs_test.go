package python_test

import (
	"github.com/muralx/codedoc/internal/testutil"
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
)

const refSrc = `from __future__ import annotations
import a.b as c, d
from ..x.y import (
    p as q,  # comment
    r,
)
from z import *
import os


@dec
@pkg.wrap
@pkg.make(1)
class C(Base, pkg.Mixin, Generic[T], object, metaclass=Meta):
    def run(self, s: Optional[pkg.Store], n: "Fwd" = 1) -> list[Item]:
        self.items.get(Item(1), k=2, *s, **t)
        helper(x for x in y)
        print(len(s))
        self.v = os.environ["APP_PORT"]
        os.getenv("home")
        os.environ.get(name, "x.y")
        cfg.load("app.db.url", f"{x}.y", "Not a key", b"BYTES_KEY")
        return KeyError("x")

    def __init__(self):
        self.store = make(Store())
`

func ref(kind extract.RefKind, name, qual, enc string, line, arity int) extract.Reference {
	return extract.Reference{Kind: kind, Name: name, Qualifier: qual, Enclosing: enc, Line: line, Arity: arity}
}

func TestReferences(t *testing.T) {
	res := extractSrc(t, "pkg/c.py", refSrc)
	const cls, run, ini = "C", "C.run", "C.__init__"
	want := []extract.Reference{
		ref(extract.RefCall, "dec", "", cls, 11, 1),
		ref(extract.RefCall, "wrap", "pkg", cls, 12, 1),
		ref(extract.RefCall, "make", "pkg", cls, 13, 1),
		ref(extract.RefExtend, "Base", "", cls, 14, -1),
		ref(extract.RefExtend, "Mixin", "pkg", cls, 14, -1),
		ref(extract.RefExtend, "Generic", "", cls, 14, -1),
		ref(extract.RefType, "Store", "pkg", run, 15, -1),
		ref(extract.RefType, "Item", "", run, 15, -1),
		ref(extract.RefCall, "get", "self.items", run, 16, 4),
		ref(extract.RefField, "items", "self", run, 16, -1),
		ref(extract.RefCall, "Item", "", run, 16, 1),
		ref(extract.RefInstantiate, "Item", "", run, 16, 1),
		ref(extract.RefCall, "helper", "", run, 17, 1),
		ref(extract.RefField, "v", "self", run, 19, -1),
		ref(extract.RefConfig, "APP_PORT", extract.ConfigAccessor, run, 19, -1),
		ref(extract.RefField, "environ", "os", run, 19, -1),
		ref(extract.RefCall, "getenv", "os", run, 20, 1),
		ref(extract.RefConfig, "home", extract.ConfigAccessor, run, 20, -1),
		ref(extract.RefCall, "get", "os.environ", run, 21, 2),
		ref(extract.RefConfig, "name", extract.ConfigAccessorConst, run, 21, -1),
		ref(extract.RefConfig, "x.y", extract.ConfigLiteral, run, 21, -1),
		ref(extract.RefField, "environ", "os", run, 21, -1),
		ref(extract.RefCall, "load", "cfg", run, 22, 4),
		ref(extract.RefConfig, "app.db.url", extract.ConfigLiteral, run, 22, -1),
		ref(extract.RefField, "store", "self", ini, 26, -1),
		ref(extract.RefCall, "make", "", ini, 26, 1),
		ref(extract.RefCall, "Store", "", ini, 26, 0),
		ref(extract.RefInstantiate, "Store", "", ini, 26, 0),
	}
	if !reflect.DeepEqual(testutil.WithoutCols(res.References), want) {
		for _, r := range res.References {
			t.Logf("%+v", r)
		}
		t.Errorf("references differ")
	}
}

func TestImports(t *testing.T) {
	res := extractSrc(t, "pkg/c.py", refSrc)
	want := []extract.Import{
		{Raw: "a.b", Alias: "c", Line: 2},
		{Raw: "d", Line: 2},
		{Raw: "..x.y", Names: []string{"p=q", "r"}, Line: 3},
		{Raw: "z", Names: []string{"*"}, Line: 7},
		{Raw: "os", Line: 8},
	}
	if !reflect.DeepEqual(res.Imports, want) {
		t.Errorf("got %+v", res.Imports)
	}
}
