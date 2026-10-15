package upkeep_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/docs/write"
	"github.com/muralx/codedoc/internal/testutil"
)

// stubEnv selects the behavior of the stub claude; when set, the test
// binary acts as claude instead of running tests. stubRecordEnv names a
// file it appends its prompt to.
const (
	stubEnv       = "UPKEEP_STUB"
	stubRecordEnv = "UPKEEP_STUB_RECORD"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(stubEnv); mode != "" {
		os.Exit(stubClaude(mode, os.Args[1:]))
	}
	os.Exit(testutil.MainIsolated(m))
}

// stubClaude simulates `claude -p` for Go symbols: it documents every
// key of the prompt the way `codedoc doc set` would (write.Apply,
// journaled under CODEDOC_RUN_ID) and prints a result envelope costing
// $0.25. Mode "idle" writes nothing; "edit" and "head" then do what a
// user working in the repo meanwhile would: create a file, or commit.
func stubClaude(mode string, args []string) int {
	prompt := ""
	for i, a := range args {
		if a == "-p" && i+1 < len(args) {
			prompt = args[i+1]
		}
	}
	if rec := os.Getenv(stubRecordEnv); rec != "" {
		f, _ := os.OpenFile(rec, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		_, _ = f.WriteString(strings.Join(args, " ") + "\n")
		_ = f.Close()
	}
	var err error
	if mode != "idle" {
		err = documentAll(prompt)
	}
	switch mode {
	case "edit":
		err = errors.Join(err, os.WriteFile("scratch.txt", []byte("mine\n"), 0o644))
	case "head":
		err = errors.Join(err, exec.Command("git", "commit", "-q", "--allow-empty", "-m", "user work").Run())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	out, _ := json.Marshal(map[string]any{"type": "result", "subtype": "success", "is_error": false,
		"total_cost_usd": 0.25, "result": "stub " + mode})
	fmt.Println(string(out))
	return 0
}

// documentAll writes "<Name> does things." above every symbol listed
// in the prompt as "- <key> (<state>, <path>:<line>)".
func documentAll(prompt string) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	for _, line := range strings.Split(prompt, "\n") {
		key, rest, ok := strings.Cut(strings.TrimPrefix(line, "- "), " (")
		if !ok || !strings.HasPrefix(line, "- ") || !strings.Contains(key, ":go:") || strings.Contains(key, " ") {
			continue
		}
		_, loc, _ := strings.Cut(strings.TrimSuffix(rest, ")"), ", ")
		path, _, _ := strings.Cut(loc, ":")
		name := key[strings.LastIndex(key, ":")+1:]
		t := write.Target{Root: wd, Path: filepath.ToSlash(path), Lang: "go", Qualified: name}
		if _, err := write.Apply(t, name+" does things."); err != nil {
			return err
		}
	}
	return nil
}
