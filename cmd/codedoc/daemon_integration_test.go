//go:build integration

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestIntegrationDaemonProcess builds codedoc, runs `codedoc daemon` as a
// subprocess, edits a file, waits until `codedoc find` reports the new
// symbol, then stops the daemon with SIGTERM and expects exit status 0.
func TestIntegrationDaemonProcess(t *testing.T) {
	_, goLib := setupWorkspace(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	bin := filepath.Join(t.TempDir(), "codedoc")
	if out, err := exec.Command("go", "build", "-tags", "sqlite_fts5", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	d, log, exited := startProcess(t, bin, "daemon")
	run := func(args ...string) string {
		out, _ := exec.Command(bin, args...).CombinedOutput()
		return string(out)
	}
	waitFor(t, "the daemon running", func() bool {
		return strings.Contains(run("status", "--workspace", "fx"), "daemon: running")
	})
	src := "package store\n\n// FromDaemon is new.\nfunc FromDaemon() {}\n"
	if err := os.WriteFile(filepath.Join(goLib, "store", "daemon.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the new symbol", func() bool {
		return strings.Contains(run("find", "FromDaemon", "--workspace", "fx"), "store/daemon.go")
	})
	if err := d.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("daemon exit: %v\n%s", err, log.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("daemon did not stop\n%s", log.String())
	}
	if !strings.Contains(log.String(), "daemon: stopped") {
		t.Fatal(log.String())
	}
}

// startProcess starts bin with args, collecting its output, and returns
// it with a channel that receives its exit status. It is killed when the
// test ends.
func startProcess(t *testing.T, bin string, args ...string) (*exec.Cmd, *syncBuffer, <-chan error) {
	t.Helper()
	log := &syncBuffer{}
	d := exec.Command(bin, args...)
	d.Stdout, d.Stderr = log, log
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- d.Wait() }()
	t.Cleanup(func() { _ = d.Process.Kill() })
	return d, log, exited
}

// syncBuffer is an output sink safe for concurrent use.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
