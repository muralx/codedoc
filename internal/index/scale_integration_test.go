//go:build integration

package index_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

// Scale budgets of a 2,000-file Python or Java tree (spec 16).
const (
	scaleFirstBudget = 15 * time.Second
	scaleNoopBudget  = 2 * time.Second
	scalePackages    = 40
	scaleModules     = 50
)

// pyTree writes 2,000 Python modules of five classes with eight methods
// each, calling their own methods through self, helpers imported by
// name and a class imported under an alias.
func pyTree(tb testing.TB, root string) {
	writeBench(tb, root, "pyproject.toml", "[project]\nname = \"bigpy\"\n")
	for p := 0; p < scalePackages; p++ {
		writeBench(tb, root, fmt.Sprintf("src/big/pkg%d/__init__.py", p), "")
		for m := 0; m < scaleModules; m++ {
			var b strings.Builder
			fmt.Fprintf(&b, "import os\nfrom big.pkg%d.mod%d import Cls%d_0 as Other, helper_%d_0\n\n",
				(p+1)%scalePackages, m, m, m)
			for c := 0; c < 5; c++ {
				fmt.Fprintf(&b, "class Cls%d_%d(object):\n    def __init__(self):\n        self.v = os.getenv('E')\n\n", m, c)
				for k := 0; k < 8; k++ {
					fmt.Fprintf(&b, "    def meth%d(self, a, b=1):\n        x = self.meth%d(a)\n"+
						"        y = helper_%d_0(a, b)\n        return Other().run(x, y)\n\n", k, (k+1)%8, m)
				}
			}
			fmt.Fprintf(&b, "def helper_%d_0(a, b=None):\n    return [a, b]\n", m)
			writeBench(tb, root, fmt.Sprintf("src/big/pkg%d/mod%d.py", p, m), b.String())
		}
	}
}

// javaTree writes 2,000 Java classes of 40 overloaded method pairs each,
// calling each other bare, through new and through a class import.
func javaTree(tb testing.TB, root string) {
	writeBench(tb, root, "pom.xml", "<project><groupId>g</groupId><artifactId>big</artifactId></project>\n")
	for p := 0; p < scalePackages; p++ {
		for m := 0; m < scaleModules; m++ {
			var b strings.Builder
			fmt.Fprintf(&b, "package com.big.p%d;\nimport com.big.p%d.C%d;\nimport java.util.*;\npublic class C%d implements Runnable {\n",
				p, (p+1)%scalePackages, m, m)
			b.WriteString("  private final Map<String, List<Integer>> data = new HashMap<>();\n")
			for k := 0; k < 40; k++ {
				fmt.Fprintf(&b, "  public int m%d(String a, int b) {\n    int x = m%d(a, b);\n    new C%d().run();\n"+
					"    return x;\n  }\n  public int m%d(int a) { return a; }\n", k, (k+1)%40, m, k)
			}
			b.WriteString("  public void run() {}\n}\n")
			writeBench(tb, root, fmt.Sprintf("src/main/java/com/big/p%d/C%d.java", p, m), b.String())
		}
	}
}

func TestScaleFirstAndNoopIndex(t *testing.T) {
	for name, gen := range map[string]func(testing.TB, string){"python": pyTree, "java": javaTree} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			gen(t, root)
			s, err := store.Open(filepath.Join(t.TempDir(), "ws.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			rep := &config.Repo{Name: "big", Path: root}
			opts := index.Options{Workspace: "w", RepoOrder: []string{"big"}}
			for run, budget := range []time.Duration{scaleFirstBudget, scaleNoopBudget} {
				start := time.Now()
				if _, err := index.Repo(s, rep, opts); err != nil {
					t.Fatal(err)
				}
				if err := index.ResolvePending(s, opts); err != nil {
					t.Fatal(err)
				}
				took := time.Since(start)
				t.Logf("run %d: %v", run, took)
				if took > budget && !raceEnabled {
					t.Errorf("run %d took %v, budget %v", run, took, budget)
				}
			}
		})
	}
}
