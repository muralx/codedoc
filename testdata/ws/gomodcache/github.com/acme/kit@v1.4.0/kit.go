package kit

import "errors"

// DefaultTimeout is the timeout of a client, in seconds.
const DefaultTimeout = 30

// ErrClosed is returned by a client that was closed.
var ErrClosed = errors.New("kit: closed")

// Client talks to an Acme service.
type Client struct {
	// Name identifies the client in logs.
	Name string
	conn *conn
	Base
}

// Base carries what every kind of client shares.
type Base struct{}

// Close releases the client.
func (Base) Close() error { return nil }

type conn struct{ Addr string }

// New returns a client with a name.
func New(name string) *Client { return &Client{Name: name} }

// Do sends one request and waits for its answer.
func (c *Client) Do() error { return c.dial() }

func (c *Client) dial() error { return nil }

// Doer is anything that can send a request.
type Doer interface {
	// Do sends one request.
	Do() error
}

func helper() {}
