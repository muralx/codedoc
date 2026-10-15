// Package store declares the types the typed-receiver probes call
// through. Register and Touch are declared twice, so a receiver that is
// not typed leaves them ambiguous; Only and Get are declared once.
package store

type Item struct{ Name string }

func (i *Item) Touch() {}
func (i *Item) Only()  {}

type Store struct {
	Items []*Item
	ByID  map[string]Item
	First *Item
	Hook  func() *Item
}

func (s *Store) Get(id string) (*Item, error) { return nil, nil }
func (s *Store) All() []*Item                 { return nil }
func (s *Store) Register(name string)         {}

type Hub struct{}

func (h *Hub) Register(name string, force bool) {}
func (h *Hub) Touch()                           {}

var Default = &Store{}
