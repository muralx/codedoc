package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

const fxProtocol = "npm/@acme/sdk@1.2.0:ts:@acme/sdk/shared/protocol.js:dist/shared/protocol.d.ts/Protocol."

// A name no workspace symbol has needs no --deps; one the workspace has
// lists the dependency's only with it (efficiency spec section 5).
func TestDependencyNamesNeedNoFlag(t *testing.T) {
	repos := depsWorkspace(t, "")
	cli(t, "index", "--workspace", "fx")
	t.Chdir(repos["deps-app"])
	for _, c := range []struct {
		args []string
		want []string
	}{
		{[]string{"find", "Server"}, []string{"== find Server (1)", "[dep @acme/sdk@1.2.0]"}},
		{[]string{"symbol", "Server", "--no-body"}, []string{"Server  ", "[dep @acme/sdk@1.2.0]"}},
		{[]string{"callers", "Server.connect"}, []string{"== callers of Server.connect [dep @acme/sdk@1.2.0] ("}},
		{[]string{"outline", "Server"}, []string{"== members of Server (4)"}},
		{[]string{"find", "revision", "--deps"}, []string{"== find revision (2)"}},
	} {
		out := cli(t, c.args...)
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%v: missing %q in\n%s", c.args, w, out)
			}
		}
	}
	if out := cli(t, "find", "revision"); !strings.Contains(out, "== find revision (1)") || strings.Contains(out, "[dep ") {
		t.Errorf("a workspace name without --deps:\n%s", out)
	}
	// An unused dependency says why its file is not in the index.
	_, stderr, code := exitOf(t, "outline", "node_modules/lint-tool/index.d.ts")
	if want := "codedoc: node_modules/lint-tool/index.d.ts is not indexed: no source file imports the dependency lint-tool; " +
		"name it under deps.include in the config to index it\n"; code != exitFailed || stderr != want {
		t.Errorf("exit %d: %s", code, stderr)
	}
}

func TestQueriesIncludeDependenciesWithTheFlag(t *testing.T) {
	repos := depsWorkspace(t, "")
	cli(t, "index", "--workspace", "fx")
	t.Chdir(repos["deps-app"])
	out := cli(t, "find", "Server", "--deps")
	if !strings.Contains(out, "export declare class Server extends Protocol") || !strings.Contains(out, "[dep @acme/sdk@1.2.0]") {
		t.Errorf("find --deps:\n%s", out)
	}
	out = cli(t, "symbol", "Server.notification", "--deps", "--body", "--keys", "--detail")
	for _, want := range []string{fxProtocol + "notification  " + repos["deps-app"] + "/node_modules/@acme/sdk/dist/shared/protocol.d.ts:9",
		"[declared on Protocol]", "9\t    notification(notification: Notification): Promise<void>"} {
		if !strings.Contains(out, want) {
			t.Errorf("symbol --deps --body misses %q:\n%s", want, out)
		}
	}
	hasLines(t, cli(t, "outline", "@acme/sdk"), "== npm/@acme/sdk@1.2.0 modules (6)",
		"@acme/sdk/server  server  ts npm-export, 4 symbols")
	if out := cli(t, "outline", "Server", "--deps"); !strings.Contains(out, "== inherited members (3)") {
		t.Errorf("outline of a dependency class:\n%s", out)
	}
	if out := cli(t, "explain", "Server.notification", "--deps"); !strings.Contains(out, "== top callers (1)") {
		t.Errorf("explain --deps:\n%s", out)
	}
	if out := cli(t, "search", "notification", "--deps"); !strings.Contains(out, "Protocol.notification  dist/shared/protocol.d.ts:9  ") {
		t.Errorf("search --deps:\n%s", out)
	}
}

func TestGraphQueriesReachDependencySymbols(t *testing.T) {
	repos := depsWorkspace(t, "")
	cli(t, "index", "--workspace", "fx")
	t.Chdir(repos["deps-app"])
	hasLines(t, cli(t, "callers", "Server.revision"),
		"== callers of Protocol.revision [dep @acme/sdk@1.2.0] (1)", "revision  src/main.ts:11  return Server.revision();",
		"Server.revision is declared on Protocol")
	hasLines(t, cli(t, "callers", "Server.revision", "--deps", "--keys"),
		"== callers of "+fxProtocol+"revision [dep @acme/sdk@1.2.0] (1)")
	out := cli(t, "callees", "App.announce")
	if !strings.Contains(out, "Protocol.notification  src/main.ts:20  await this.server.notification({ method: \"started\" });  [dep]") {
		t.Errorf("callees tag dependency targets:\n%s", out)
	}
	out = cli(t, "callees", "App.announce", "--keys", "--detail")
	if !strings.Contains(out, fxProtocol+"notification  src/main.ts:20") || !strings.Contains(out, "[import] [dep]") {
		t.Errorf("callees --keys --detail:\n%s", out)
	}
	if out := cli(t, "refs", "Server", "--kind", "type", "--detail"); !strings.Contains(out, "[type] [import]") {
		t.Errorf("refs of a dependency class:\n%s", out)
	}
	hasLines(t, cli(t, "overview", "--workspace", "fx", "--budget", "0"), "== dependencies (9)",
		"npm/@acme/sdk@1.2.0  deps-app indexed symbols:18", "npm/left-pad  deps-app missing")
	_, err := runCLIStdin(t, "A doc.\n", "doc", "set", fxProtocol+"close")
	if !errors.Is(err, query.ErrDependency) || exitCode(err, false) != 1 {
		t.Errorf("doc set refuses a dependency symbol: %v", err)
	}
}

