package query_test

import (
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/query"
)

// undefinedWorkspace has code reading APP_DATA and APP_HOME through
// os.Getenv with no definition, APP_MODE defined in .env and read, and
// APP_NOTE only as a key-like literal.
func undefinedWorkspace(t *testing.T) *query.Ctx {
	t.Helper()
	c := tempWorkspace(t, map[string]string{
		"go.mod": "module x/m\n",
		".env":   "APP_MODE=dev\n",
		"m.go": "package m\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\n" +
			"// Data returns the data directory.\nfunc Data() string { return os.Getenv(\"APP_DATA\") }\n\n" +
			"// Home returns the home directory.\nfunc Home() string { return os.Getenv(\"APP_HOME\") }\n\n" +
			"// Mode returns the mode.\nfunc Mode() string { return os.Getenv(\"APP_MODE\") }\n\n" +
			"// Note prints a note.\nfunc Note() { fmt.Println(\"APP_NOTE\") }\n\n" +
			"var top = os.Getenv(\"APP_DATA\")\n",
	})
	if err := index.ResolvePending(c.Store, index.Options{Workspace: "w"}); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestConfigUndefinedKeyReads(t *testing.T) {
	c := undefinedWorkspace(t)
	r, err := query.Config(c, "APP_DATA", query.ConfigOpts{})
	mustContain(t, run(t, r, err), "== defines APP_DATA (0)", "== reads APP_DATA (2)",
		"m.go\n  Data  :9  func Data() string  Data returns the data directory.  [undefined]",
		"  top  :20  var top = os.Getenv(\"APP_DATA\")  [undefined]")
	r, err = query.Config(c, "APP_NOTE", query.ConfigOpts{Reads: true})
	mustContain(t, run(t, r, err), "== reads APP_NOTE (1)", "\nNote  m.go:18  func Note()  Note prints a note.  [undefined]")
	r, err = query.Config(c, "APP_MODE", query.ConfigOpts{})
	text := run(t, r, err)
	mustContain(t, text, "== defines APP_MODE (1)", "== reads APP_MODE (1)")
	if strings.Contains(text, "[undefined]") {
		t.Errorf("a defined key's reads are not undefined:\n%s", text)
	}
}

func TestConfigGlobMixesDefinedAndUndefined(t *testing.T) {
	c := undefinedWorkspace(t)
	r, err := query.Config(c, "APP_*", query.ConfigOpts{Reads: true})
	text := run(t, r, err)
	mustContain(t, text, "== config keys matching APP_* (3)",
		"APP_DATA  defines:0 references:0 reads:2  [undefined]",
		"APP_HOME  defines:0 references:0 reads:1  [undefined]",
		"APP_MODE  defines:1 references:0 reads:1 in r\n", "== reads APP_HOME (1)")
	if strings.Contains(text, "APP_NOTE") {
		t.Errorf("a glob lists no key-like literal:\n%s", text)
	}
}
