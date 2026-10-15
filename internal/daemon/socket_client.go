package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

const (
	// dialTimeout bounds connecting to the socket.
	dialTimeout = 200 * time.Millisecond
	// replyTimeout bounds one request and its reply.
	replyTimeout = 2 * time.Second
)

// QueryStatus asks the daemon listening at path for its status. It
// returns an error wrapping ErrNoDaemon when nobody listens there.
func QueryStatus(path string) (Status, error) {
	var st Status
	err := query(path, request{Op: "status"}, &st)
	return st, err
}

// QueryFresh asks the daemon listening at path whether the repo of q is
// fresh. It returns an error wrapping ErrNoDaemon when nobody listens
// there.
func QueryFresh(path string, q FreshQuery) (FreshReply, error) {
	var r FreshReply
	err := query(path, request{Op: "fresh", FreshQuery: q}, &r)
	return r, err
}

// query sends req and decodes the one-line reply into out.
func query(path string, req request, out any) error {
	c, err := net.DialTimeout("unix", path, dialTimeout)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNoDaemon, err)
	}
	defer func() { _ = c.Close() }()
	if err := c.SetDeadline(time.Now().Add(replyTimeout)); err != nil {
		return err
	}
	if err := json.NewEncoder(c).Encode(req); err != nil {
		return err
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("daemon: reading reply: %w", err)
	}
	var e errorReply
	if err := json.Unmarshal(line, &e); err != nil {
		return fmt.Errorf("daemon: malformed reply: %w", err)
	}
	if e.Error != "" {
		return errors.New("daemon: " + e.Error)
	}
	return json.Unmarshal(line, out)
}
