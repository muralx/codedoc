package golang_test

import (
	"strconv"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
)

// recvTypes returns, by "name@line", the receiver type recorded on each
// qualified reference of res.
func recvTypes(res *extract.FileResult) map[string]string {
	out := map[string]string{}
	for _, r := range res.References {
		if r.Qualifier != "" && r.Kind == extract.RefCall {
			out[r.Name+"@"+strconv.Itoa(r.Line)] = r.RecvType
		}
	}
	return out
}

// checkRecvTypes fails for every call of want whose recorded receiver
// type differs.
func checkRecvTypes(t *testing.T, res *extract.FileResult, want map[string]string) {
	t.Helper()
	got := recvTypes(res)
	for k, w := range want {
		if g, ok := got[k]; !ok || g != w {
			t.Errorf("%s: receiver type %q (found %v), want %q", k, g, ok, w)
		}
	}
}

// localsSrc declares local variables every way the extractor reads a
// type from.
const localsSrc = `package p

import "example.com/kit"

func run(c *kit.Client, s Store, n int, names ...string) (out *Result, err error) {
	c.Do()
	s.Get()
	n.Bits()
	names.Len()
	out.Add()
	var a T
	var b *kit.Client
	var g, h List[int]
	var u = kit.New()
	a.A()
	b.B()
	g.G()
	h.H()
	u.U()
	d := T{}
	e := &kit.Client{}
	f := new(T)
	k := kit.New("x")
	l := build()
	m, err := kit.Open()
	o, q := T{}, &U{}
	d.D()
	e.E()
	f.F()
	k.K()
	l.L()
	m.M()
	err.Error()
	o.O()
	q.Q()
	d.inner.Deep()
}
`

func TestLocalVariableTypesAreRecorded(t *testing.T) {
	res := extractSrc(t, "p.go", localsSrc)
	want := map[string]string{
		"Do@6": "kit.Client", "Get@7": "Store", "Bits@8": "-", "Len@9": "?", "Add@10": "Result",
		"A@15": "T", "B@16": "kit.Client", "G@17": "List", "H@18": "List", "U@19": "kit.New()",
		"D@27": "T", "E@28": "kit.Client", "F@29": "T", "K@30": "kit.New()", "L@31": "build()", "M@32": "kit.Open()",
		"Error@33": "?", "O@34": "T", "Q@35": "U", "Deep@36": "T",
	}
	checkRecvTypes(t, res, want)
}

func TestLaterDeclarationShadowsALocalVariable(t *testing.T) {
	res := extractSrc(t, "p.go", `package p

type S struct{}

func (s *S) run(x T) {
	s.self()
	x.First()
	x := other()
	x.Second()
	x = T{}
	x.Third()
	for _, x := range items() {
		x.Fourth()
	}
	y := y.Of()
	x, z := 1, T{}
	x.Fifth()
	z.Sixth()
	func(x U) { x.Seventh() }(U{})
	switch z := any(x).(type) {
	case T:
		z.Eighth()
	}
}

func other(x T) { x.Other() }
`)
	want := map[string]string{
		"self@6": "", "First@7": "T", "Second@9": "other()", "Third@11": "other()", "Fourth@13": "items()@",
		"Of@15": "", "Fifth@17": "?", "Sixth@18": "T", "Seventh@19": "U", "Eighth@22": "?", "Other@26": "T",
	}
	checkRecvTypes(t, res, want)
}

// scopesSrc declares variables of one name in nested scopes.
const scopesSrc = `package p

import "example.com/kit"

var shared = kit.Client{}

const limit Size = 3

type hook func(shared *Other)

func scopes(ok bool, m *kit.Maker, bs []*kit.Beta, ch chan *kit.Beta) {
	x := kit.NewAlpha()
	if ok {
		x := &kit.Beta{}
		x.Block()
	}
	x.AfterBlock()
	f := func(x *kit.Beta) { x.Closure() }
	x.AfterClosure()
	if x, err := kit.NewBeta(); err == nil {
		x.IfInit()
	}
	x.AfterIf()
	for _, x := range bs {
		x.Range()
	}
	x.AfterRange()
	switch {
	case ok:
		x := &kit.Beta{}
		x.Case()
	default:
		x.Default()
	}
	go func() {
		x.Outer()
		x := &kit.Beta{}
		x.Inner()
	}()
	x.AfterGo()
	select {
	case x := <-ch:
		x.Received()
	}
	x.AfterSelect()
	kit := m
	y := kit.NewAlpha()
	y.FromMethod()
	g := f
	z := g()
	z.FromValue()
	a := x.(*kit.Beta)
	a.Asserted()
	shared.Top()
	limit.Const()
	later.Below()
}

var later Late

func other() { x.Nowhere() }
`

func TestVariablesLiveInLexicalScopes(t *testing.T) {
	res := extractSrc(t, "p.go", scopesSrc)
	checkRecvTypes(t, res, map[string]string{
		"Block@15": "kit.Beta", "AfterBlock@17": "kit.NewAlpha()", "Closure@18": "kit.Beta",
		"AfterClosure@19": "kit.NewAlpha()", "IfInit@21": "kit.NewBeta()", "AfterIf@23": "kit.NewAlpha()",
		"Range@25": "kit.Beta", "AfterRange@27": "kit.NewAlpha()", "Case@31": "kit.Beta", "Default@33": "kit.NewAlpha()",
		"Outer@36": "kit.NewAlpha()", "Inner@38": "kit.Beta", "AfterGo@40": "kit.NewAlpha()", "Received@43": "?",
		"AfterSelect@45": "kit.NewAlpha()", "NewAlpha@47": "kit.Maker", "FromMethod@48": "kit.Maker#NewAlpha()", "FromValue@51": "?",
		"Asserted@53": "kit.Beta", "Top@54": "kit.Client", "Const@55": "Size", "Below@56": "Late", "Nowhere@61": "",
	})
}
