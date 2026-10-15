package extract_test

import (
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
)

func TestLooksLikeKey(t *testing.T) {
	for s, want := range map[string]bool{
		"PYLIB_MODE": true, "ABC": true, "AB": false, "Abc_D": false, "_ABC": false, "A1_": true,
		"lib.mode": true, "a.b.c": true, "spring.data-source.url": true, "a_b.c": true,
		"lib": false, "Lib.mode": false, "lib.": false, ".lib": false, "a..b": false,
		"hello world": false, "": false, "a.B": false,
	} {
		if got := extract.LooksLikeKey(s); got != want {
			t.Errorf("%q: got %v", s, got)
		}
	}
}

func TestIsConfigAccessor(t *testing.T) {
	for _, c := range []struct {
		family, qualifier, name string
		want                    bool
	}{
		{"py", "os", "getenv", true},
		{"py", "", "getenv", true},
		{"py", "os.environ", "get", true},
		{"py", "self.cfg", "get", false},
		{"java", "System", "getenv", true},
		{"java", "System", "getProperty", true},
		{"java", "env", "getProperty", true},
		{"java", "props", "getProperty", false},
		{"go", "os", "Getenv", true},
		{"go", "os", "LookupEnv", true},
		{"go", "viper", "GetString", true},
		{"go", "viper", "Get", true},
		{"go", "viper", "Set", false},
		{"go", "cfg", "GetString", false},
		{"ts", "os", "Getenv", false},
	} {
		if got := extract.IsConfigAccessor(c.family, c.qualifier, c.name); got != c.want {
			t.Errorf("%+v: got %v", c, got)
		}
	}
	if !extract.IsConfigMap("py", "os.environ") || extract.IsConfigMap("py", "os.path") ||
		extract.IsConfigMap("java", "os.environ") || !extract.IsConfigMap("ts", "process.env") {
		t.Error("IsConfigMap")
	}
}

func TestPlaceholderKeys(t *testing.T) {
	for s, want := range map[string][]string{
		"${lib.mode}":     {"lib.mode"},
		"${x:default}":    {"x"},
		"a ${A} b ${ B }": {"A", "B"},
		"${a:${b}}":       {"a", "b"},
		"${:x}":           nil,
		"${unterminated":  nil,
		"no placeholders": nil,
		"$x ${y:}":        {"y"},
	} {
		if got := extract.PlaceholderKeys(s); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %q", s, got)
		}
	}
}
