package resolve_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/deps"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/resolve"
	"github.com/muralx/codedoc/internal/store"
	"github.com/muralx/codedoc/internal/testutil"
)

// libPackage is an npm package with two entry points, a class that
// extends another and a function an entry point re-exports.
var libPackage = map[string]string{
	"node_modules/lib/package.json": `{"name": "lib", "version": "1.0.0",
  "exports": {".": {"types": "./index.d.ts"}, "./extra": {"types": "./extra.d.ts"}}}`,
	"node_modules/lib/index.d.ts": `export { deep } from "./inner.js";
export declare class Base { static make(): Base; ping(): void; }
export declare class Widget extends Base { size: number; grow(by: number): void; }
export declare class Frame { widget: Widget; label: string; }
export declare function frame(): Frame;
export declare function helper(x: number): number;
`,
	"node_modules/lib/inner.d.ts": "export declare function deep(): void;\n",
	"node_modules/lib/extra.d.ts": "export declare function extra(): void;\nexport declare function twin(): void;\n",
	"node_modules/lib/twin.d.ts":  "export declare function twin(): void;\n",
}

const depApp = `import { Widget, helper } from "lib";
import * as ns from "lib";
import def from "lib/extra";
import { extra, deep, twin } from "lib/extra";
import { gone } from "nolib";
import { readFile } from "node:fs";

export class Holder {
  private w: Widget = new Widget();
  private n: number = 1;
  private loose = 2;
  run(): void {
    this.w.grow(1);
    this.w.ping();
    this.w.size.toFixed();
    this.n.toFixed();
    this.loose.toFixed();
    this.missing.grow(1);
  }
}

export function use(): void {
  helper(1);
  Widget.make();
  ns.helper(2);
  ns.Widget.make();
  def();
  extra();
  deep();
  twin();
  gone();
  readFile("x");
  helper(3).grow();
}
`

// depWS indexes one repo of files and syncs its dependencies against a
// deps.db next to the workspace database.
func depWS(t *testing.T, files map[string]string) (*store.Store, *config.Workspace, deps.Options) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ws := &config.Workspace{Name: "w", Repos: []*config.Repo{{Name: "app", Path: testutil.SourceRepo(t, "app", files)}}}
	if _, err := index.Repo(s, ws.Repos[0], index.Options{Workspace: ws.Name}); err != nil {
		t.Fatal(err)
	}
	opts := deps.Options{DB: filepath.Join(dir, "deps.db"), Full: true}
	if _, err := deps.Sync(context.Background(), s, ws, ws.Repos, opts); err != nil {
		t.Fatal(err)
	}
	if err := index.ResolvePending(s, index.Options{Workspace: ws.Name}); err != nil {
		t.Fatal(err)
	}
	return s, ws, opts
}

// depEdges returns "source -> dependency key" of every edge of the
// workspace that ends in a dependency, sorted.
func depEdges(t *testing.T, s *store.Store, path string) []string {
	t.Helper()
	repos, err := s.Repos()
	if err != nil || len(repos) != 1 {
		t.Fatalf("repos: %v %v", repos, err)
	}
	f, err := s.FileByPath(repos[0].ID, path)
	if err != nil {
		t.Fatal(err)
	}
	syms, err := s.SymbolsInFile(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, sy := range syms {
		edges, err := s.EdgesFrom(sy.ID, resolve.EdgeCalls, resolve.EdgeReferences)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range edges {
			if e.DepKey != "" {
				out = append(out, sy.Name+" -> "+e.DepKey[strings.Index(e.DepKey, ":")+1:]+" ["+e.Confidence+"]")
			}
		}
	}
	sort.Strings(out)
	return out
}

func sameLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\n got\n  %s\n want\n  %s", what, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func tsFiles() map[string]string {
	files := map[string]string{
		"package.json": `{"name": "app", "version": "0.0.0", "dependencies": {"lib": "^1.0.0", "nolib": "^1.0.0"}}`,
		"src/a.ts":     depApp,
	}
	for p, body := range libPackage {
		files[p] = body
	}
	return files
}

