package daemon

import (
	"context"
	"io"
	"testing"
)

func TestPendingCountsQueuedAndInFlightChanges(t *testing.T) {
	root := newRepo(t, "a.go", "b.go")
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	c := start(t, []RepoSpec{{Workspace: "w", Name: "r", Root: root}}, io.Discard, func(ctx context.Context, _ Change) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
	})
	if n := c.w.Pending("w", "r"); n != 0 {
		t.Fatalf("pending at start: %d", n)
	}
	if n := c.w.Pending("w", "nope"); n != 0 {
		t.Fatalf("unknown repo: %d", n)
	}
	write(t, root, "a.go", "edited\n")
	eventually(t, "a queued path", func() bool { return c.w.Pending("w", "r") >= 1 })
	receive(t, entered, waitLimit, "delivery")
	if n := c.w.Pending("w", "r"); n != 1 {
		t.Fatalf("pending while delivering: %d", n)
	}
	write(t, root, "b.go", "edited\n")
	eventually(t, "a queued path behind the delivery", func() bool { return c.w.Pending("w", "r") == 2 })
	release <- struct{}{}
	receive(t, entered, waitLimit, "second delivery")
	close(release)
	eventually(t, "an empty queue", func() bool { return c.w.Pending("w", "r") == 0 })
}
