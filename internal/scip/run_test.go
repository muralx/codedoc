package scip

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stub writes a fake indexer that records its working directory and
// arguments into the file named by its --output flag.
func stub(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "indexer")
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

const recordArgs = `out=""; prev=""
for a in "$@"; do [ "$prev" = "--output" ] && out="$a"; prev="$a"; done
echo "$(pwd) $*" > "$out"`

func runOne(t *testing.T, env, lang, root string) string {
	t.Helper()
	t.Setenv(env, stub(t, recordArgs))
	out := filepath.Join(t.TempDir(), "o.scip")
	if err := (Runner{}).RunLang(context.Background(), root, lang, out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(strings.TrimSpace(string(b)), out, "OUT")
}

func TestRunInvocations(t *testing.T) {
	root := t.TempDir()
	tsRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(tsRoot, "tsconfig.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ env, lang, root, args string }{
		{"CODEDOC_SCIP_GO", "go", root, "--output OUT"},
		{"CODEDOC_SCIP_TS", "typescript", root, "index --output OUT --infer-tsconfig"},
		{"CODEDOC_SCIP_TS", "ts", tsRoot, "index --output OUT"},
		{"CODEDOC_SCIP_PY", "python", root, "index . --project-name " + filepath.Base(root) + " --output OUT"},
		{"CODEDOC_SCIP_JAVA", "java", root, "index --output OUT"},
	}
	for _, c := range cases {
		want := c.root + " " + c.args
		if got := runOne(t, c.env, c.lang, c.root); got != want {
			t.Errorf("%s: ran %q, want %q", c.lang, got, want)
		}
	}
}

func TestRunMissingIndexerIsTyped(t *testing.T) {
	t.Setenv("CODEDOC_SCIP_PY", filepath.Join(t.TempDir(), "no-such-indexer"))
	err := (Runner{}).RunLang(context.Background(), t.TempDir(), "python", filepath.Join(t.TempDir(), "o.scip"))
	var missing *ErrIndexerMissing
	if !errors.As(err, &missing) || missing.Lang != "py" || !strings.Contains(missing.Error(), "no-such-indexer") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunFailures(t *testing.T) {
	ctx := context.Background()
	if err := (Runner{}).RunLang(ctx, t.TempDir(), "cobol", "x.scip"); err == nil {
		t.Fatal("unknown language: no error")
	}
	t.Setenv("CODEDOC_SCIP_GO", stub(t, "echo broken module >&2; exit 3"))
	err := (Runner{}).RunLang(ctx, t.TempDir(), "go", filepath.Join(t.TempDir(), "o.scip"))
	if err == nil || !strings.Contains(err.Error(), "broken module") {
		t.Fatalf("failing indexer: %v", err)
	}
	t.Setenv("CODEDOC_SCIP_GO", stub(t, "exec sleep 10"))
	start := time.Now()
	err = Runner{Timeout: 50 * time.Millisecond}.RunLang(ctx, t.TempDir(), "go", filepath.Join(t.TempDir(), "o.scip"))
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("timeout: %v after %v", err, time.Since(start))
	}
}

func TestTail(t *testing.T) {
	long := strings.Repeat("a", 3000) + "END"
	if got := tail([]byte(long)); len(got) != 2000 || !strings.HasSuffix(got, "END") {
		t.Fatalf("tail len %d", len(got))
	}
}
