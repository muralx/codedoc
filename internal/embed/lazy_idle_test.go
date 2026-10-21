package embed

import (
	"sync/atomic"
	"testing"
	"time"
)

// counted is a Fake that counts its closes and can hold a call open.
type counted struct {
	*Fake
	closes *atomic.Int32
	hold   chan struct{}
}

func (c counted) Embed(text string) ([]float32, error) {
	if c.hold != nil {
		<-c.hold
	}
	return c.Fake.Embed(text)
}

func (c counted) Close() error { c.closes.Add(1); return nil }

func idleLazy(idle time.Duration, hold chan struct{}) (*Lazy, *atomic.Int32, *atomic.Int32) {
	opens, closes := new(atomic.Int32), new(atomic.Int32)
	l := NewLazy(8, func() (Embedder, error) {
		opens.Add(1)
		return counted{Fake: NewFake(8), closes: closes, hold: hold}, nil
	}, nil).UnloadAfter(idle)
	return l, opens, closes
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestLazyUnloadsWhenIdleAndReloadsOnUse(t *testing.T) {
	l, opens, closes := idleLazy(20*time.Millisecond, nil)
	if _, err := l.Embed("a"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "unload", func() bool { return !l.Loaded() })
	if closes.Load() != 1 {
		t.Fatalf("closes %d", closes.Load())
	}
	if _, err := l.EmbedBatch([]string{"b"}); err != nil || opens.Load() != 2 || !l.Loaded() {
		t.Fatalf("reload: err=%v opens=%d", err, opens.Load())
	}
	if err := l.Close(); err != nil || closes.Load() != 2 || l.Loaded() {
		t.Fatalf("close: err=%v closes=%d", err, closes.Load())
	}
}

// EmbedBatch of counted never blocks (the embedded Fake calls its own
// Embed); Embed blocks until hold is closed.
func TestLazyKeepsTheModelWhileACallRuns(t *testing.T) {
	hold := make(chan struct{})
	l, _, closes := idleLazy(10*time.Millisecond, hold)
	if _, err := l.EmbedBatch([]string{"warm"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error)
	go func() { _, err := l.Embed("long"); done <- err }()
	waitFor(t, "the call to start", func() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.busy == 1 })
	time.Sleep(50 * time.Millisecond)
	if !l.Loaded() || closes.Load() != 0 {
		t.Fatalf("unloaded under a running call: closes=%d", closes.Load())
	}
	close(hold)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	waitFor(t, "unload after the call", func() bool { return !l.Loaded() })
}

func TestLazyWithoutIdleKeepsTheModel(t *testing.T) {
	opens, closes := new(atomic.Int32), new(atomic.Int32)
	l := NewLazy(8, func() (Embedder, error) {
		opens.Add(1)
		return counted{Fake: NewFake(8), closes: closes}, nil
	}, nil)
	if _, err := l.Embed("a"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if !l.Loaded() || closes.Load() != 0 || opens.Load() != 1 {
		t.Fatalf("loaded=%v closes=%d", l.Loaded(), closes.Load())
	}
}
