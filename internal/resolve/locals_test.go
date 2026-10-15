package resolve_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/resolve"
	"github.com/muralx/codedoc/internal/store"
)

// callsOf returns "source -> target [confidence]" of every call edge
// leaving the symbols of a file, sorted: the target is the qualified
// name of a workspace symbol, the key (without its package id) of a
// dependency symbol, or the candidates of an ambiguous edge joined by
// '|'.
func callsOf(t *testing.T, s *store.Store, path string) []string {
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
		edges, err := s.EdgesFrom(sy.ID, resolve.EdgeCalls)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range edges {
			out = append(out, sy.Name+" -> "+callTarget(t, s, e)+" ["+e.Confidence+"]")
		}
	}
	sort.Strings(out)
	return out
}

func callTarget(t *testing.T, s *store.Store, e store.Edge) string {
	t.Helper()
	if e.DepKey != "" {
		return e.DepKey[strings.Index(e.DepKey, ":")+1:]
	}
	ids := e.Candidates
	if e.Dst != 0 {
		ids = []int64{e.Dst}
	}
	var names []string
	for _, id := range ids {
		sy, err := s.SymbolByID(id)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, sy.Qualified)
	}
	sort.Strings(names)
	return strings.Join(names, "|")
}

const localsTS = `import { Widget } from "lib";
import * as ns from "lib";
import { Local } from "./local";
import * as loc from "./local";
import Default from "lib/extra";

class Here {
  go(): void {}
  get(): void {}
}

type Alias = Here;
type Wide = Alias<string>;
type Shape = { go(): void };

export function locals(p: Widget, q: ns.Widget, r: Local): void {
  const w = new Widget();
  w.grow(1);
  w.ping();
  p.grow(2);
  q.grow(3);
  r.work();
  const l = new Local();
  l.work();
  l.inherited();
  const h = new Here();
  h.go();
  const m = new Map();
  m.get();
  const loose = make();
  loose.go();
}

export function more(a: Wide, n: loc.Local, d: Default, deep: loc.inner.Local, s: Shape, u: ns.Missing): void {
  a.go();
  n.work();
  d.work();
  deep.work();
  s.get();
  u.grow(5);
  n.missing.work();
}

export class Keeper {
  built = new Widget();
  constructor() {
    this.late = new Local();
  }
  run(): void {
    this.built.grow(4);
    this.late.work();
  }
}
`

const localTS = `export class Parent {
  inherited(): void {}
}
export class Local extends Parent {
  work(): void {}
  get(): void {}
}
`

func TestTypeScriptCallsThroughLocalVariables(t *testing.T) {
	files := tsFiles()
	files["src/locals.ts"], files["src/local.ts"] = localsTS, localTS
	s, _, _ := depWS(t, files)
	sameLines(t, "calls of src/locals.ts", callsOf(t, s, "src/locals.ts"), []string{
		"locals -> src/local.ts/Local.work [import]",
		"locals -> src/local.ts/Local.work [import]",
		"locals -> src/local.ts/Parent.inherited [unique]",
		"locals -> src/locals.ts/Here.go [local]",
		"locals -> src/locals.ts/Here.go|src/locals.ts/Shape.go [ambiguous]",
		"locals -> ts:lib:index.d.ts/Base.ping [import]",
		"locals -> ts:lib:index.d.ts/Widget.grow [import]",
		"locals -> ts:lib:index.d.ts/Widget.grow [import]",
		"locals -> ts:lib:index.d.ts/Widget.grow [import]",
		"more -> src/local.ts/Local.get|src/locals.ts/Here.get [ambiguous]",
		"more -> src/local.ts/Local.work [ambiguous]",
		"more -> src/local.ts/Local.work [import]",
		"more -> src/local.ts/Local.work [unique]",
		"more -> src/locals.ts/Here.go [local]",
		"run -> src/local.ts/Local.work [import]",
		"run -> ts:lib:index.d.ts/Widget.grow [import]",
	})
}

const localsGo = `package main

import (
	"strings"

	"example.com/app/inner"
	"example.com/kit"
	"example.com/kit/sub"
	"example.com/nowhere"
)

type local struct{}

func (local) Do() error          { return nil }
func (local) WriteString(string) {}

func build() (*local, error) { return &local{}, nil }

func far() kit.Client { return kit.Client{} }

func name() string { return "" }

func things() []local { return nil }

var hook = build

func use(c *kit.Client) {
	_ = c.Do()
	k := kit.New()
	_ = k.Close()
	var v kit.Client
	_ = v.Do()
	f := far()
	_ = f.Do()
	l := local{}
	_ = l.Do()
	b, _ := build()
	_ = b.Do()
	p := &local{}
	_ = p.Do()
	n := new(local)
	_ = n.Do()
	var sb strings.Builder
	sb.WriteString("x")
	k = nil
	c.Nope()
}

func more(t inner.Thing) {
	t.Run()
	in := inner.New()
	in.Run()
	in.Do()
	s := name()
	s.Do()
	ts := things()
	ts.Do()
	h, _ := hook()
	h.Do()
	x := sub.Run()
	x.Do()
	y := kit.Absent()
	y.Do()
	z := nowhere.New()
	z.Do()
	w := inner.Text()
	w.Do()
	c := sub.Client{}
	c.Do()
}
`

func TestGoCallsThroughLocalVariables(t *testing.T) {
	kitModule(t)
	s, _, _ := depWS(t, map[string]string{
		"go.mod":  "module example.com/app\n\ngo 1.22\n\nrequire example.com/kit v1.0.0\n",
		"main.go": localsGo,
		"inner/inner.go": "package inner\n\ntype Thing struct{}\n\nfunc (Thing) Run() {}\n\n" +
			"func New() *Thing { return nil }\n\nfunc Text() string { return \"\" }\n",
	})
	sameLines(t, "calls of main.go", callsOf(t, s, "main.go"), []string{
		"more -> New [import]",
		"more -> Text [import]",
		"more -> Thing.Run [import]",
		"more -> Thing.Run [import]",
		"more -> go:example.com/kit/sub:Run [import]",
		"more -> local.Do [ambiguous]",
		"more -> local.Do [ambiguous]",
		"more -> local.Do [unique]",
		"more -> name [local]",
		"more -> things [local]",
		"use -> build [local]",
		"use -> far [local]",
		"use -> go:example.com/kit:Base.Close [import]",
		"use -> go:example.com/kit:Client.Do [import]",
		"use -> go:example.com/kit:Client.Do [import]",
		"use -> go:example.com/kit:Client.Do [import]",
		"use -> go:example.com/kit:New [import]",
		"use -> local.Do [local]",
		"use -> local.Do [local]",
		"use -> local.Do [local]",
		"use -> local.Do [local]",
	})
}
