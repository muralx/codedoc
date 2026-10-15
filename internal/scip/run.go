// Package scip runs SCIP indexers on a repository and parses their output
// into plain occurrence and symbol records (spec 7.4), then merges them
// into the store as precise edges (Merge, MapDefinitions).
package scip

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/muralx/codedoc/internal/parse"
)

// DefaultTimeout bounds one indexer run.
const DefaultTimeout = 20 * time.Minute

// ErrIndexerMissing reports that the indexer binary for a language is not
// installed; callers record it as a non-fatal status.
type ErrIndexerMissing struct {
	// Lang is the language family whose indexer is missing.
	Lang string
	// Bin is the binary that was looked up.
	Bin string
}

// Error implements error.
func (e *ErrIndexerMissing) Error() string {
	return fmt.Sprintf("scip: %s indexer %q not found", e.Lang, e.Bin)
}

// indexer describes how to invoke one language's SCIP indexer.
type indexer struct {
	bin  string // default binary name
	env  string // environment variable overriding bin
	args func(root, out string) []string
}

var indexers = map[string]indexer{
	"go": {bin: "scip-go", env: "CODEDOC_SCIP_GO", args: func(_, out string) []string {
		return []string{"--output", out}
	}},
	"ts": {bin: "scip-typescript", env: "CODEDOC_SCIP_TS", args: tsArgs},
	"py": {bin: "scip-python", env: "CODEDOC_SCIP_PY", args: func(root, out string) []string {
		return []string{"index", ".", "--project-name", filepath.Base(root), "--output", out}
	}},
	"java": {bin: "scip-java", env: "CODEDOC_SCIP_JAVA", args: func(_, out string) []string {
		return []string{"index", "--output", out}
	}},
}

func tsArgs(root, out string) []string {
	args := []string{"index", "--output", out}
	if _, err := os.Stat(filepath.Join(root, "tsconfig.json")); err != nil {
		args = append(args, "--infer-tsconfig")
	}
	return args
}

// Runner runs SCIP indexers with a per-run timeout.
type Runner struct {
	// Timeout bounds each indexer run; zero means DefaultTimeout.
	Timeout time.Duration
}

// RunLang runs the indexer of one language ("go", "ts", "py", "java" or a
// precise name such as "typescript") in root, writing the index to the
// file out.
func (r Runner) RunLang(ctx context.Context, root, lang, out string) error {
	ix, ok := indexers[parse.Family(lang)]
	if !ok {
		return fmt.Errorf("scip: no indexer for language %q", lang)
	}
	lang = parse.Family(lang)
	bin := ix.bin
	if v := os.Getenv(ix.env); v != "" {
		bin = v
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return &ErrIndexerMissing{Lang: lang, Bin: bin}
	}
	absOut, err := filepath.Abs(out)
	if err != nil {
		return fmt.Errorf("scip: output path: %w", err)
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, ix.args(root, absOut)...)
	cmd.Dir = root
	if msg, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return fmt.Errorf("scip: %s indexer: %w: %s", lang, err, tail(msg))
	}
	return nil
}

// Families returns the language families with a SCIP indexer among the
// file languages langs (such as "go" or "tsx"), sorted and unique.
func Families(langs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range langs {
		fam := parse.Family(l)
		if _, ok := indexers[fam]; ok && !seen[fam] {
			seen[fam] = true
			out = append(out, fam)
		}
	}
	sort.Strings(out)
	return out
}

// tail returns the last part of an indexer's output for error messages.
func tail(b []byte) string {
	const limit = 2000
	if len(b) > limit {
		b = b[len(b)-limit:]
	}
	return string(b)
}
