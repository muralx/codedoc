// Package transport carries kit requests over HTTP.
package transport

// HTTP is the HTTP transport.
type HTTP struct {
	// BaseURL is where requests go.
	BaseURL string
}

// RoundTrip sends one request.
func (h *HTTP) RoundTrip() error { return nil }
