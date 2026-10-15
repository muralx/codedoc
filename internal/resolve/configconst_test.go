package resolve_test

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/testutil"
)

// constFiles is a repo whose code reads config keys through constants in
// Go, Java, Python and TypeScript, locally and through imports.
var constFiles = map[string]string{
	"go.mod":     "module example.com/app\n",
	"cfg/cfg.go": "package cfg\n\n// HomeEnv names the home override.\nconst HomeEnv = \"APP_HOME\"\n",
	"main.go": `package main

import (
	"os"

	"example.com/app/cfg"
)

// BinEnv names the binary override.
const BinEnv = "APP_BIN"

func run(name string) {
	_ = os.Getenv(BinEnv)
	_ = os.Getenv(cfg.HomeEnv)
	_ = os.Getenv(name)
}
`,
	"pom.xml":                   "<project><groupId>p</groupId><artifactId>a</artifactId></project>\n",
	"src/main/java/p/Keys.java": "package p;\n\nclass Keys {\n    static final String PROP = \"JAVA_PROP\";\n}\n",
	"src/main/java/p/C.java": `package p;

class C {
    static final String HOME_ENV = "JAVA_HOME_KEY";

    String f() {
        return System.getenv(HOME_ENV) + System.getProperty(Keys.PROP);
    }
}
`,
	"pyproject.toml":    "[project]\nname = \"pyapp\"\n",
	"pyapp/__init__.py": "",
	"pyapp/keys.py":     "LEVEL_KEY = \"PY_LEVEL\"\n",
	"pyapp/run.py": `import os
from pyapp.keys import LEVEL_KEY

MODE_KEY = "PY_MODE"


def run():
    return os.environ.get(MODE_KEY) + os.environ[LEVEL_KEY]
`,
	"package.json": "{\"name\": \"tsapp\", \"version\": \"0.0.0\", \"type\": \"module\"}\n",
	"src/keys.ts":  "export const KEY = \"TS_KEY\";\n",
	"src/a.ts":     "import { KEY } from \"./keys\";\n\nexport function f() {\n  return process.env[KEY];\n}\n",
	".env":         "APP_HOME=/opt\nTS_KEY=x\n",
}

func constWS(t *testing.T) *ws {
	w := newWS(t)
	w.repos = append(w.repos, &config.Repo{Name: "app", Path: testutil.SourceRepo(t, "app", constFiles)})
	w.indexAll()
	return w
}

// undefinedReads describes the reads of key that no config file defines
// as "path:line".
func (w *ws) undefinedReads(key string) []string {
	w.t.Helper()
	reads, err := w.s.UnmatchedConfigReads(key, false, 0)
	if err != nil {
		w.t.Fatal(err)
	}
	var out []string
	for _, r := range reads {
		out = append(out, r.Path+":"+strconv.Itoa(r.Line))
	}
	return out
}

func TestConfigReadsThroughConstants(t *testing.T) {
	w := constWS(t)
	const main = "app:go:example.com/app:"
	w.want(w.uses("APP_HOME"), "defines app:.env:1 env /opt", "reads app:main.go:14 go "+main+"run")
	w.want(w.uses("TS_KEY"), "reads app:src/a.ts:4 typescript app:ts:tsapp:src/a.ts/f")
	for key, want := range map[string]string{
		"APP_BIN": "main.go:13", "JAVA_HOME_KEY": "src/main/java/p/C.java:7", "JAVA_PROP": "src/main/java/p/C.java:7",
		"PY_MODE": "pyapp/run.py:8", "PY_LEVEL": "pyapp/run.py:8",
	} {
		if got := w.undefinedReads(key); !slices.Equal(got, []string{want}) {
			t.Errorf("undefined reads of %s: %v, want [%s]", key, got, want)
		}
	}
	if got := w.undefinedReads("BinEnv"); len(got) != 0 {
		t.Errorf("a constant's name is no key: %v", got)
	}
	if got, err := w.s.UnmatchedConfigReads("PY_*", true, 0); err != nil || len(got) != 2 {
		t.Errorf("glob PY_*: %+v %v", got, err)
	}
	if _, unresolved, err := w.s.ConfigStats(w.repo("app")); err != nil || unresolved != 5 {
		t.Errorf("unresolved accessor reads %d, want 5 (%v)", unresolved, err)
	}
}

func TestConfigReadThroughConstantFollowsItsValueAndKey(t *testing.T) {
	w := constWS(t)
	app := w.repos[0]
	w.rewrite(app, filepath.Join(app.Path, "main.go"), func(old string) string {
		return strings.Replace(old, `"APP_BIN"`, `"APP_BIN2"`, 1)
	})
	if got := w.undefinedReads("APP_BIN"); len(got) != 0 {
		t.Errorf("the old value still read: %v", got)
	}
	w.want(w.undefinedReads("APP_BIN2"), "main.go:13")
	w.rewrite(app, filepath.Join(app.Path, ".env"), func(old string) string { return old + "APP_BIN2=b\n" })
	w.want(w.uses("APP_BIN2"), "reads app:main.go:13 go app:go:example.com/app:run")
	if got := w.undefinedReads("APP_BIN2"); len(got) != 0 {
		t.Errorf("a defined key is no undefined read: %v", got)
	}
	w.rewrite(app, filepath.Join(app.Path, "cfg/cfg.go"), func(old string) string {
		return strings.Replace(old, `"APP_HOME"`, `"APP_BIN2"`, 1)
	})
	w.want(w.uses("APP_BIN2"), "reads app:main.go:13 go app:go:example.com/app:run",
		"reads app:main.go:14 go app:go:example.com/app:run")
	w.rewrite(app, filepath.Join(app.Path, ".env"), func(old string) string {
		return strings.Replace(old, "APP_BIN2=b\n", "", 1)
	})
	w.want(w.undefinedReads("APP_BIN2"), "main.go:13", "main.go:14")
}