func TestTypeScriptReferencesLandInDependencies(t *testing.T) {
	s, _, _ := depWS(t, tsFiles())
	sameLines(t, "edges of src/a.ts", depEdges(t, s, "src/a.ts"), []string{
		"run -> ts:lib:index.d.ts/Base.ping [import]",
		"run -> ts:lib:index.d.ts/Widget.grow [import]",
		"use -> ts:lib/extra:extra.d.ts/extra [import]",
		"use -> ts:lib/extra:extra.d.ts/twin [import]",
		"use -> ts:lib:index.d.ts/Base.make [import]",
		"use -> ts:lib:index.d.ts/helper [import]",
		"use -> ts:lib:index.d.ts/helper [import]",
		"use -> ts:lib:index.d.ts/helper [import]",
		"use -> ts:lib:inner.d.ts/deep [import]",
		"w -> ts:lib:index.d.ts/Widget [import]",
		"w -> ts:lib:index.d.ts/Widget [import]",
	})
}

// kitModule is a Go module in a module cache: a client that embeds a
// base type, a pool whose fields and method lead to the client, and a
// second package.
func kitModule(t *testing.T) {
	t.Helper()
	cache := t.TempDir()
	t.Setenv(deps.ModCacheEnv, cache)
	for p, body := range map[string]string{
		"go.mod": "module example.com/kit\n\ngo 1.22\n",
		"kit.go": "package kit\n\n// Client talks.\ntype Client struct {\n\tBase\n\tName string\n}\n\n// Base is shared.\ntype Base struct{}\n\n// Close ends it.\nfunc (Base) Close() error { return nil }\n\n// Do sends.\nfunc (c *Client) Do() error { return nil }\n\n// New builds.\nfunc New() *Client { return &Client{} }\n",
		"pool.go": "package kit\n\n// Pool holds clients.\ntype Pool struct {\n\tMain *Client\n\tAll  []*Client\n}\n\n" +
			"// Pick returns one.\nfunc (p *Pool) Pick() *Client { return p.Main }\n",
		"sub/sub.go": "package sub\n\n// Run runs.\nfunc Run() {}\n",
	} {
		full := filepath.Join(cache, "example.com/kit@v1.0.0", p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const depGoApp = `package main

import (
	"fmt"

	"example.com/kit"
	"example.com/kit/sub"
	"example.com/other"
)

type app struct {
	c    *kit.Client
	o    *other.Thing
	name string
}

func (a *app) run() {
	_ = a.c.Do()
	_ = a.c.Close()
	a.c.Nope()
	a.c.Name.Len()
	a.o.Do()
	a.zzz.Do()
}

func main() {
	a := &app{c: kit.New()}
	a.run()
	sub.Run()
	kit.Run()
	kit.Missing()
	fmt.Println(a.name)
}
`

func TestGoReferencesLandInDependencies(t *testing.T) {
	kitModule(t)
	s, _, _ := depWS(t, map[string]string{
		"go.mod":  "module example.com/app\n\ngo 1.22\n\nrequire example.com/kit v1.0.0\n",
		"main.go": depGoApp,
	})
	sameLines(t, "edges of main.go", depEdges(t, s, "main.go"), []string{
		"c -> go:example.com/kit:Client [import]",
		"main -> go:example.com/kit/sub:Run [import]",
		"main -> go:example.com/kit:New [import]",
		"run -> go:example.com/kit:Base.Close [import]",
		"run -> go:example.com/kit:Client.Do [import]",
		"run -> go:example.com/kit:Client.Name [import]",
	})
}

// Without the packages there is nothing to land in: deps.db gone, a
// package pruned from it, or no recorded deps.db at all.
func TestDependencyEdgesNeedThePackages(t *testing.T) {
	s, _, opts := depWS(t, tsFiles())
	all := func() error { return resolve.ReferencesThen(s, nil, nil) }
	db, err := deps.Open(opts.DB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Prune(deps.PruneOptions{All: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil || all() != nil {
		t.Fatal(err)
	}
	sameLines(t, "pruned packages", depEdges(t, s, "src/a.ts"), nil)
	if err := os.Remove(opts.DB); err != nil || all() != nil {
		t.Fatal(err)
	}
	sameLines(t, "no deps.db", depEdges(t, s, "src/a.ts"), nil)
	if err := s.Batch(func(b *store.Batch) error { return b.DeleteMeta(store.DepsDBKey) }); err != nil || all() != nil {
		t.Fatal(err)
	}
	sameLines(t, "no recorded deps.db", depEdges(t, s, "src/a.ts"), nil)
}
