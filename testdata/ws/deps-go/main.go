// Command depsgo is a fixture that depends on third-party modules.
package main

import "github.com/acme/kit"

// app holds the client the command talks through.
type app struct {
	client *kit.Client
}

// run sends one request and closes the client.
func (a *app) run() error {
	if err := a.client.Do(); err != nil {
		return err
	}
	return a.client.Close()
}

func main() {
	a := &app{client: kit.New("fixture")}
	_ = a.run()
}
