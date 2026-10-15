//go:build integration

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/testutil"
)

// rpc writes one JSON-RPC message per line and reads the replies.
type rpc struct {
	t    *testing.T
	in   io.WriteCloser
	out  *bufio.Scanner
	cmd  *exec.Cmd
	next int
}

func (r *rpc) send(msg string) {
	r.t.Helper()
	if _, err := io.WriteString(r.in, msg+"\n"); err != nil {
		r.t.Fatal(err)
	}
}

// reply reads the next line, which must be a JSON-RPC message.
func (r *rpc) reply() map[string]any {
	r.t.Helper()
	if !r.out.Scan() {
		r.t.Fatalf("no reply: %v", r.out.Err())
	}
	var m map[string]any
	if err := json.Unmarshal(r.out.Bytes(), &m); err != nil {
		r.t.Fatalf("stdout line is not JSON-RPC: %q", r.out.Text())
	}
	return m
}

// call runs tools/call for a tool with JSON arguments and returns the
// text of its result; a tool error fails the test.
func (r *rpc) call(tool, args string) string {
	r.t.Helper()
	r.next++
	r.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, r.next, tool, args))
	var reply struct {
		Result struct {
			Content []struct{ Text string }
			IsError bool
		}
	}
	raw := stringify(r.reply())
	if err := json.Unmarshal([]byte(raw), &reply); err != nil || reply.Result.IsError || len(reply.Result.Content) != 1 {
		r.t.Fatalf("%s %s: %v: %s", tool, args, err, raw)
	}
	return reply.Result.Content[0].Text
}

// close ends the server by closing its stdin; it must exit cleanly.
func (r *rpc) close() {
	r.t.Helper()
	_ = r.in.Close()
	if err := r.cmd.Wait(); err != nil {
		r.t.Fatalf("codedoc mcp exited with %v", err)
	}
}

// mcpStdio builds codedoc, starts `codedoc mcp` in dir on its real stdin
// and stdout and performs the initialize handshake.
func mcpStdio(t *testing.T, dir string) *rpc {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "codedoc")
	if out, err := exec.Command("go", "build", "-tags", "sqlite_fts5", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cmd := exec.Command(bin, "mcp")
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r := &rpc{t: t, in: in, out: bufio.NewScanner(out), cmd: cmd, next: 1}
	r.out.Buffer(nil, 1<<20)
	r.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",` +
		`"capabilities":{},"clientInfo":{"name":"it","version":"0"}}}`)
	if init := r.reply(); !strings.Contains(stringify(init), `"name":"codedoc"`) {
		t.Fatalf("initialize: %v", init)
	}
	r.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	return r
}

// TestIntegrationMCPStdio builds codedoc and talks JSON-RPC to `codedoc
// mcp` over its real stdin and stdout: initialize, tools/list, tools/call.
func TestIntegrationMCPStdio(t *testing.T) {
	repos := e2eWorkspace(t)
	r := mcpStdio(t, repos["go-lib"])
	r.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if list := stringify(r.reply()); strings.Count(list, `"name":"codedoc_`) != 19 {
		t.Fatalf("tools/list: %s", list)
	}
	if call := r.call("codedoc_symbol", `{"symbol":"go-lib:store/store.go#MemStore.Get","no_body":true}`); !strings.Contains(call, "Get implements Store.") {
		t.Fatalf("tools/call: %s", call)
	}
	r.close()
}

// TestIntegrationMCPStdioDocuments asks the stdio server about
// documents: the outline of a Markdown file, a section by its
// file.md#section address, the file listing and a line range with the
// section the index places on it.
func TestIntegrationMCPStdioDocuments(t *testing.T) {
	repo := testutil.CopyFixture(t, "doc-repo")
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("workspaces:\n  docs:\n    repos: [{path: "+repo+"}]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEDOC_CONFIG", cfgPath)
	t.Setenv("CODEDOC_DATA", filepath.Join(t.TempDir(), "data"))
	cli(t, "index", "--workspace", "docs")
	r := mcpStdio(t, repo)
	// A fresh answer of a one-repo workspace has no header line: the
	// outline starts with its title.
	outline := r.call("codedoc_outline", `{"target":"docs/design.md"}`)
	hasAll(t, "codedoc_outline", outline, "\n15-35  ## 2. Model  21 lines\n", "\n  19-27  ### 2.1 Keys  9 lines\n")
	if !strings.HasPrefix(outline, "== docs/design.md, 63 lines (11)\n") {
		t.Errorf("codedoc_outline starts with\n%s", outline)
	}
	// The section is its location and its lines; detail adds the
	// heading, its kind and the section it is in.
	hasAll(t, "codedoc_symbol", r.call("codedoc_symbol", `{"symbol":"docs/design.md#2.1"}`),
		"2.1 Keys  docs/design.md:19-27\n19\t### 2.1 Keys\n", "\n21\tKeys are strings. The empty key is rejected.\n")
	hasAll(t, "codedoc_symbol with detail", r.call("codedoc_symbol", `{"symbol":"docs/design.md#2.1","detail":true}`),
		"2.1 Keys  docs/design.md:19-27  ### 2.1 Keys  [section] [in:2. Model]\n",
		"\n21\tKeys are strings. The empty key is rejected.\n")
	// The files of a directory are listed under its name.
	ls := r.call("codedoc_ls", `{"paths":["docs"],"sort":"name","long":true}`)
	hasAll(t, "codedoc_ls", ls, "== ls docs (5)\ndocs/\n  CHANGES.txt  ", "\n  design.md  ",
		"  63 lines  doc/markdown  11 sections  Store design\n", "  doc/asciidoc  5 sections  Operations guide\n",
		"\ndocs/runbooks/retries.md  ")
	changes, design := strings.Index(ls, "\n  CHANGES.txt  "), strings.Index(ls, "\n  design.md  ")
	if changes < 0 || design < 0 || changes > design {
		t.Errorf("codedoc_ls sorted by name:\n%s", ls)
	}
	// A range is one header line naming the section that encloses it or
	// that it runs into, then the lines: no title line.
	lines := r.call("codedoc_lines", `{"path":"docs/design.md","range":"14-18"}`)
	hasAll(t, "codedoc_lines", lines, "docs/design.md:14-18  in 2. Model (15-35)\n14\t\n15\t## 2. Model\n",
		"\n17\tA record has a key and a value.\n")
	if !strings.HasPrefix(lines, "docs/design.md:14-18  ") {
		t.Errorf("codedoc_lines starts with\n%s", lines)
	}
	hasAll(t, "codedoc_lines with A+N", r.call("codedoc_lines", `{"path":"doc-repo:docs/design.md","range":"19+2"}`),
		"docs/design.md:19-21  in 2. Model > 2.1 Keys (19-27)\n19\t### 2.1 Keys\n",
		"\n21\tKeys are strings. The empty key is rejected.\n")
	r.close()
}

func stringify(m map[string]any) string {
	b, _ := json.Marshal(m)
	return string(b)
}
