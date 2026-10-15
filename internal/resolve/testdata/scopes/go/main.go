// Package adv holds adversarial probes of local variable scoping: each
// call carries the edge it must yield, as the name called, the tier and
// the targets.
package adv

import "example.com/adv/kit"

// 1 nested block shadow
func blockShadow(ok bool) {
	x := kit.NewAlpha()
	if ok {
		x := &kit.Beta{}
		x.Run() // => Run import Beta.Run
	}
	x.Run() // => Run import Alpha.Run
}

// 2 closure param does not leak
func closureLeak() {
	x := kit.NewAlpha()
	f := func(x *kit.Beta) { x.Run() } // => Run import Beta.Run
	f(nil)
	x.Close() // => Close import Alpha.Close
}

// 3 multi-assign from call
func multi() {
	a, b := kit.Pair()
	a.Run() // => Run import Alpha.Run
	b.Run() // => Run ambiguous Alpha.Run|Beta.Run
	c, err := kit.NewBeta()
	_ = err
	c.Run() // => Run import Beta.Run
}

// 4 local var named like the package; method call result
func pkgShadow(m *kit.Maker) {
	kit := m
	y := kit.NewAlpha() // => NewAlpha import Maker.NewAlpha
	y.Run()             // => Run import Beta.Run
}

// 5 if-init scope
func ifInit(ok bool) {
	x := kit.NewAlpha()
	if x, err := kit.NewBeta(); err == nil {
		x.Run() // => Run import Beta.Run
	}
	x.Run() // => Run import Alpha.Run
}

// 6 method value
func methodValue() {
	x := kit.NewAlpha()
	run := x.Run
	run()
	g := kit.NewAlpha
	z := g()
	z.Run() // => Run ambiguous Alpha.Run|Beta.Run
}

// 7 range var hides
func rng(bs []*kit.Beta) {
	x := kit.NewAlpha()
	for _, x := range bs {
		x.Run() // => Run import Beta.Run
	}
	x.Run() // => Run import Alpha.Run
}

// 8 plain assignment keeps type
func assign() {
	var x *kit.Alpha
	x = kit.NewAlpha()
	x.Run() // => Run import Alpha.Run
}

// 9 switch cases
func sw(n int) {
	switch n {
	case 1:
		x := &kit.Alpha{}
		x.Run() // => Run import Alpha.Run
	case 2:
		x := &kit.Beta{}
		x.Run() // => Run import Beta.Run
	}
}

// 10 goroutine closure reads outer var declared before; inner redeclare after
func late() {
	x := kit.NewAlpha()
	go func() {
		x.Run() // => Run import Alpha.Run
		x := &kit.Beta{}
		x.Run() // => Run import Beta.Run
	}()
	x.Close() // => Close import Alpha.Close
}

// 11 a variable of unknown type never yields a confident guess
func blind(items func() []any) {
	for _, it := range items() {
		it.Only() // => Only ambiguous Maker.Only
	}
}

// 12 package variables, declared in another file
func packageVars() {
	shared.Run()  // => Run import Alpha.Run
	typed.Close() // => Close import Beta.Close
	built.Run()   // => Run import Alpha.Run
	opaque.Only() // => Only unique Maker.Only
	shared := &kit.Beta{}
	shared.Run() // => Run import Beta.Run
}
