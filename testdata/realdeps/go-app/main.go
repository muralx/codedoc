// Command realgo is a fixture that depends on real third-party modules,
// downloaded into a module cache when the integration image is built.
package main

import (
	"fmt"
	"time"

	"github.com/jinzhu/now"
	"github.com/pkg/errors"
)

// calendar holds the clock the command asks about weeks.
type calendar struct {
	clock *now.Now
}

// monday returns the start of the week of at, or why it is unknown.
func (c *calendar) monday(at time.Time) (time.Time, error) {
	if c.clock == nil {
		return time.Time{}, errors.New("no clock")
	}
	return c.clock.With(at).BeginningOfWeek(), nil
}

func main() {
	c := &calendar{clock: now.New(time.Now())}
	fmt.Println(c.monday(time.Now()))
}
