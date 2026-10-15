package query_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
)

func configWorkspace(t *testing.T) *query.Ctx {
	t.Helper()
	return workspaceOf(t, "go-lib", "ts-domain", "py-lib", "java-lib", "cfg-repo")
}

func TestConfigKeySnapshot(t *testing.T) {
	c := configWorkspace(t)
	r, err := query.Config(c, "LIB_MODE", query.ConfigOpts{})
	text := run(t, r, err)
	snapshot(t, "config_lib_mode", text)
	mustContain(t, text, "== defines LIB_MODE (2)", "cfg-repo:.env:2  env  dev", "== references LIB_MODE (2)",
		"== reads LIB_MODE (1)", "\nMode  go-lib:service/service.go:28  func Mode() string")
}

func TestConfigGlobSnapshot(t *testing.T) {
	c := configWorkspace(t)
	r, err := query.Config(c, "*_MODE", query.ConfigOpts{})
	text := run(t, r, err)
	snapshot(t, "config_glob_mode", text)
	mustContain(t, text, "== config keys matching *_MODE (2)", "LIB_MODE  defines:2 references:2 reads:1 in cfg-repo",
		"== reads PYLIB_MODE (1)")
}

func TestConfigFlagsAndRepoFilter(t *testing.T) {
	c := configWorkspace(t)
	r, err := query.Config(c, "lib.mode", query.ConfigOpts{Reads: true})
	text := run(t, r, err)
	if strings.Contains(text, "== defines") || !strings.Contains(text, "== reads lib.mode (1)") ||
		!strings.Contains(text, "\nService.mode  src/main/java/com/fixture/service/Service.java:13") {
		t.Fatalf("--reads:\n%s", text)
	}
	r, err = query.Config(c, "lib.mode", query.ConfigOpts{Defines: true})
	text = run(t, r, err)
	if strings.Contains(text, "== reads") || strings.Contains(text, "== references") ||
		!strings.Contains(text, "java-lib:pom.xml:8  xml  dev") {
		t.Fatalf("--defines:\n%s", text)
	}
	c.Repo, c.Filter = c.Workspace.Repos[3], true // java-lib
	r, err = query.Config(c, "lib.mode", query.ConfigOpts{Defines: true})
	if text = run(t, r, err); strings.Contains(text, "cfg-repo:") || !strings.Contains(text, "== defines lib.mode (1)") {
		t.Fatalf("--repo java-lib:\n%s", text)
	}
	r, err = query.Config(c, "NOPE", query.ConfigOpts{})
	if text = run(t, r, err); !strings.Contains(text, "== config NOPE (0)\n") {
		t.Fatalf("no match is an empty section:\n%s", text)
	}
}

func TestConfigFileLevelReadAndOffset(t *testing.T) {
	c := tempWorkspace(t, map[string]string{
		"settings.py": "import os\n\nif os.getenv(\"TOP_KEY\"):\n    print(os.environ[\"TOP_KEY\"])\n",
		".env":        "TOP_KEY=1\n",
	})
	if err := index.ResolvePending(c.Store, index.Options{Workspace: "w"}); err != nil {
		t.Fatal(err)
	}
	r, err := query.Config(c, "TOP_KEY", query.ConfigOpts{Reads: true})
	mustContain(t, run(t, r, err), "== reads TOP_KEY (2)\nr:settings.py\n  :3  (file level)\n  :4  (file level)\n")
	c.Offset = 1
	r, err = query.Config(c, "TOP_KEY", query.ConfigOpts{Reads: true})
	if text := run(t, r, err); strings.Contains(text, "settings.py:3") || !strings.Contains(text, "settings.py:4") {
		t.Fatalf("--offset 1:\n%s", text)
	}
}

