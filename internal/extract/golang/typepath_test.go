package golang_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/extract"
)

// typePathsSrc reaches variables through declared field types, method
// results and loop elements, and declares types inside a function.
const typePathsSrc = `package p

import "example.com/kit"

func (s *Server) run(st *kit.Store, opts Options, byID map[string]*kit.Item, grid [4]Cell, ch chan Item) {
	for _, it := range st.Items {
		it.Touch()
	}
	for _, v := range byID {
		v.Touch()
	}
	for _, c := range grid {
		c.Fill()
	}
	for i, it := range (st.All()) {
		i.None()
		it.Touch()
	}
	for v := range ch {
		v.None()
	}
	r, err := st.Get("x")
	r.Touch()
	err.None()
	first := st.First
	first.Touch()
	own := s.opts.Store
	own.Get("y")
	got, _ := s.opts.Store.Get("z")
	got.Touch()
	h := s.pick()
	h.Touch()
	deep := st.All()[0].Owner()
	deep.None()
	lit := []*kit.Item{}
	for _, l := range lit {
		l.Touch()
	}
	fn := opts.hook
	x := fn()
	x.None()
	p := opts.Pool.Pick()
	p.Do()
}

func local() {
	type row struct{ n Item }
	type alias = kit.Item
	var one row
	one.n.Touch()
	rows := []row{}
	for _, r := range rows {
		r.n.Touch()
	}
	var a alias
	a.Touch()
}

type row struct{}

func after(r row) { r.Touch() }
`

func TestTypePathsAreRecordedFromDeclaredTypes(t *testing.T) {
	res := extractSrc(t, "p.go", typePathsSrc)
	checkRecvTypes(t, res, map[string]string{
		"Touch@7": "kit.Store#Items@", "Touch@10": "kit.Item", "Fill@13": "Cell", "None@16": "?",
		"Touch@17": "kit.Store#All()@", "None@20": "?", "Touch@23": "kit.Store#Get()", "None@24": "?",
		"Touch@26": "kit.Store#First", "Get@28": "$s#opts#Store", "Touch@30": "$s#opts#Store#Get()",
		"Touch@32": "s.pick()", "None@34": "?", "Touch@37": "kit.Item", "None@41": "?", "Do@43": "Options#Pool#Pick()",
		"Touch@50": extract.BuiltinType, "Touch@53": extract.BuiltinType, "Touch@56": extract.BuiltinType,
		"Touch@61": "row",
	})
}
