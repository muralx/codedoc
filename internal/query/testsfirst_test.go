package query_test

import (
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/query"
)

// testsFirstWorkspace has production code in m.go and, in a_test.go
// (which sorts before it), a test and a helper that read APP_DATA and
// call Data, so path order alone would list the test file first.
func testsFirstWorkspace(t *testing.T) *query.Ctx {
	t.Helper()
	c := tempWorkspace(t, map[string]string{
		"go.mod": "module x/m\n",
		"m.go": "package m\n\nimport \"os\"\n\n// Data returns the data directory.\n" +
			"func Data() string { return os.Getenv(\"APP_DATA\") }\n\n// Use uses the data directory.\n" +
			"func Use() string { return Data() }\n\n// Cfg is the configuration.\ntype Cfg struct{}\n",
		"a_test.go": "package m\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n" +
			"func helper() string { var _ Cfg; return os.Getenv(\"APP_DATA\") + Data() }\n\n" +
			"func TestData(t *testing.T) { _ = Data() + helper() }\n",
	})
	if err := index.ResolvePending(c.Store, index.Options{Workspace: "w"}); err != nil {
		t.Fatal(err)
	}
	return c
}

// assertOrder fails unless the parts appear in text in order.
func assertOrder(t *testing.T, text string, parts ...string) {
	t.Helper()
	at := 0
	for _, p := range parts {
		i := strings.Index(text[at:], p)
		if i < 0 {
			t.Fatalf("%q missing or out of order in:\n%s", p, text)
		}
		at += i + len(p)
	}
}

func TestConfigReadsListProductionBeforeTests(t *testing.T) {
	c := testsFirstWorkspace(t)
	r, err := query.Config(c, "APP_DATA", query.ConfigOpts{Reads: true})
	text := run(t, r, err)
	assertOrder(t, text, "== reads APP_DATA (2)", "\nData  m.go:6", "\nhelper  a_test.go:8")
	mustContain(t, text, "func helper() string  [test] [undefined]")
}

func TestCallersListProductionBeforeTests(t *testing.T) {
	c := testsFirstWorkspace(t)
	r, err := query.Callers(c, "Data", query.GraphOpts{Depth: 1})
	text := run(t, r, err)
	assertOrder(t, text, "\nUse  m.go:9  func Use() string { return Data() }\n", "a_test.go\n  helper  :8  ", "  TestData  :10  ")
	if strings.Count(text, "[test]") != 2 {
		t.Errorf("both test-file callers are tagged test:\n%s", text)
	}
	r, err = query.Callers(c, "Data", query.GraphOpts{Depth: 1, GroupByFile: true})
	assertOrder(t, run(t, r, err), "in r:m.go", "in r:a_test.go")
}

// TestNoTestsDropsEverythingTaggedTest checks that --no-tests leaves out
// test functions and the helpers of test files, which are tagged test.
func TestNoTestsDropsEverythingTaggedTest(t *testing.T) {
	c := testsFirstWorkspace(t)
	r, err := query.Callers(c, "Data", query.GraphOpts{Depth: 2, NoTests: true})
	if text := run(t, r, err); strings.Contains(text, "TestData") || strings.Contains(text, "helper") ||
		!strings.Contains(text, "Use") {
		t.Errorf("callers --no-tests:\n%s", text)
	}
	r, err = query.Callees(c, "TestData", query.GraphOpts{Depth: 1})
	mustContain(t, run(t, r, err), "  helper  :10  ", "[test]", "  Data  :10  ")
	r, err = query.Callees(c, "TestData", query.GraphOpts{Depth: 1, NoTests: true})
	if text := run(t, r, err); strings.Contains(text, "\nhelper  ") || !strings.Contains(text, "\nData  a_test.go:10  ") {
		t.Errorf("callees --no-tests:\n%s", text)
	}
	r, err = query.Refs(c, "Cfg", query.GraphOpts{})
	mustContain(t, run(t, r, err), "\nhelper  a_test.go:8  ", "[test]")
	r, err = query.Refs(c, "Cfg", query.GraphOpts{NoTests: true})
	if text := run(t, r, err); strings.Contains(text, "helper") {
		t.Errorf("refs --no-tests:\n%s", text)
	}
}

func TestFindAndTestsListProductionFirst(t *testing.T) {
	c := testsFirstWorkspace(t)
	r, err := query.Find(c, "*", query.FindOpts{Kind: "function"})
	assertOrder(t, run(t, r, err), "  Data  :6", "  Use  :9", "  helper  :8", "[test]", "  TestData  :10", "[test]")
	r, err = query.Tests(c, "Data", query.GraphOpts{})
	if text := run(t, r, err); !strings.Contains(text, "TestData") || strings.Contains(text, "\nhelper  ") {
		t.Errorf("tests lists test functions only:\n%s", text)
	}
}