// manyKeysWorkspace defines 120 keys MANY_KEY_n, each read by code.
func manyKeysWorkspace(t *testing.T) *query.Ctx {
	t.Helper()
	var env, code strings.Builder
	code.WriteString("package m\n\nimport \"os\"\n\n")
	for i := 0; i < 120; i++ {
		fmt.Fprintf(&env, "MANY_KEY_%03d=value%d\n", i, i)
		fmt.Fprintf(&code, "func Read%d() string { return os.Getenv(\"MANY_KEY_%03d\") }\n", i, i)
	}
	c := tempWorkspace(t, map[string]string{".env": env.String(), "m.go": code.String(), "go.mod": "module x/m\n"})
	if err := index.ResolvePending(c.Store, index.Options{Workspace: "w"}); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestConfigGlobWithManyKeysFitsTheBudget(t *testing.T) {
	for _, budget := range []int{8000, 1500} {
		c := manyKeysWorkspace(t)
		c.Budget = budget
		text := renderBudget(t, c, "MANY_*", budget)
		if len(text) > budget || !strings.Contains(text, "== config keys matching MANY_* (120)") ||
			!strings.Contains(text, "MANY_KEY_000  defines:1 references:0 reads:1") {
			t.Fatalf("budget %d: key list missing (%d bytes)\n%s", budget, len(text), text)
		}
		if !strings.Contains(text, "more keys omitted") && !strings.Contains(text, " more: rerun with --offset") {
			t.Fatalf("budget %d: nothing states what was omitted\n%s", budget, text)
		}
	}
	c := manyKeysWorkspace(t)
	c.Budget = 8000
	r, err := query.Config(c, "MANY_*", query.ConfigOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sections) < 4 || len(r.Sections) >= 1+3*120 || !strings.Contains(r.Note, "more keys omitted") {
		t.Fatalf("sections %d note %q", len(r.Sections), r.Note)
	}
}

// renderBudget renders a config answer as text under budget.
func renderBudget(t *testing.T, c *query.Ctx, pattern string, budget int) string {
	t.Helper()
	r, err := query.Config(c, pattern, query.ConfigOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if _, err := output.Render(&b, r, "text", budget); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestSiblingsOfAConfigKey(t *testing.T) {
	c := workspaceOf(t, "go-lib", "cfg-repo")
	r, err := query.Siblings(c, "db.host")
	text := run(t, r, err)
	mustContain(t, text, "cfg-repo@", "== config keys under db besides db.host (1)\ndb.password  defines:2")
	r, err = query.Siblings(c, "aws_instance.web.ami")
	mustContain(t, run(t, r, err), "aws_instance.web.availability_zone  defines:1")
	r, err = query.Siblings(c, "API_URL")
	text = run(t, r, err)
	mustContain(t, text, "== top-level config keys besides API_URL", "DB_PASSWORD  defines:1", "LIB_MODE  ")
	if strings.Contains(text, "db.host") {
		t.Fatalf("a nested key is no top-level sibling:\n%s", text)
	}
}

func TestConfigUnknownKeyIsAnEmptySectionWithANote(t *testing.T) {
	c := workspaceOf(t, "cfg-repo")
	r, err := query.Config(c, "NOPE", query.ConfigOpts{})
	mustContain(t, run(t, r, err), "== config NOPE (0)\n", "no definitions, references or reads")
}

func TestSiblingsConfigAddressPicksTheKeyOverASymbol(t *testing.T) {
	c := tempWorkspace(t, map[string]string{
		"go.mod":      "module x/m\n",
		"a.go":        "package m\n\n// Port is a port.\nfunc Port() int { return 1 }\n\n// Run runs.\nfunc Run() {}\n",
		"config.yaml": "Port: 8080\nHost: example\n",
	})
	r, err := query.Siblings(c, "config:Port")
	text := run(t, r, err)
	mustContain(t, text, "== top-level config keys besides Port (1)\nHost  defines:1")
	r, err = query.Siblings(c, "Port")
	if text = run(t, r, err); strings.Contains(text, "config keys") || !strings.Contains(text, "Run") {
		t.Fatalf("a bare name that a symbol has stays a symbol:\n%s", text)
	}
	if _, err := query.Siblings(c, "config:Nope"); !errors.Is(err, query.ErrNotFound) {
		t.Fatalf("unknown key: %v", err)
	}
}
