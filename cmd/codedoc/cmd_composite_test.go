package main

import (
	"regexp"
	"strings"
	"testing"
)

// omission matches the line that ends a cut section: how many are left
// and the command that prints them.
var omission = regexp.MustCompile(`(?m)^\s*\d+ more[^:\n]*: codedoc (.+)$`)

// words splits a command an answer printed into its arguments, as a
// shell would: the addresses of the fixture hold no spaces, so dropping
// the quotes is enough.
func words(command string) []string {
	return strings.Fields(strings.ReplaceAll(command, "'", ""))
}

func TestImpactCommand(t *testing.T) {
	e2eWorkspace(t)
	out := cli(t, "impact", "MemStore.Get", "--workspace", "fx")
	if !strings.Contains(out, "MemStore.Get  store/store.go:26-32  func (m *MemStore) Get(key string) (string, error)  Get implements Store.\n"+
		"== callers (1)\nTestMemStorePutGet  store/store_test.go:10  if v, err := m.Get(\"k\"); err != nil || v != \"v\" {  [test]\n"+
		"== implements or overrides (1)\nStore.Get  store/store.go:12  Get(key string) (string, error)\n== tests (1)\n") ||
		!strings.HasSuffix(out, "exported  yes (public member of MemStore)  no other repo of the workspace uses it\n") {
		t.Fatalf("got\n%s", out)
	}
	// Every command an impact answer names runs as printed, in the same
	// workspace, and continues the section it ends.
	out = cli(t, "impact", "go-lib:store/store.go#Store", "--workspace", "fx", "--budget", "500")
	cmds := omission.FindAllStringSubmatch(out, -1)
	if len(out) > 500 || len(cmds) == 0 {
		t.Fatalf("%d bytes, %d commands:\n%s", len(out), len(cmds), out)
	}
	for _, m := range cmds {
		if !strings.Contains(m[1], " --workspace fx") {
			t.Errorf("%q does not name the workspace", m[0])
		}
		if next := cli(t, words(m[1])...); !strings.Contains(next, "== ") {
			t.Errorf("%q answers:\n%s", m[1], next)
		}
	}
}

func TestImpactCommandFlags(t *testing.T) {
	e2eWorkspace(t)
	// The default budget is the one of a summary, and it is a flag.
	help := cli(t, "impact", "--help")
	if !strings.Contains(help, "output budget in bytes (0 means unlimited) (default 3000)") || !strings.Contains(help, "--only string") {
		t.Fatalf("help:\n%s", help)
	}
	if out, err := runCLI(t, "impact", "MemStore.Get", "--workspace", "fx", "--offset", "2"); err == nil ||
		!strings.Contains(err.Error(), "--offset pages one section: name it with --only") {
		t.Fatalf("%v\n%s", err, out)
	}
	out := cli(t, "impact", "MemStore.Get", "--workspace", "fx", "--only", "tests", "--format", "json")
	if !strings.Contains(out, `"title":"tests"`) || strings.Contains(out, `"title":"callers"`) {
		t.Fatalf("json:\n%s", out)
	}
}

func TestHowCommand(t *testing.T) {
	e2eWorkspace(t)
	out := cli(t, "how", "Service.Lookup", "--workspace", "fx")
	if !strings.Contains(out, "== explain Service.Lookup (5)\n") || !strings.Contains(out, "== top callers (1)\n") ||
		strings.Contains(out, "top callees") ||
		!strings.Contains(out, "== flow from Service.Lookup (3)\nService.Lookup  go-lib:service/service.go:17-23\n  Store.Get  ") {
		t.Fatalf("got\n%s", out)
	}
	out = cli(t, "how", "go-app:main.go", "--workspace", "fx")
	if !strings.Contains(out, "== explain go-app:main.go (3)\n") || !strings.Contains(out, "== flow from main (5)\n") {
		t.Fatalf("got\n%s", out)
	}
	// The command a cut tree names prints that tree.
	out = cli(t, "how", "go-app:main.go", "--workspace", "fx", "--budget", "700")
	m := omission.FindStringSubmatch(out[strings.Index(out, "== flow from main"):])
	if m == nil || !strings.HasPrefix(m[1], "flow 'go-app:main.go#main' --depth 2 --workspace fx --offset ") {
		t.Fatalf("got\n%s", out)
	}
	if next := cli(t, words(m[1])...); !strings.Contains(next, "== flow from main (5)\n") {
		t.Fatalf("%q answers:\n%s", m[1], next)
	}
}