// Indexing never waits for the vectors of dependencies: codedoc deps
// index --embed writes them, once, and codedoc deps says per package how
// many of its documented symbols and modules have one.
func TestDependencyVectorsAreEmbeddedOnRequestOnce(t *testing.T) {
	depsWorkspace(t, "")
	t.Setenv("CODEDOC_EMBEDDER", "fake")
	if out := cli(t, "index", "--workspace", "fx", "--verbose"); strings.Contains(out, "dep vectors") ||
		!strings.Contains(out, "vectors  +") {
		t.Errorf("index embeds the workspace only:\n%s", out)
	}
	hasLines(t, cli(t, "deps", "--workspace", "fx"),
		"npm/@acme/sdk@1.2.0  deps-app:package.json  indexed declared:^1.2.0 symbols:18 vectors:0/14")
	if out := cli(t, "deps", "index", "--workspace", "fx", "--verbose"); strings.Contains(out, "embedded") {
		t.Errorf("deps index without --embed embeds nothing:\n%s", out)
	}
	out := cli(t, "deps", "index", "--workspace", "fx", "--embed", "--verbose")
	if !strings.Contains(out, "embedded 41 vector(s)") {
		t.Errorf("deps index --embed embeds what is indexed:\n%s", out)
	}
	hasLines(t, out, "npm/@acme/sdk@1.2.0  deps-app:package.json  indexed declared:^1.2.0 symbols:18 vectors:14/14",
		"go/github.com/acme/kit@v1.4.0  deps-go:go.mod  indexed declared:v1.4.0 symbols:16 vectors:17/17")
	if out := cli(t, "deps", "index", "--workspace", "fx", "--embed", "--verbose"); strings.Contains(out, "embedded") {
		t.Errorf("a package version is embedded once:\n%s", out)
	}
	out = cli(t, "deps", "--workspace", "fx", "--format", "json")
	if !strings.Contains(out, `"vectors":{"embedded":14,"total":14}`) || !strings.Contains(out, `"dev":false`) {
		t.Errorf("the embedding state is in JSON:\n%s", out)
	}
}

func TestDepsPruneTakesBudgetAndOffset(t *testing.T) {
	depsWorkspace(t, "")
	cli(t, "index", "--workspace", "fx")
	out := cli(t, "deps", "prune", "--all", "--offset", "4", "--verbose")
	hasLines(t, out, "== pruned (6)")
	if strings.Count(out, "\n") != 3 {
		t.Errorf("--offset skips the first four:\n%s", out)
	}
	for _, flag := range []string{"--offset", "--budget"} {
		if _, err := runCLI(t, "deps", "prune", flag, "-1"); err == nil {
			t.Errorf("a negative %s must fail", flag)
		}
	}
}

// Asked to embed without an embedder, deps index says why in one line
// and fails: silence would read as done.
func TestDepsIndexEmbedFailsWithoutAnEmbedder(t *testing.T) {
	depsWorkspace(t, "")
	cli(t, "index", "--workspace", "fx")
	for _, c := range []struct{ env, dir, want string }{
		{"off", "", "codedoc: deps index --embed: nothing was embedded: embeddings are off (CODEDOC_EMBEDDER=off); " +
			"unset it and run the command again\n"},
		{"", "", "codedoc: deps index --embed: nothing was embedded: no embedding model is configured; " +
			"set embed.model_dir in the config (or CODEDOC_MODEL_DIR) to the directory make download-model fills\n"},
		{"", t.TempDir(), "nomic-embed-text-v1.5.onnx: no such file or directory; check embed.model_dir in the config (or CODEDOC_MODEL_DIR)\n"},
	} {
		t.Setenv("CODEDOC_EMBEDDER", c.env)
		t.Setenv("CODEDOC_MODEL_DIR", c.dir)
		stdout, stderr, code := exitOf(t, "deps", "index", "--workspace", "fx", "--embed")
		if code != exitFailed || stdout != "" || !strings.HasSuffix(stderr, c.want) || strings.Count(stderr, "\n") != 1 ||
			!strings.HasPrefix(stderr, "codedoc: deps index --embed: nothing was embedded: ") {
			t.Errorf("%q, %q: exit %d, stdout %q, stderr %q", c.env, c.dir, code, stdout, stderr)
		}
	}
	// Without --embed the same command succeeds in silence.
	if stdout, stderr, code := exitOf(t, "deps", "index", "--workspace", "fx"); code != 0 || stdout+stderr != "" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}
