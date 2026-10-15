// Package kit declares the types the scope probes call through.
package kit

type Alpha struct{}

func (a *Alpha) Run()   {}
func (a *Alpha) Close() {}

type Beta struct{}

func (b *Beta) Run()   {}
func (b *Beta) Close() {}

func NewAlpha() *Alpha        { return &Alpha{} }
func NewBeta() (*Beta, error) { return &Beta{}, nil }
func Pair() (*Alpha, *Beta)   { return nil, nil }

type Maker struct{}

func (m *Maker) NewAlpha() *Beta { return &Beta{} }

// Only is a method no other type declares: the unique tier finds it.
func (m *Maker) Only() {}
