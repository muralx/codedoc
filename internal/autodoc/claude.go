package autodoc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/muralx/codedoc/internal/docs/write"
	"github.com/muralx/codedoc/internal/session"
)

// The headless agent is confined at the claude command line, not by the
// prompt alone (spec 11.2):
//
//   - --tools makes only Bash and Read available at all, so Edit, Write,
//     MultiEdit and NotebookEdit do not exist in the session; they are also
//     listed in --disallowedTools in case a settings file or plugin brings
//     them back.
//   - --allowedTools pre-approves Read and Bash for `codedoc` commands only.
//   - --permission-mode dontAsk denies, without prompting, every call that
//     is not pre-approved, so any other Bash command is refused.
//
// The runner still verifies after every group that nothing outside the
// run's journal changed (see guard.go), since the confinement is only as
// good as the CLI's command matching.
const (
	// ClaudeBinEnv overrides the claude binary the runner invokes.
	ClaudeBinEnv = "CODEDOC_CLAUDE_BIN"
	// availableTools are the only built-in tools of the session.
	availableTools = "Bash,Read"
	// allowedTools limits the headless agent to codedoc commands and reads.
	allowedTools = "Bash(codedoc:*),Read"
	// disallowedTools are the editing tools, denied outright.
	disallowedTools = "Edit,Write,MultiEdit,NotebookEdit"
	// permissionMode denies whatever allowedTools does not approve.
	permissionMode = "dontAsk"
	// waitDelay bounds waiting for output after a cancelled run is killed.
	waitDelay = 5 * time.Second
)

// Envelope is the JSON object `claude -p --output-format json` prints.
type Envelope struct {
	IsError bool `json:"is_error"`
	// Subtype says how the session ended: success, error_max_turns,
	// error_max_budget_usd, error_during_execution, ...
	Subtype string   `json:"subtype"`
	Cost    float64  `json:"total_cost_usd"`
	Result  string   `json:"result"`
	Errors  []string `json:"errors"`
	// reported is set once claude printed the envelope.
	reported bool
}

// failure describes an envelope that reports an error.
func (e Envelope) failure() string {
	msg := strings.TrimSpace(e.Result)
	if len(e.Errors) > 0 {
		msg = strings.TrimSpace(strings.Join(append([]string{msg}, e.Errors...), "; "))
	}
	return fmt.Sprintf("claude reported an error (subtype %s): %s", orUnknown(e.Subtype), strings.TrimPrefix(msg, "; "))
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// claudeBin returns bin, else $CODEDOC_CLAUDE_BIN, else "claude".
func claudeBin(bin string) string {
	if bin != "" {
		return bin
	}
	if env := os.Getenv(ClaudeBinEnv); env != "" {
		return env
	}
	return "claude"
}

// maxTurns scales the agent's turn limit with the keys of a group:
// 10 + 8 per key, at most 60.
func maxTurns(keys int) int { return min(10+8*keys, 60) }

// claudeArgs returns the command-line arguments documenting keys keys
// with prompt, spending at most maxCost USD when it is positive, on
// model when one is named. The prompt directly follows -p: the
// tool-list options take several values.
func claudeArgs(prompt string, keys int, maxCost float64, model string) []string {
	args := []string{"-p", prompt, "--output-format", "json",
		"--tools", availableTools, "--allowedTools", allowedTools, "--disallowedTools", disallowedTools,
		"--permission-mode", permissionMode, "--max-turns", strconv.Itoa(maxTurns(keys))}
	if maxCost > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(maxCost, 'f', 2, 64))
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	return args
}

// invocation is one claude run of a group.
type invocation struct {
	bin, root, id, prompt string
	model                 string
	keys                  int
	maxCost               float64
}

// invoke runs claude headless in the root under the run id and parses
// its envelope. claude runs in its own process group, killed as a whole
// when ctx ends (and, on Linux, killed when codedoc dies), with the
// directory of the running codedoc first on PATH so the agent's codedoc
// commands run this very binary. A process that
// fails or prints no envelope is an error; an envelope with is_error set
// is returned as is, with its cost.
func invoke(ctx context.Context, in invocation) (Envelope, error) {
	cmd := exec.CommandContext(ctx, in.bin, claudeArgs(in.prompt, in.keys, in.maxCost, in.model)...)
	cmd.Dir = in.root
	cmd.WaitDelay = waitDelay
	cmd.SysProcAttr = procAttr()
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.Env = append(os.Environ(), "PATH="+selfPath(), session.HeadlessEnv+"=1", write.RunIDEnv+"="+in.id)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	env, parseErr := parseEnvelope(stdout.Bytes())
	if runErr != nil {
		return env, fmt.Errorf("autodoc: %s: %w: %s", in.bin, runErr, strings.TrimSpace(stderr.String()))
	}
	return env, parseErr
}

// selfPath returns $PATH with the running executable's directory first.
func selfPath() string {
	path := os.Getenv("PATH")
	exe, err := os.Executable()
	if err != nil {
		return path
	}
	if path == "" {
		return filepath.Dir(exe)
	}
	return filepath.Dir(exe) + string(os.PathListSeparator) + path
}

// parseEnvelope reads the last JSON object claude printed.
func parseEnvelope(out []byte) (Envelope, error) {
	out = bytes.TrimSpace(out)
	if i := bytes.LastIndexByte(out, '\n'); i >= 0 && !json.Valid(out) {
		out = out[i+1:]
	}
	var env Envelope
	if err := json.Unmarshal(out, &env); err != nil {
		return Envelope{}, fmt.Errorf("autodoc: unreadable claude output: %w", err)
	}
	env.reported = true
	return env, nil
}