func TestAPICommand(t *testing.T) {
	depsWorkspace(t, "")
	cli(t, "index", "--workspace", "fx")
	out := cli(t, "api", "@acme/sdk", "--workspace", "fx")
	if !strings.Contains(out, "/node_modules/@acme/sdk  Client and server building blocks for the Acme protocol.\n") ||
		!strings.Contains(out, "== @acme/sdk/server (1)\nServer  class\n") || !strings.Contains(out, "== modules of @acme/sdk (6)\n") {
		t.Fatalf("got\n%s", out)
	}
	out = cli(t, "api", "@acme/sdk@1.2.0", "Server", "--workspace", "fx", "--doc")
	if !strings.Contains(out, "== members of Server (6)\ndist/server/index.d.ts\n  constructor  :10  (name: string)\n"+
		"  connect  :12  (transport: Transport): Promise<Result>  Attaches the server to a transport.\n") ||
		!strings.Contains(out, "dist/shared/protocol.d.ts\n  Protocol.notification  :9  (notification: Notification): Promise<void>  ") {
		t.Fatalf("got\n%s", out)
	}
	// A package that is declared but not indexed is one line, not an
	// error; one no manifest declares is not found.
	if out := cli(t, "api", "lint-tool", "--workspace", "fx"); !strings.HasSuffix(out, "lint-tool@9.4.0 is declared in package.json but no "+
		"source file imports it, so it is not indexed; name it under deps.include in the codedoc config and run codedoc deps index\n") {
		t.Fatalf("got\n%s", out)
	}
	for _, c := range [][]string{
		{"api", "nothing", "codedoc: package nothing: not found: no manifest of the workspace declares it; codedoc deps lists the packages\n"},
		{"api", "lint-tol", "codedoc: package lint-tol: not found: no manifest of the workspace declares it; nearest: lint-tool\n"},
		{"api", "@acme/sdk", "Zzyzxq", "codedoc: Zzyzxq in @acme/sdk: not found: it exports no name like that; " +
			"codedoc api @acme/sdk lists what it exports\n"},
		{"siblings", "config:no.such.key", "codedoc: config key no.such.key: not found: no config file of the workspace defines it; " +
			"codedoc config '*' lists the keys\n"},
	} {
		args, want := c[:len(c)-1], c[len(c)-1]
		if stdout, stderr, code := exitOf(t, append(args, "--workspace", "fx")...); code != exitFailed || stdout != "" || stderr != want {
			t.Fatalf("%v: exit %d, stdout %q, stderr %q", args, code, stdout, stderr)
		}
	}
	if _, err := runCLI(t, "api", "--workspace", "fx"); err == nil {
		t.Fatal("api without a package accepted")
	}
}

func TestSymbolCommandTakesSeveralAddresses(t *testing.T) {
	e2eWorkspace(t)
	out := cli(t, "symbol", "MemStore.Get", "Nope", "MemStore.Put", "--workspace", "fx")
	if !strings.Contains(out, "MemStore.Get  store/store.go:26-32\n25\t// Get implements Store.\n") ||
		!strings.Contains(out, "\nNope  not found\nMemStore.Put  store/store.go:35-38\n34\t// Put implements Store.\n") {
		t.Fatalf("got\n%s", out)
	}
	// Under a budget the small one is whole and the large one is cut and
	// continued with the exact lines left.
	out = cli(t, "symbol", "py-lib:src/pylib/store.py#MemStore", "MemStore.Get", "--workspace", "fx", "--budget", "600")
	if len(out) > 600 || !strings.Contains(out, "MemStore.Get  go-lib:store/store.go:26-32\n25\t// Get implements Store.\n") ||
		!strings.Contains(out, "MemStore  py-lib:src/pylib/store.py:25-44\n25\tclass MemStore(Store):\n") ||
		!strings.Contains(out, " more lines: codedoc lines py-lib:src/pylib/store.py ") {
		t.Fatalf("%d bytes:\n%s", len(out), out)
	}
	if out, err := runCLI(t, "symbol", "Nope", "Never", "--workspace", "fx"); err == nil ||
		!strings.Contains(out, "Nope  not found\nNever  not found\n") {
		t.Fatalf("%v\n%s", err, out)
	}
}
