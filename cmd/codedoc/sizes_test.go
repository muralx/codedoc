package main

import (
	"fmt"
	"strings"
	"testing"
)

// sizedAnswers are the answers whose byte sizes testdata/sizes.txt
// fixes: one of each shape an agent reads, on the fixture workspace.
var sizedAnswers = [][]string{
	{"symbol", "MemStore.Get"},
	{"symbol", "py-lib:src/pylib/store.py#MemStore", "--budget", "500"},
	{"symbol", "MemStore.Get", "MemStore.Put", "NewMemStore"},
	{"outline", "go-lib:store/store.go"},
	{"find", "*Store*"},
	{"callers", "MemStore.Get"},
	{"callers", "MemStore.Get", "-C", "2"},
	{"refs", "go-lib:store/store.go#MemStore"},
	{"lines", "go-lib:store/store.go", "26-32"},
	{"ls", "go-lib:."},
	{"ls", "go-lib:.", "--long"},
	{"search", "persist values by key", "--limit", "5"},
	{"where", "persist values by key"},
	{"explain", "MemStore.Get"},
	{"impact", "MemStore.Get"},
	{"impact", "go-lib:store/store.go#Store.Get"},
	{"how", "Service.Lookup"},
	{"how", "go-app:main.go"},
	{"overview"},
	{"status"},
}

// sizedDepAnswers are the sized answers that need indexed dependencies.
var sizedDepAnswers = [][]string{
	{"find", "Server", "--deps"},
	{"callers", "Server.revision"},
	{"api", "@acme/sdk"},
	{"api", "@acme/sdk", "Server"},
}

// sizes runs each answer on workspace fx and returns one line per
// answer: the command and the bytes it printed, the directory of each
// of repos counted as its name (an answer about a dependency names the
// directory it is installed in, a temporary one here).
func sizes(t *testing.T, answers [][]string, repos map[string]string) string {
	t.Helper()
	var b strings.Builder
	for _, args := range answers {
		out, err := runCLI(t, append(args, "--workspace", "fx")...)
		if err != nil || out == "" {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		for name, dir := range repos {
			out = strings.ReplaceAll(out, dir, name)
		}
		fmt.Fprintf(&b, "codedoc %s -> %d\n", strings.Join(args, " "), len(out))
	}
	return b.String()
}

// TestAnswerSizes fixes the byte size of a representative set of answers
// (efficiency spec section 3), so that an answer that grows shows up as
// a diff of testdata/sizes.txt: rewrite it with -update and say in the
// commit why the bytes are worth it.
func TestAnswerSizes(t *testing.T) {
	e2eWorkspace(t)
	got := sizes(t, sizedAnswers, nil)
	repos := depsWorkspace(t, "")
	cli(t, "index", "--workspace", "fx")
	cliSnapshot(t, "sizes", got+sizes(t, sizedDepAnswers, repos))
}
