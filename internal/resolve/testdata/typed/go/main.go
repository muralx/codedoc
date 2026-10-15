// Probes of receivers typed through declared types: each call carries
// the edge it must yield, as the name called, the tier and the targets
// ("none -" for a call that must stay unresolved).
package main

import (
	"example.com/app/store"
	"example.com/kit"
)

type Base struct{ hub *store.Hub }

func (b *Base) Ping() {}

type Options struct {
	Client *store.Hub
	Store  *store.Store
	Pool   *kit.Pool
	Loose  interface{ Register(string) }
	Name   string
}

type Box[T any] struct{ item T }

type Server struct {
	Base
	*store.Store
	opts Options
	box  Box[store.Item]
}

var shared = &Server{}

// 1 a struct field, two hops
func create(options Options) {
	options.Client.Register("a", true) // => Register import Hub.Register
	options.Store.First.Touch()        // => Touch import Item.Touch
	options.Name.Touch()               // => Touch none -
}

// 2 the receiver, two hops, embedded fields as Go promotes them
func (s *Server) run() {
	s.opts.Client.Register("a", true) // => Register import Hub.Register
	s.opts.Store.First.Touch()        // => Touch import Item.Touch
	s.Ping()                          // => Ping local Base.Ping
	s.hub.Touch()                     // => Touch import Hub.Touch
	s.Get("x")                        // => Get import Store.Get
	s.Register("a")                   // => Register import Store.Register
	s.First.Touch()                   // => Touch import Item.Touch
	s.Base.Ping()                     // => Ping local Base.Ping
}

// 3 a hop through a type of a dependency
func dependency(options Options) {
	options.Pool.Main.Do() // => Do import kit:Client.Do
	p := options.Pool.Pick()
	p.Do() // => Do import kit:Client.Do
	for _, c := range options.Pool.All {
		c.Do() // => Do import kit:Client.Do
	}
}

// 4 a hop of unknown type keeps the unique tier
func unknown(options Options, s *Server) {
	options.Loose.Register("a") // => Register unique Store.Register
	options.Missing.Only()      // => Only unique Item.Only
	s.box.item.Only()           // => Only unique Item.Only
	options.Store.Hook.Only()   // => Only unique Item.Only
	h := options.Store.Hook
	h.Only() // => Only ambiguous Item.Only
}

// 5 the element type of a range
func ranges(st *store.Store, options Options) {
	for _, it := range st.Items {
		it.Touch() // => Touch import Item.Touch
	}
	for _, it := range st.ByID {
		it.Touch() // => Touch import Item.Touch
	}
	for _, it := range options.Store.All() {
		it.Touch() // => Touch import Item.Touch
	}
	items := st.All()
	for i, it := range items {
		_ = i
		it.Touch() // => Touch import Item.Touch
	}
	for it := range st.Items {
		it.Touch() // => Touch ambiguous Hub.Touch|Item.Touch
	}
}

// 6 the declared result of a method
func results(st *store.Store) {
	r, err := st.Get("x")
	_ = err
	r.Touch() // => Touch import Item.Touch
	first := st.First
	first.Touch() // => Touch import Item.Touch
}

func (s *Server) pick() *store.Hub { return nil }

func (s *Server) own() {
	it, _ := s.opts.Store.Get("x")
	it.Touch() // => Touch import Item.Touch
	h := s.pick()
	h.Touch() // => Touch import Hub.Touch
	o := shared.pick()
	o.Touch() // => Touch import Hub.Touch
	d, _ := store.Default.Get("x")
	d.Touch() // => Touch ambiguous Hub.Touch|Item.Touch
}

// 7 a call in the chain stands for its declared result
func chained(options Options, s *Server) {
	options.Store.All().Touch() // => Touch ambiguous Hub.Touch|Item.Touch
	s.pick().Touch()            // => Touch import Hub.Touch
	options.Pool.Pick().Do()    // => Do import kit:Client.Do
}
