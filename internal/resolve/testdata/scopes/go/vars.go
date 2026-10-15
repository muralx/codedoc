package adv

import "example.com/adv/kit"

// shared and typed are package variables used from main.go.
var shared = kit.Alpha{}

var (
	typed  *kit.Beta
	built  = kit.NewAlpha()
	opaque = make(chan int)
)
