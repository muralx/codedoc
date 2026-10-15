package autodoc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/docs/write"
	"github.com/muralx/codedoc/internal/session"
	"github.com/muralx/codedoc/internal/testutil"
)

// stubEnv selects the stub claude behavior; when set, the test binary
// acts as claude instead of running tests.
const stubEnv = "AUTODOC_STUB"

// stubRecordEnv names a file the stub writes its arguments and
// environment to.
const stubRecordEnv = "AUTODOC_STUB_RECORD"

// invokeHelperEnv makes the test binary act as codedoc invoking the
// stub claude once, so a test can kill it midway.
const invokeHelperEnv = "AUTODOC_INVOKE_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(invokeHelperEnv) != "" {
		_ = os.Unsetenv(invokeHelperEnv)
		exe, _ := os.Executable()
		_, err := invoke(context.Background(), invocation{bin: exe, root: mustWd(), id: "autodoc-helper",
			prompt: "- r:go:a:A1 (missing, a/a.go:3)", keys: 1})
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if mode := os.Getenv(stubEnv); mode != "" {
		os.Exit(stubClaude(mode, os.Args[1:]))
	}
	os.Exit(testutil.MainIsolated(m))
}

// stubClaude simulates `claude -p`: it documents every key listed in the
// prompt the way `codedoc doc set` would (write.Apply, journaled under
// CODEDOC_RUN_ID) and prints a result envelope. Keys look like
// repo:go:<dir>:<Func>, the function living in <dir>/<dir>.go.
//
// Other modes add a side effect after documenting (see sideEffect), or
// crash, hang, or hang with a child process ("orphan").
func stubClaude(mode string, args []string) int {
	record(args)
	switch mode {
	case "crash":
		fmt.Fprintln(os.Stderr, "stub crashed")
		return 1
	case "hang":
		time.Sleep(time.Minute)
		return 1
	case "orphan":
		return orphan()
	case "stubborn":
		return stubborn(args)
	}
	if late, ok := strings.CutPrefix(mode, "late-"); ok {
		// behave until module b's group, the last one of testGaps
		if mode = "ok"; slices.Contains(promptKeys(args), "r:go:b:B1") {
			mode = late
		}
	}
	text := "%s does things."
	if strings.HasPrefix(mode, "broken") {
		text = "%s is BROKEN."
	}
	for _, k := range promptKeys(args) {
		if err := document(mustWd(), k, text); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	if err := sideEffect(mode); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	out, _ := json.Marshal(map[string]any{"type": "result", "subtype": "success", "is_error": mode == "error",
		"total_cost_usd": 0.25, "result": "stub " + mode})
	fmt.Println("some progress line")
	fmt.Println(string(out))
	return 0
}

// document writes text as the doc of key in root through write.Apply.
func document(root, key, text string) error {
	f := strings.SplitN(key, ":", 4)
	t := write.Target{Root: root, Path: f[2] + "/" + f[2] + ".go", Lang: "go", Qualified: f[3]}
	_, err := write.Apply(t, fmt.Sprintf(text, f[3]))
	return err
}

// sideEffect does what a misbehaving agent (or a user editing during
// the run) would do, per mode.
func sideEffect(mode string) error {
	wd := mustWd()
	switch mode {
	case "stray": // edit a tracked file directly and create a file
		return errors.Join(os.WriteFile(filepath.Join(wd, "go.mod"), []byte("module hacked\n"), 0o644),
			os.MkdirAll(filepath.Join(wd, "new"), 0o755),
			os.WriteFile(filepath.Join(wd, "new", "stray.txt"), []byte("x\n"), 0o644))
	case "dirty": // change a file the user had modified before the run
		return os.WriteFile(filepath.Join(wd, "notes.txt"), []byte("overwritten\n"), 0o644)
	case "outside": // document a symbol of another repo
		return document(os.Getenv(stubOutsideEnv), "o:go:b:B1", "%s does things.")
	case "drift": // document a key outside the group
		return document(wd, "r:go:b:B1", "%s does things.")
	case "head": // the user commits something else during the run
		return exec.Command("git", "commit", "-q", "--allow-empty", "-m", "user work").Run()
	case "commit-all": // the user commits everything, the run's writes included
		return errors.Join(os.WriteFile(filepath.Join(wd, "user.txt"), []byte("mine\n"), 0o644),
			exec.Command("git", "add", "-A").Run(), exec.Command("git", "commit", "-q", "-m", "user work").Run())
	case "broken-useredit": // the user edits a written file during the run
		p := filepath.Join(wd, "a", "a.go")
		data, err := os.ReadFile(p)
		return errors.Join(err, os.WriteFile(p, append(data, "// user\n"...), 0o644))
	}
	return nil
}

// stubOutsideEnv names the other repo the "outside" mode writes to.
const stubOutsideEnv = "AUTODOC_STUB_OUTSIDE"

// stubChildEnv names the file the "orphan" mode writes its child's pid
// to, and the "stubborn" mode its own.
const stubChildEnv = "AUTODOC_STUB_CHILD"

// orphan starts a hanging child, records its pid, and hangs.
func orphan() int {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), stubEnv+"=hang")
	if err := cmd.Start(); err != nil {
		return 1
	}
	_ = os.WriteFile(os.Getenv(stubChildEnv), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
	time.Sleep(time.Minute)
	return 1
}

// stubborn ignores SIGINT and SIGTERM, documents the prompt's keys,
// records its pid, and hangs.
func stubborn(args []string) int {
	signal.Ignore(syscall.SIGINT, syscall.SIGTERM)
	for _, k := range promptKeys(args) {
		if err := document(mustWd(), k, "%s does things."); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}
	_ = os.WriteFile(os.Getenv(stubChildEnv), []byte(strconv.Itoa(os.Getpid())), 0o600)
	time.Sleep(time.Minute)
	return 1
}

// promptKeys returns the "- key" lines of the -p argument.
func promptKeys(args []string) []string {
	var keys []string
	for i, a := range args {
		if a != "-p" || i+1 >= len(args) {
			continue
		}
		for _, l := range strings.Split(args[i+1], "\n") {
			k, ok := strings.CutPrefix(l, "- ")
			k, _, _ = strings.Cut(k, " (")
			if ok && strings.Count(k, ":") == 3 {
				keys = append(keys, k)
			}
		}
	}
	return keys
}

// record appends the stub's arguments, working directory and run
// environment to $AUTODOC_STUB_RECORD.
func record(args []string) {
	path := os.Getenv(stubRecordEnv)
	if path == "" {
		return
	}
	line, _ := json.Marshal(map[string]any{"args": args, "dir": mustWd(),
		"headless": os.Getenv(session.HeadlessEnv), "run": os.Getenv(write.RunIDEnv), "path": os.Getenv("PATH")})
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
}

func mustWd() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return wd
}
