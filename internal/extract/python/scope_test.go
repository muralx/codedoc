package python_test

import (
	"slices"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
)

func hasRef(refs []extract.Reference, kind extract.RefKind, name, qual string) bool {
	return slices.ContainsFunc(refs, func(r extract.Reference) bool {
		return r.Kind == kind && r.Name == name && r.Qualifier == qual
	})
}

func TestFStringInterpolations(t *testing.T) {
	src := "class C:\n    def run(self):\n        return f\"{helper_fn()} {self.describe()!r:>{width()}} {self.url}\"\n"
	refs := extractSrc(t, "m.py", src).References
	for _, want := range []struct {
		kind       extract.RefKind
		name, qual string
	}{
		{extract.RefCall, "helper_fn", ""}, {extract.RefCall, "describe", "self"},
		{extract.RefCall, "width", ""}, {extract.RefField, "url", "self"},
	} {
		if !hasRef(refs, want.kind, want.name, want.qual) {
			t.Errorf("missing %s %s.%s in %+v", want.kind, want.qual, want.name, refs)
		}
	}
	for _, r := range refs {
		if r.Enclosing != "C.run" {
			t.Errorf("ref %+v enclosed by %q", r, r.Enclosing)
		}
	}
}

func TestConditionalModuleDefinitions(t *testing.T) {
	src := `import sys
if sys.version_info >= (3, 11):
    def compat():
        pass
    LIMIT = 1
elif sys.platform == "win32":
    def compat():
        pass
else:
    class Shim:
        def m(self):
            pass
try:
    from fast import impl
except ImportError:
    def impl():
        pass
finally:
    DONE = True
with open("x") as f:
    data = f.read()
def outer():
    if True:
        def local():
            pass
`
	res := extractSrc(t, "m.py", src)
	var got []string
	for _, s := range res.Symbols {
		got = append(got, string(s.Kind)+" "+s.Qualified+" "+s.Container)
	}
	want := []string{
		"function compat ", "const LIMIT ", "function compat~2 ", "class Shim ", "method Shim.m Shim",
		"function impl ", "const DONE ", "var data ", "function outer ",
	}
	if !slices.Equal(got, want) {
		t.Errorf("symbols:\n got %q\nwant %q", got, want)
	}
}

func TestAccessorSkipsComments(t *testing.T) {
	src := "import os\nos.getenv(\n    # the port\n    \"port\",\n)\nos.getenv(  # trailing\n    name, \"fallback\")\n"
	var keys []string
	for _, r := range extractSrc(t, "m.py", src).References {
		if r.Kind == extract.RefConfig {
			keys = append(keys, r.Name+":"+r.Qualifier)
		}
	}
	if !slices.Equal(keys, []string{"port:accessor", "name:accessor-const"}) {
		t.Errorf("config keys %q, want [port:accessor name:accessor-const]", keys)
	}
}
