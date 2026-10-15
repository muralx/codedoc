package resolve

import "testing"

func TestGoResultTypeName(t *testing.T) {
	for sig, want := range map[string]string{
		"func New(name string) *Client":              "Client",
		"func Open(p string) (*DB, error)":           "DB",
		"func Open() (db *kit.DB, err error)":        "kit.DB",
		"func Make[T any](xs []T) List[T]":           "List",
		"func With(cb func(int) string) store.Store": "store.Store",
		"func Name() string":                         "string",
		"func Run()":                                 "",
		"func All() []Item":                          "",
		"func Pair() (map[string]int, error)":        "",
		"func Broken(":                               "",
		"func Broken() (":                            "",
		"var hook":                                   "",
	} {
		if got := typeNameOf("go", goResultText(sig)); got != want {
			t.Errorf("result type of %q = %q, want %q", sig, got, want)
		}
	}
}

func TestGoVarType(t *testing.T) {
	for sig, want := range map[string]string{
		"var shared = kit.Alpha{}":    "kit.Alpha",
		"typed *kit.Beta":             "kit.Beta",
		"var list List[int]":          "List",
		"cache = &Cache{":             "Cache",
		"var buf = new(bytes.Buffer)": "bytes.Buffer",
		"built = kit.NewAlpha()":      "kit.NewAlpha()",
		"var local = build(1)":        "build()",
		"var opaque = make(chan int)": "",
		"var names []string":          "",
		"var n = 3":                   "",
		"var fn = func() {}":          "",
	} {
		expr, call := goVarType(sig)
		if call {
			expr += "()"
		}
		if expr != want {
			t.Errorf("goVarType(%q) = %q, want %q", sig, expr, want)
		}
	}
}
