package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/docs/write"
	"github.com/muralx/codedoc/internal/testutil"
)

// stubEnv, when set, makes the test binary act as the claude an upkeep
// run invokes (see stubClaude) instead of running tests. stubRecordEnv
// names a file it appends one line per invocation to, and stubGateEnv a
// file mode "wait" waits for.
const (
	stubEnv       = "SERVICE_STUB"
	stubRecordEnv = "SERVICE_STUB_RECORD"
	stubGateEnv   = "SERVICE_STUB_GATE"
)

// TestMain keeps the tests out of the user's real directories (see
// testutil.IsolateEnv).
func TestMain(m *testing.M) {
	if mode := os.Getenv(stubEnv); mode != "" {
		os.Exit(stubClaude(mode, os.Args[1:]))
	}
	os.Exit(testutil.MainIsolated(m))
}

// stubClaude simulates `claude -p` for Go symbols: it writes
// "<Name> does things." above every symbol the prompt lists as
// "- <key> (<state>, <path>:<line>)", the way `codedoc doc set` would
// (write.Apply, journaled under CODEDOC_RUN_ID), and prints a result
// envelope costing $0.25. Mode "wait" then stays until the gate file
// exists, so a test can change the repo while the run is going.
func stubClaude(mode string, args []string) int {
	prompt := ""
	for i, a := range args {
		if a == "-p" && i+1 < len(args) {
			prompt = args[i+1]
		}
	}
	for _, line := range strings.Split(prompt, "\n") {
		if err := documentLine(line); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	if f, err := os.OpenFile(os.Getenv(stubRecordEnv), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		_, _ = f.WriteString(mode + " " + os.Getenv("CODEDOC_HEADLESS") + "\n")
		_ = f.Close()
	}
	for mode == "wait" {
		if _, err := os.Stat(os.Getenv(stubGateEnv)); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Println(`{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.25,"result":"stub"}`)
	return 0
}

// documentLine documents the Go symbol a prompt line names, if it
// names one, in the working directory.
func documentLine(line string) error {
	key, rest, ok := strings.Cut(strings.TrimPrefix(line, "- "), " (")
	if !ok || !strings.HasPrefix(line, "- ") || !strings.Contains(key, ":go:") || strings.Contains(key, " ") {
		return nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	_, loc, _ := strings.Cut(strings.TrimSuffix(rest, ")"), ", ")
	path, _, _ := strings.Cut(loc, ":")
	name := key[strings.LastIndex(key, ":")+1:]
	_, err = write.Apply(write.Target{Root: wd, Path: filepath.ToSlash(path), Lang: "go", Qualified: name}, name+" does things.")
	return err
}
