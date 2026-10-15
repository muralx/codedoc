// Command app is the fixture entry point.
package main

import (
	"fmt"

	"github.com/fixture/lib/service"
	"github.com/fixture/lib/store"
)

func main() {
	svc := service.New(store.NewMemStore())
	fmt.Println(run(svc))
}

func run(svc *service.Service) string { return svc.Lookup("k") }
