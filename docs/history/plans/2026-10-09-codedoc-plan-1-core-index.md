# codedoc Plan 1: Core Index (skeleton, Go and TypeScript extraction, resolution)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A working `codedoc` CLI that indexes a workspace of Go and TypeScript repos into SQLite and answers `outline`, `find`, `symbol`, `callers`, `callees`, `refs`, `impls`, `tests`, `siblings`, `deps`, `dependents` with fresh, budgeted output.

**Architecture:** Pure extractors turn file bytes into symbol, reference, import, and doc records using tree-sitter; an index pipeline stores them in SQLite keyed by repo and stable key; a resolver turns references into confidence-tagged edges; a query layer renders answers through one output frame that enforces a byte budget. Spec: `docs/history/specs/2026-10-09-codedoc-design.md` (sections 5 to 9, 13.1, 17 steps 1 to 3).

**Tech Stack:** Go 1.27 (CGO), `github.com/tree-sitter/go-tree-sitter` v0.25.0 with `tree-sitter-go` v0.25.0 and `tree-sitter-typescript` v0.23.2, `github.com/mattn/go-sqlite3` v1.14.52 (build tag `sqlite_fts5`), `github.com/spf13/cobra`, `gopkg.in/yaml.v3`, `github.com/cespare/xxhash/v2`, git CLI.

**Conventions for every task (from the repo's code-quality rules, see @enforcing-code-quality):**
- Test first. Run the failing test before writing the implementation.
- Files under 400 lines, functions under 40 lines. Split when a file grows past that.
- `gofmt`, `go vet ./...`, and `golangci-lint run` (config in Task 1) must pass before each commit.
- Every exported identifier has a doc comment.
- Commit after each task with the message given.
- Build and test with `make test` (which passes `-tags sqlite_fts5`); plain `go test ./...` fails on the FTS tables.

---

## File structure

```
cmd/codedoc/main.go                 cobra root; registers subcommands
cmd/codedoc/cmd_index.go            index, status
cmd/codedoc/cmd_query.go            outline, find, symbol, graph queries (one file per 3 to 4 commands as they grow)
internal/config/config.go           Config, Workspace, Repo types; Load; validation
internal/config/resolve.go          workspace resolution from cwd
internal/config/config_test.go
internal/store/store.go             Open, Close, migrations runner, transaction helper
internal/store/schema.go            schema SQL (versioned migrations slice)
internal/store/repos.go             repo and file rows
internal/store/symbols.go           symbol rows, stable-key lookup, name index
internal/store/edges.go             edge, edge_candidate, module_edge rows and graph queries
internal/store/modules.go           module rows, identity map
internal/store/store_test.go ...    one test file per source file
internal/gitmeta/git.go             branch, commit, dirty files, ls-files, last commit per file
internal/gitmeta/git_test.go
internal/output/frame.go            Response, Header, Section, Entry; budget enforcement
internal/output/text.go             compact text renderer
internal/output/json.go             JSON renderer
internal/output/frame_test.go
internal/parse/parser.go            Parser interface, Tree/Node wrappers, Close discipline
internal/parse/registry.go          language detection by extension, grammar registry
internal/parse/treesitter.go        CGO implementation
internal/parse/parse_test.go
internal/extract/types.go           Symbol, Reference, Import, Doc, FileResult records
internal/extract/extractor.go       Extractor interface, registry by language
internal/extract/golang/golang.go   Go extractor: symbols and docs
internal/extract/golang/refs.go     Go extractor: references and imports
internal/extract/golang/golang_test.go  golden tests
internal/extract/ts/ts.go           TypeScript/JavaScript extractor: symbols and docs
internal/extract/ts/refs.go         references and imports
internal/extract/ts/ts_test.go
internal/extract/golden/            golden JSON files per fixture source file
internal/index/discover.go          file discovery, exclusions, hashing
internal/index/pipeline.go          Index(repo): changed-file detection, extract, store, resolve
internal/index/stablekey.go         stable key construction and parsing
internal/index/freshness.go         query-time freshness check
internal/index/index_test.go
internal/resolve/identity.go        module identity map (go.mod, package.json)
internal/resolve/resolve.go         reference resolution tiers
internal/resolve/resolve_test.go
internal/query/outline.go, find.go, symbol.go, graph.go, siblings.go, deps.go
internal/query/addr.go              symbol address parsing (stable key, repo:path#name, bare name)
internal/query/query_test.go
testdata/ws/go-lib/                 fixture repo: module github.com/fixture/lib
testdata/ws/go-app/                 fixture repo: imports go-lib
testdata/ws/ts-domain/              fixture repo: npm package @fixture/domain
testdata/ws/ts-app/                 fixture repo: imports @fixture/domain
internal/testutil/fixture.go        CopyFixture(t, name): copies a fixture to a temp dir and gives it git history
Makefile, .golangci.yml, .gitignore, README.md
```

---

## Step 1: Skeleton

### Task 1: Module, build, lint, CLI root

**Files:**
- Create: `go.mod`, `Makefile`, `.golangci.yml`, `.gitignore`, `cmd/codedoc/main.go`, `cmd/codedoc/main_test.go`

- [ ] **Step 1: Initialise the module and dependencies**

```bash
cd ~/src/codedoc
go mod init github.com/muralx/codedoc
go get github.com/spf13/cobra@v1.10.2 gopkg.in/yaml.v3@v3.0.1 github.com/mattn/go-sqlite3@v1.14.52 \
  github.com/cespare/xxhash/v2@v2.3.0 github.com/bmatcuk/doublestar/v4@v4.10.2 github.com/tree-sitter/go-tree-sitter@v0.25.0 \
  github.com/tree-sitter/tree-sitter-go@v0.25.0 github.com/tree-sitter/tree-sitter-typescript@v0.23.2
```

- [ ] **Step 2: Write the Makefile**

```makefile
BINARY := codedoc
TAGS := sqlite_fts5

.PHONY: build test lint clean

build:
	CGO_ENABLED=1 go build -tags "$(TAGS)" -o $(BINARY) ./cmd/codedoc

test:
	CGO_ENABLED=1 go test -tags "$(TAGS)" -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

lint:
	gofmt -l . | tee /dev/stderr | test -z "$$(cat)"
	go vet -tags "$(TAGS)" ./...
	golangci-lint run --build-tags "$(TAGS)"

clean:
	rm -f $(BINARY) coverage.out
```

- [ ] **Step 3: Write `.golangci.yml`**

```yaml
version: "2"
linters:
  enable: [errcheck, govet, staticcheck, unused, revive, gocyclo, funlen]
  settings:
    funlen: {lines: 40, statements: 30}
    gocyclo: {min-complexity: 12}
run:
  build-tags: [sqlite_fts5]
```

- [ ] **Step 4: Write `.gitignore`**

```
/codedoc
coverage.out
*.db
```

- [ ] **Step 5: Write the failing CLI test**

`cmd/codedoc/main_test.go`:

```go
package main

import (
	"bytes"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	var out bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := out.String(); got != "codedoc dev\n" {
		t.Fatalf("got %q", got)
	}
}
```

- [ ] **Step 6: Run it, expect a compile failure (`newRootCmd` undefined)**

Run: `go test ./cmd/codedoc/`

- [ ] **Step 7: Write `cmd/codedoc/main.go`**

```go
// Command codedoc indexes a workspace of repositories and answers structural
// and documentation questions for coding agents.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "codedoc",
		Short:         "Code knowledge index for coding agents",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "codedoc %s\n", version)
			return err
		},
	})
	return root
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "codedoc:", err)
		os.Exit(exitCode(err))
	}
}
```

Add `exitCode` in `cmd/codedoc/errors.go`:

```go
package main

import "errors"

// ErrNotFound and ErrAmbiguous map to the exit codes in spec section 13.1.
var (
	ErrNotFound  = errors.New("not found")
	ErrAmbiguous = errors.New("ambiguous")
)

func exitCode(err error) int {
	switch {
	case errors.Is(err, ErrNotFound):
		return 2
	case errors.Is(err, ErrAmbiguous):
		return 3
	default:
		return 1
	}
}
```

- [ ] **Step 8: Run the test, expect PASS**

Run: `go test ./cmd/codedoc/`

- [ ] **Step 9: Write the fixture helper** `internal/testutil/fixture.go`

```go
// Package testutil holds helpers shared by tests.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// WorkspaceDir returns the absolute path of testdata/ws.
func WorkspaceDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "ws")
}

// CopyFixture copies testdata/ws/<name> to a temp directory, initialises a
// git repository there with one commit, and returns the path. Fixture sources
// stay plain files in the project tree; git history exists only in the copy.
func CopyFixture(t *testing.T, name string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), name)
	if err := os.CopyFS(dst, os.DirFS(filepath.Join(WorkspaceDir(), name))); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-qm", "fixture: initial"}} {
		cmd := exec.Command("git", append([]string{"-c", "user.email=f@x", "-c", "user.name=fixture"}, args...)...)
		cmd.Dir = dst
		// Fixed dates make the commit hash reproducible, so snapshot tests can
		// compare the response header exactly.
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dst
}
```

- [ ] **Step 10: Lint and commit** (the repository already exists with the design docs committed; no `git init` needed)

```bash
make lint && make test
git add -A && git commit -m "feat: module skeleton, CLI root, build and lint setup"
```

### Task 2: Configuration and workspace resolution

**Files:**
- Create: `internal/config/config.go`, `internal/config/resolve.go`, `internal/config/config_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/config"
)

const sample = `
workspaces:
  demo:
    repos:
      - path: %[1]s/a
      - path: %[1]s/b
        name: bee
        languages: [go]
    exclude: ["**/gen/**"]
`

func writeSample(t *testing.T) (cfgPath, root string) {
	t.Helper()
	root = t.TempDir()
	for _, d := range []string{"a", "b", "c"} {
		if err := os.MkdirAll(filepath.Join(root, d, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfgPath = filepath.Join(root, "config.yaml")
	body := []byte(fmtSprintf(sample, root))
	if err := os.WriteFile(cfgPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return cfgPath, root
}

func TestLoadDefaultsRepoNameToDirName(t *testing.T) {
	cfgPath, root := writeSample(t)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	ws := cfg.Workspaces["demo"]
	if ws.Repos[0].Name != "a" || ws.Repos[1].Name != "bee" {
		t.Fatalf("names: %+v", ws.Repos)
	}
	if ws.Repos[0].Path != filepath.Join(root, "a") {
		t.Fatalf("path not absolute: %s", ws.Repos[0].Path)
	}
	if cfg.DBPath("demo") == "" {
		t.Fatal("db path empty")
	}
}

func TestResolveWorkspaceFromCwd(t *testing.T) {
	cfgPath, root := writeSample(t)
	cfg, _ := config.Load(cfgPath)
	ws, repo, err := cfg.Resolve(filepath.Join(root, "b", "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if ws.Name != "demo" || repo.Name != "bee" {
		t.Fatalf("got %s %s", ws.Name, repo.Name)
	}
	if _, _, err := cfg.Resolve(filepath.Join(root, "c")); err == nil {
		t.Fatal("expected error for directory outside any workspace")
	}
}

func TestLoadRejectsDuplicateRepoNamesInWorkspace(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "x", "platform"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, "y", "platform"), 0o755)
	body := "workspaces:\n  w:\n    repos: [{path: " + root + "/x/platform}, {path: " + root + "/y/platform}]\n"
	p := filepath.Join(root, "c.yaml")
	_ = os.WriteFile(p, []byte(body), 0o644)
	if _, err := config.Load(p); err == nil {
		t.Fatal("expected duplicate name error")
	}
}

func TestLoadRejectsDirectoryInTwoWorkspaces(t *testing.T) {
	root := t.TempDir()
	body := "workspaces:\n  x:\n    repos: [{path: " + root + "}]\n  y:\n    repos: [{path: " + root + "}]\n"
	p := filepath.Join(root, "c.yaml")
	_ = os.WriteFile(p, []byte(body), 0o644)
	if _, err := config.Load(p); err == nil {
		t.Fatal("expected error")
	}
}
```

(`fmtSprintf` is `fmt.Sprintf`; import `fmt`.)

- [ ] **Step 2: Run, expect compile failure**

Run: `go test ./internal/config/`

- [ ] **Step 3: Implement `config.go`**

```go
// Package config loads the codedoc configuration file and resolves which
// workspace and repository a directory belongs to.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// Config is the parsed configuration file.
type Config struct {
	Workspaces map[string]*Workspace `yaml:"workspaces"`
	dataDir    string
}

// Workspace is a named set of repositories that may reference each other.
type Workspace struct {
	Name    string   `yaml:"-"`
	Repos   []*Repo  `yaml:"repos"`
	Exclude []string `yaml:"exclude"`
}

// Repo is one working tree inside a workspace.
type Repo struct {
	Path      string   `yaml:"path"`
	Name      string   `yaml:"name"`
	Languages []string `yaml:"languages"`
}

// DefaultPath returns the configuration file location.
func DefaultPath() string {
	if p := os.Getenv("CODEDOC_CONFIG"); p != "" {
		return p
	}
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "codedoc", "config.yaml")
}

// Load reads and validates a configuration file.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.dataDir = dataDir()
	return &cfg, cfg.normalize()
}

func dataDir() string {
	if p := os.Getenv("CODEDOC_DATA"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "codedoc")
}

// DBPath returns the SQLite file for a workspace.
func (c *Config) DBPath(workspace string) string {
	return filepath.Join(c.dataDir, workspace+".db")
}

func (c *Config) normalize() error {
	seen := map[string]string{}
	for name, ws := range c.Workspaces {
		ws.Name = name
		for _, r := range ws.Repos {
			abs, err := filepath.Abs(expandHome(r.Path))
			if err != nil {
				return err
			}
			r.Path = filepath.Clean(abs)
			if r.Name == "" {
				r.Name = filepath.Base(r.Path)
			}
			if other, dup := seen[r.Path]; dup && other != name {
				return fmt.Errorf("repo %s is in workspaces %s and %s", r.Path, other, name)
			}
			seen[r.Path] = name
		}
		if err := uniqueNames(name, ws.Repos); err != nil {
			return err
		}
	}
	return nil
}

func uniqueNames(ws string, repos []*Repo) error {
	names := map[string]bool{}
	for _, r := range repos {
		if names[r.Name] {
			return fmt.Errorf("workspace %s: two repos named %q; set name explicitly", ws, r.Name)
		}
		names[r.Name] = true
	}
	return nil
}

func expandHome(p string) string {
	if len(p) > 1 && p[:2] == "~/" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

// Names returns workspace names in sorted order.
func (c *Config) Names() []string {
	out := make([]string, 0, len(c.Workspaces))
	for n := range c.Workspaces {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 4: Implement `resolve.go`**

```go
package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Resolve finds the workspace and repository containing dir. The longest
// matching repo path wins, so nested checkouts resolve to the inner one.
func (c *Config) Resolve(dir string) (*Workspace, *Repo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, err
	}
	var bestWS *Workspace
	var bestRepo *Repo
	for _, ws := range c.Workspaces {
		for _, r := range ws.Repos {
			if !within(abs, r.Path) {
				continue
			}
			if bestRepo == nil || len(r.Path) > len(bestRepo.Path) {
				bestWS, bestRepo = ws, r
			}
		}
	}
	if bestRepo == nil {
		return nil, nil, fmt.Errorf("%s is not inside any configured repository", abs)
	}
	return bestWS, bestRepo, nil
}

func within(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}
```

- [ ] **Step 5: Run tests, expect PASS. Lint. Commit.**

```bash
go test ./internal/config/ && make lint
git add -A && git commit -m "feat(config): configuration file and workspace resolution"
```

### Task 3: Store and schema

**Files:**
- Create: `internal/store/store.go`, `internal/store/schema.go`, `internal/store/repos.go`, `internal/store/store_test.go`

- [ ] **Step 1: Write the failing test**

```go
package store_test

import (
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

func openTemp(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenCreatesSchemaAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	for i := 0; i < 2; i++ {
		s, err := store.Open(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		v, err := s.SchemaVersion()
		if err != nil || v < 1 {
			t.Fatalf("version %d err %v", v, err)
		}
		_ = s.Close()
	}
}

func TestUpsertRepoAndFiles(t *testing.T) {
	s := openTemp(t)
	id, err := s.UpsertRepo(store.Repo{Workspace: "demo", Name: "a", Path: "/tmp/a", Branch: "main", Commit: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	f := store.File{RepoID: id, Path: "x.go", Lang: "go", Size: 10, Mtime: 1, Hash: "h1", Lines: 2}
	fid, err := s.UpsertFile(f)
	if err != nil || fid == 0 {
		t.Fatalf("upsert file: %v", err)
	}
	got, err := s.FilesByRepo(id)
	if err != nil || len(got) != 1 || got[0].Hash != "h1" {
		t.Fatalf("files: %+v %v", got, err)
	}
	if err := s.DeleteFile(fid); err != nil {
		t.Fatal(err)
	}
	got, _ = s.FilesByRepo(id)
	if len(got) != 0 {
		t.Fatal("file not deleted")
	}
}
```

- [ ] **Step 2: Run, expect compile failure**

- [ ] **Step 3: Write `schema.go`** (the full spec section 8 schema, as one versioned migration)

```go
package store

// migrations are applied in order; the meta table records the version.
var migrations = []string{
	`
CREATE TABLE workspace(id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL);
CREATE TABLE repo(id INTEGER PRIMARY KEY, workspace_id INTEGER NOT NULL REFERENCES workspace(id),
  name TEXT NOT NULL, path TEXT NOT NULL UNIQUE, vcs TEXT NOT NULL DEFAULT 'git',
  branch TEXT, "commit" TEXT, indexed_at INTEGER, scip_commit TEXT, UNIQUE(workspace_id, name));
CREATE TABLE module(id INTEGER PRIMARY KEY, repo_id INTEGER NOT NULL REFERENCES repo(id) ON DELETE CASCADE,
  path TEXT NOT NULL, lang TEXT NOT NULL, kind TEXT NOT NULL, identity TEXT NOT NULL,
  parent_id INTEGER REFERENCES module(id), doc_text TEXT, doc_source TEXT, doc_state TEXT NOT NULL DEFAULT 'missing',
  UNIQUE(repo_id, lang, path));
CREATE INDEX module_identity ON module(identity);
CREATE TABLE file(id INTEGER PRIMARY KEY, repo_id INTEGER NOT NULL REFERENCES repo(id) ON DELETE CASCADE,
  path TEXT NOT NULL, lang TEXT, size INTEGER NOT NULL, mtime INTEGER NOT NULL, hash TEXT NOT NULL,
  lines INTEGER NOT NULL DEFAULT 0, generated INTEGER NOT NULL DEFAULT 0, parse_errors INTEGER NOT NULL DEFAULT 0,
  last_commit TEXT, last_commit_at INTEGER, UNIQUE(repo_id, path));
CREATE TABLE symbol(id INTEGER PRIMARY KEY, file_id INTEGER NOT NULL REFERENCES file(id) ON DELETE CASCADE,
  module_id INTEGER REFERENCES module(id), kind TEXT NOT NULL, name TEXT NOT NULL, qualified_name TEXT NOT NULL,
  stable_key TEXT NOT NULL, container_id INTEGER REFERENCES symbol(id),
  start_line INTEGER NOT NULL, end_line INTEGER NOT NULL, start_byte INTEGER NOT NULL, end_byte INTEGER NOT NULL,
  sig_start_line INTEGER NOT NULL, sig_end_line INTEGER NOT NULL, signature TEXT NOT NULL,
  visibility TEXT NOT NULL, is_test INTEGER NOT NULL DEFAULT 0, body_hash TEXT NOT NULL,
  doc_text TEXT, doc_hash TEXT, doc_body_hash TEXT, doc_state TEXT NOT NULL DEFAULT 'missing',
  doc_start_line INTEGER, doc_end_line INTEGER);
CREATE INDEX symbol_name ON symbol(name);
CREATE UNIQUE INDEX symbol_key ON symbol(stable_key);
CREATE INDEX symbol_file ON symbol(file_id);
CREATE TABLE edge(id INTEGER PRIMARY KEY, src_symbol_id INTEGER NOT NULL REFERENCES symbol(id) ON DELETE CASCADE,
  dst_symbol_id INTEGER REFERENCES symbol(id) ON DELETE CASCADE, kind TEXT NOT NULL, ref_kind TEXT NOT NULL, confidence TEXT NOT NULL,
  file_id INTEGER NOT NULL REFERENCES file(id) ON DELETE CASCADE, line INTEGER NOT NULL);
CREATE INDEX edge_dst ON edge(dst_symbol_id, kind);
CREATE INDEX edge_src ON edge(src_symbol_id, kind);
CREATE TABLE edge_candidate(edge_id INTEGER NOT NULL REFERENCES edge(id) ON DELETE CASCADE,
  symbol_id INTEGER NOT NULL REFERENCES symbol(id) ON DELETE CASCADE);
CREATE INDEX edge_candidate_symbol ON edge_candidate(symbol_id);
CREATE TABLE module_edge(src_module_id INTEGER NOT NULL REFERENCES module(id) ON DELETE CASCADE,
  dst_module_id INTEGER NOT NULL REFERENCES module(id) ON DELETE CASCADE, kind TEXT NOT NULL, confidence TEXT NOT NULL,
  PRIMARY KEY(src_module_id, dst_module_id, kind));
CREATE TABLE import(file_id INTEGER NOT NULL REFERENCES file(id) ON DELETE CASCADE, raw TEXT NOT NULL, alias TEXT,
  resolved_module_id INTEGER REFERENCES module(id), line INTEGER NOT NULL);
CREATE INDEX import_file ON import(file_id);
CREATE TABLE config_key(id INTEGER PRIMARY KEY, repo_id INTEGER NOT NULL REFERENCES repo(id) ON DELETE CASCADE,
  key TEXT NOT NULL, UNIQUE(repo_id, key));
CREATE INDEX config_key_key ON config_key(key);
CREATE TABLE config_use(key_id INTEGER NOT NULL REFERENCES config_key(id) ON DELETE CASCADE,
  file_id INTEGER NOT NULL REFERENCES file(id) ON DELETE CASCADE, symbol_id INTEGER REFERENCES symbol(id) ON DELETE CASCADE,
  line INTEGER NOT NULL, kind TEXT NOT NULL, format TEXT NOT NULL, value_preview TEXT);
CREATE INDEX config_use_file ON config_use(file_id);
CREATE TABLE repo_identity(repo_id INTEGER NOT NULL REFERENCES repo(id) ON DELETE CASCADE, lang TEXT NOT NULL,
  identity TEXT NOT NULL, path TEXT NOT NULL, PRIMARY KEY(repo_id, lang, identity));
CREATE TABLE reference_unresolved(file_id INTEGER NOT NULL REFERENCES file(id) ON DELETE CASCADE,
  symbol_id INTEGER REFERENCES symbol(id) ON DELETE CASCADE, name TEXT NOT NULL, line INTEGER NOT NULL, kind TEXT NOT NULL);
CREATE INDEX reference_unresolved_name ON reference_unresolved(name);
CREATE TABLE scip_symbol(stable_key TEXT PRIMARY KEY, scip_symbol TEXT NOT NULL);
CREATE VIRTUAL TABLE symbol_fts USING fts5(name, qualified_name, signature, doc_text, content='symbol', content_rowid='id');
CREATE VIRTUAL TABLE module_fts USING fts5(path, identity, doc_text, content='module', content_rowid='id');
`,
}
```

FTS content tables need the standard external-content triggers; add them in the same migration:

```sql
CREATE TRIGGER symbol_ai AFTER INSERT ON symbol BEGIN
  INSERT INTO symbol_fts(rowid, name, qualified_name, signature, doc_text) VALUES (new.id, new.name, new.qualified_name, new.signature, new.doc_text); END;
CREATE TRIGGER symbol_ad AFTER DELETE ON symbol BEGIN
  INSERT INTO symbol_fts(symbol_fts, rowid, name, qualified_name, signature, doc_text) VALUES ('delete', old.id, old.name, old.qualified_name, old.signature, old.doc_text); END;
CREATE TRIGGER symbol_au AFTER UPDATE ON symbol BEGIN
  INSERT INTO symbol_fts(symbol_fts, rowid, name, qualified_name, signature, doc_text) VALUES ('delete', old.id, old.name, old.qualified_name, old.signature, old.doc_text);
  INSERT INTO symbol_fts(rowid, name, qualified_name, signature, doc_text) VALUES (new.id, new.name, new.qualified_name, new.signature, new.doc_text); END;
```

(and the same three for `module` / `module_fts`). The `symbol_vec` table is added by Plan 4 when embeddings arrive.

- [ ] **Step 4: Write `store.go`**

```go
// Package store owns the SQLite schema and every read and write against it.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	_ "github.com/mattn/go-sqlite3" // sqlite driver
)

// Store is one open workspace database.
type Store struct{ db *sql.DB }

// Open opens or creates the database at path and applies migrations.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// SchemaVersion returns the number of applied migrations.
func (s *Store) SchemaVersion() (int, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&v)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(v)
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return err
	}
	current, err := s.SchemaVersion()
	if err != nil {
		return err
	}
	for i := current; i < len(migrations); i++ {
		if err := s.tx(func(tx *sql.Tx) error {
			if _, err := tx.Exec(migrations[i]); err != nil {
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			_, err := tx.Exec(`INSERT OR REPLACE INTO meta(key,value) VALUES('schema_version',?)`, strconv.Itoa(i+1))
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) tx(fn func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
```

`meta` is created by `migrate` before the migrations run, which is why it is absent from the migration string. Because `SetMaxOpenConns(1)` is set, code inside a `tx` callback must use only the `*sql.Tx` handle; calling `s.db.Query` or `s.db.Exec` from inside the callback deadlocks. This matters in `ReplaceFileSymbols`, resolution, and `Ensure`.

- [ ] **Step 5: Write `repos.go`** with `Repo`, `File` structs and `UpsertRepo` (inserts the workspace row if needed), `RepoByPath`, `UpdateRepoHead(id, branch, commit)`, `UpsertFile` (ON CONFLICT(repo_id,path) DO UPDATE all columns), `FilesByRepo`, `FileByPath`, `DeleteFile`. Struct fields mirror the columns; use `int64` ids.

- [ ] **Step 6: Run tests, expect PASS. Lint. Commit.**

```bash
make test && make lint
git add -A && git commit -m "feat(store): sqlite schema, migrations, repo and file rows"
```

### Task 4: Git metadata

**Files:**
- Create: `internal/gitmeta/git.go`, `internal/gitmeta/git_test.go`

- [ ] **Step 1: Write the failing tests** (create a temp git repo in the test with two commits touching different files, plus one untracked and one modified file)

```go
func TestHeadAndDirty(t *testing.T) {
	dir := initRepo(t) // helper: git init, commit a.go, commit b.go, modify a.go, create c.go untracked
	h, err := gitmeta.Head(dir)
	if err != nil || h.Branch != "main" && h.Branch != "master" || len(h.Commit) != 40 {
		t.Fatalf("%+v %v", h, err)
	}
	dirty, _ := gitmeta.DirtyFiles(dir)
	want := map[string]bool{"a.go": true, "c.go": true}
	if len(dirty) != 2 || !want[dirty[0]] || !want[dirty[1]] {
		t.Fatalf("dirty %v", dirty)
	}
}

func TestTrackedAndUntrackedFiles(t *testing.T) {
	dir := initRepo(t)
	files, _ := gitmeta.ListFiles(dir) // tracked plus untracked-not-ignored, sorted
	if strings.Join(files, ",") != "a.go,b.go,c.go" {
		t.Fatalf("%v", files)
	}
}

func TestLastCommitPerFile(t *testing.T) {
	dir := initRepo(t)
	m, _ := gitmeta.LastCommits(dir, "") // all history
	if m["a.go"].Commit == m["b.go"].Commit || m["a.go"].At == 0 {
		t.Fatalf("%+v", m)
	}
}
```

- [ ] **Step 2: Run, expect compile failure**

- [ ] **Step 3: Implement** with `os/exec` calls: `git symbolic-ref --short HEAD` (fall back to `git rev-parse --short HEAD` when detached, branch = `HEAD`), `git rev-parse HEAD`, `git status --porcelain --untracked-files=all` (dirty = any status line; path is the field after the two status chars, handle renames `a -> b` by taking `b`), `git ls-files --cached --others --exclude-standard`, and `git log --format=%H%x00%ct --name-only [since..HEAD]` parsed into `map[string]Commit{Commit, At int64}` keeping the first (newest) commit per path. All functions take `dir` and run with `cmd.Dir = dir`. Return a typed error `ErrNotRepo` when git exits with 128.

- [ ] **Step 4: Run tests, expect PASS. Lint. Commit.**

```bash
git commit -am "feat(gitmeta): head, dirty files, file listing, last commit per file"
```

### Task 5: Output frame with budgets

**Files:**
- Create: `internal/output/frame.go`, `internal/output/text.go`, `internal/output/json.go`, `internal/output/frame_test.go`

The frame is the only way queries produce output (spec section 9). Design:

```go
// Package output renders query results as compact text or JSON under a byte budget.
package output

// Header describes the index state the answer was computed from.
type Header struct {
	Repo, Branch, Commit string
	Dirty                int
	Fresh                bool
}

// Entry is one ranked result line. Body, when set, is a multi-line block
// (printed with line numbers starting at BodyStart) that is dropped as a
// whole when it does not fit.
type Entry struct {
	Key       string   // stable key, printed first
	Loc       string   // path:line
	Text      string   // signature or one-line description
	Doc       string   // first sentence of the doc, may be empty
	Tags      []string // e.g. "ambiguous", "test"
	Body      string
	BodyStart int
	Extra     map[string]any // JSON only
}

// Section groups entries under a title; sections render in order.
type Section struct {
	Title   string
	Entries []Entry
	// MoreHint is the exact follow-up when entries were omitted.
	MoreHint string
	omitted  int
}

// Response is a complete answer.
type Response struct {
	Header   Header
	Sections []Section
	Note     string // one line printed last, e.g. stale hint
}

// Render writes the response in the given format, enforcing budget bytes
// (0 means unlimited). It returns the number of omitted entries.
func Render(w io.Writer, r *Response, format string, budget int) (omitted int, err error)
```

Text layout:

```
auth main@3f2a1c dirty:2 fresh:yes
== callers (12)
auth:go:github.com/acme/auth/internal/api:TokenHandler.Serve  internal/api/token.go:88  func (h *TokenHandler) Serve(w http.ResponseWriter, r *http.Request)  Serves the token endpoint.  [ambiguous]
...
... 7 more; rerun with --offset 5 or --out FILE
```

Bodies render as `N\tcode` lines after the entry, indented by nothing (same as the Read tool).

- [ ] **Step 1: Write the failing tests**: (a) a response with 10 entries and a budget that fits 4 renders exactly 4 plus the "more" line, and returns `omitted == 6`; (b) an entry whose body does not fit is rendered as its header line only, and a `[body omitted; use --out]` tag appears; (c) JSON output round-trips through `json.Unmarshal` and contains `omitted` per section; (d) budget 0 renders everything.

- [ ] **Step 2: Run, expect failure**

- [ ] **Step 3: Implement.** Budget algorithm: render the header; for each section render entries in order, measuring the bytes of each rendered entry (with its body); if adding an entry would exceed the budget, try it without the body; if the header line alone does not fit, stop the section and count the rest as omitted. Reserve 120 bytes for the "more" line and the note. JSON: build a struct, marshal with indentation off; when over budget, drop bodies first, then entries from the end, recording `omitted`.

- [ ] **Step 4: Run tests, expect PASS. Lint. Commit.**

```bash
git commit -am "feat(output): response frame with text and json renderers under a byte budget"
```

### Task 6: Discovery, `index` (files only), `status`, fixtures

**Files:**
- Create: `internal/index/discover.go`, `internal/index/pipeline.go`, `internal/index/index_test.go`, `cmd/codedoc/cmd_index.go`, `testdata/ws/go-lib/...`, `testdata/ws/go-app/...`, `testdata/ws/ts-domain/...`, `testdata/ws/ts-app/...`

- [ ] **Step 1: Create the fixture repos.** Contents are deliberately small and exercise the spec's fixture list (section 16): an interface with two implementations, a cross-file call chain of depth 3, a test covering one function, a generated file, a second repo importing the first.

`testdata/ws/go-lib/go.mod`: `module github.com/fixture/lib` / `go 1.27`

`testdata/ws/go-lib/store/store.go`:

```go
// Package store holds the storage abstractions of the fixture library.
package store

import "errors"

// ErrMissing is returned when a key has no value.
var ErrMissing = errors.New("missing")

// Store persists string values by key.
type Store interface {
	// Get returns the value for key.
	Get(key string) (string, error)
	// Put stores value under key.
	Put(key, value string) error
}

// MemStore is an in-memory Store.
type MemStore struct {
	data map[string]string
}

// NewMemStore creates an empty MemStore.
func NewMemStore() *MemStore { return &MemStore{data: map[string]string{}} }

// Get implements Store.
func (m *MemStore) Get(key string) (string, error) {
	v, ok := m.data[key]
	if !ok {
		return "", ErrMissing
	}
	return v, nil
}

// Put implements Store.
func (m *MemStore) Put(key, value string) error {
	m.data[key] = value
	return nil
}
```

`testdata/ws/go-lib/store/null.go`: `NullStore` implementing `Store` with `Get` always returning `ErrMissing` and `Put` a no-op, no doc comments on the methods (a doc gap for Plan 3).

`testdata/ws/go-lib/store/store_test.go`: `TestMemStorePutGet` calling `NewMemStore`, `Put`, `Get`.

`testdata/ws/go-lib/service/service.go`:

```go
// Package service exposes the fixture's use cases.
package service

import "github.com/fixture/lib/store"

// Service answers lookups through a Store.
type Service struct{ s store.Store }

// New wires a Service to a Store.
func New(s store.Store) *Service { return &Service{s: s} }

// Lookup returns the value for key, or "" when missing.
func (svc *Service) Lookup(key string) string {
	v, err := svc.s.Get(key)
	if err != nil {
		return fallback(key)
	}
	return v
}

func fallback(key string) string { return "default:" + key }
```

`testdata/ws/go-lib/service/gen.go`: starts with `// Code generated by fixture. DO NOT EDIT.` and defines `func Generated() int { return 1 }`.

`testdata/ws/go-app/go.mod`: `module github.com/fixture/app` with `require github.com/fixture/lib v0.0.0` and `replace github.com/fixture/lib => ../go-lib`.

`testdata/ws/go-app/main.go`:

```go
// Command app is the fixture entry point.
package main

import (
	"fmt"

	"github.com/fixture/lib/service"
	"github.com/fixture/lib/store"
)

func main() {
	svc := service.New(store.NewMemStore())
	fmt.Println(run(svc))
}

func run(svc *service.Service) string { return svc.Lookup("k") }
```

`testdata/ws/ts-domain/package.json`: `{"name": "@fixture/domain", "version": "0.0.0", "type": "module"}`

`testdata/ws/ts-domain/src/records.ts`:

```ts
/** A record with a health flag. */
export interface Environment {
  name: string;
  health: Health;
}

/** Health states an environment can report. */
export type Health = 'ok' | 'degraded';

/** Repository of environments. */
export interface EnvironmentRepo {
  /** Loads one environment by name. */
  load(name: string): Environment | undefined;
}

/** In-memory EnvironmentRepo. */
export class MemoryRepo implements EnvironmentRepo {
  private items = new Map<string, Environment>();
  load(name: string): Environment | undefined {
    return this.items.get(name);
  }
  /** Stores an environment. */
  save(env: Environment): void {
    this.items.set(env.name, env);
  }
}

export function isHealthy(env: Environment): boolean {
  return env.health === 'ok';
}
```

`testdata/ws/ts-domain/src/records.test.ts`: imports `MemoryRepo`, `isHealthy` and has one `it(...)` calling both. `testdata/ws/ts-domain/src/index.ts`: `export * from './records.js';`

`testdata/ws/ts-app/package.json`: `{"name": "@fixture/app", "dependencies": {"@fixture/domain": "*"}}`
`testdata/ws/ts-app/src/main.ts`:

```ts
import { MemoryRepo, isHealthy } from '@fixture/domain';

export function check(name: string): boolean {
  const repo = new MemoryRepo();
  const env = repo.load(name);
  return env !== undefined && isHealthy(env);
}
```

Fixtures are plain files; tests obtain a git-backed copy through `testutil.CopyFixture`. Do not create a `.fixture` marker or any `.git` inside `testdata/`.

- [ ] **Step 2: Write the failing discovery test**

```go
func TestDiscoverListsSourceFilesWithLanguageAndHash(t *testing.T) {
	files, err := index.Discover(testutil.CopyFixture(t, "go-lib"), nil)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]index.Discovered{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	if byPath["store/store.go"].Lang != "go" || byPath["store/store.go"].Hash == "" {
		t.Fatalf("%+v", byPath["store/store.go"])
	}
	if !byPath["service/gen.go"].Generated {
		t.Fatal("generated file not flagged")
	}
	if _, ok := byPath["go.mod"]; !ok {
		t.Fatal("go.mod must be discovered (lang empty, used by identity map)")
	}
}

func TestDiscoverHonoursExcludeGlobs(t *testing.T) {
	files, _ := index.Discover(testutil.CopyFixture(t, "go-lib"), []string{"service/**"})
	for _, f := range files {
		if strings.HasPrefix(f.Path, "service/") {
			t.Fatalf("excluded file present: %s", f.Path)
		}
	}
}
```

- [ ] **Step 3: Implement `discover.go`**: `Discover(root string, exclude []string) ([]Discovered, error)` uses `gitmeta.ListFiles` (falls back to `filepath.WalkDir` when not a git repo), filters built-in exclusions (`vendor/`, `node_modules/`, `target/`, `build/`, `dist/`, `.git/`) and user globs (use `github.com/bmatcuk/doublestar/v4` for `**`), stats each file, reads it, computes `xxhash.Sum64` as hex, counts lines, detects language from extension (`.go`, `.ts`, `.tsx`, `.js`, `.jsx`, `.mjs`, `.cjs` in this plan; others keep `Lang == ""`), and sets `Generated` when the first 2 KB contain `Code generated`, `<auto-generated>`, `@generated`, or `DO NOT EDIT`. Skip files larger than 2 MB and binary files (NUL byte in the first 8 KB).

- [ ] **Step 4: Write the failing pipeline test**

```go
func TestIndexRepoStoresFilesAndIsIncremental(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture("go-lib")
	res, err := index.Repo(s, rep, index.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.FilesIndexed < 5 || res.FilesSkipped != 0 {
		t.Fatalf("%+v", res)
	}
	res, _ = index.Repo(s, rep, index.Options{})
	if res.FilesIndexed != 0 || res.FilesSkipped < 5 {
		t.Fatalf("second run should skip unchanged: %+v", res)
	}
}
```

`repoFixture` wraps `testutil.CopyFixture` and returns a `config.Repo` pointing at the copy.

- [ ] **Step 5: Implement `pipeline.go`**: `Repo(s *store.Store, r *config.Repo, opts Options) (Result, error)`:
  1. `gitmeta.Head`, `UpsertRepo`, `UpdateRepoHead`; when `gitmeta.ErrNotRepo` is returned, continue with empty branch and commit (`Discover` already falls back to a directory walk).
  2. `Discover`; load existing file rows; for each discovered file whose hash differs or is new: upsert file row (this plan stores files only; Task 11 adds extraction). Delete rows for files no longer present.
  3. `gitmeta.LastCommits(root, previousCommit)` and update `last_commit` columns for the affected paths; on first index pass `""` for full history.
  4. Record `indexed_at`.
  Result counts files indexed, skipped, deleted.

- [ ] **Step 6: Wire `codedoc index [--workspace W] [--repo R]` and `codedoc status`** in `cmd_index.go`. `index` loads config, resolves the workspace from cwd unless `--workspace` is given, opens the DB at `cfg.DBPath`, runs `index.Repo` for each repo (or the one named), prints one line per repo: `name branch@commit files:+N ~M -K`. `status` prints per repo: files, symbols (0 for now), last indexed, branch, commit, dirty count. Both render through `output.Render` with a `Section` of entries.

- [ ] **Step 7: Run all tests, lint, commit**

```bash
make test && make lint
git add -A && git commit -m "feat(index): file discovery, incremental file indexing, index and status commands, fixture workspace"
```

---

## Step 2: Go and TypeScript extraction, first queries

### Task 7: Parser layer

**Files:**
- Create: `internal/parse/parser.go`, `internal/parse/registry.go`, `internal/parse/treesitter.go`, `internal/parse/parse_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestParseGoAndTypeScript(t *testing.T) {
	for _, tc := range []struct{ lang, src, rootKind string }{
		{"go", "package p\nfunc F() {}\n", "source_file"},
		{"typescript", "export function f() {}\n", "program"},
		{"tsx", "const x = <div/>;\n", "program"},
	} {
		tree, err := parse.Parse(tc.lang, []byte(tc.src))
		if err != nil {
			t.Fatalf("%s: %v", tc.lang, err)
		}
		if k := tree.Root().Kind(); k != tc.rootKind {
			t.Fatalf("%s: root %s", tc.lang, k)
		}
		tree.Close()
	}
	if _, err := parse.Parse("cobol", []byte("x")); err == nil {
		t.Fatal("unknown language must error")
	}
}

func TestLanguageForPath(t *testing.T) {
	for p, want := range map[string]string{"a.go": "go", "b.ts": "typescript", "c.tsx": "tsx", "d.js": "javascript", "e.txt": ""} {
		if got := parse.LanguageForPath(p); got != want {
			t.Fatalf("%s: %q", p, got)
		}
	}
}
```

- [ ] **Step 2: Run, expect compile failure**

- [ ] **Step 3: Implement.** `parser.go` defines the narrow interface the extractors use, so the CGO binding can be swapped:

```go
// Tree is a parsed file. Close must be called.
type Tree interface {
	Root() Node
	Close()
}

// Node is a syntax node. Positions are 0-based rows; callers add 1 for lines.
type Node interface {
	Kind() string
	IsNamed() bool
	StartByte() uint
	EndByte() uint
	StartRow() uint
	EndRow() uint
	ChildCount() uint
	Child(i uint) Node
	NamedChildCount() uint
	NamedChild(i uint) Node
	ChildByField(name string) Node // nil when absent
	Parent() Node
	PrevSibling() Node
	NextSibling() Node
	Text(src []byte) string
	HasError() bool
}
```

`treesitter.go` wraps `tree_sitter.Parser`, `Tree`, `Node` (pointer receivers; nil-safe wrappers returning nil `Node` interfaces for missing children, taking care to return an untyped nil). `registry.go` also exposes `Family(lang string) string` mapping `typescript`, `tsx`, `javascript` to `ts` and `go` to `go`; the family is what stable keys and `module.lang` use (spec 5.1), the precise name is what `file.lang` stores. `registry.go` maps language names to `*tree_sitter.Language` built once with `tree_sitter.NewLanguage(tree_sitter_go.Language())`, `LanguageTypescript()`, `LanguageTSX()`; JavaScript uses the TypeScript grammar for this plan (note in the code: `tree-sitter-javascript` can be added later). `Parse(lang, src)` creates a parser per call (parsers are cheap; avoid shared mutable state), sets the language, parses, and returns the wrapper. `LanguageForPath` maps extensions as in the test.

- [ ] **Step 4: Run tests, expect PASS. Lint. Commit.**

```bash
git commit -am "feat(parse): tree-sitter parser layer with go and typescript grammars"
```

### Task 8: Extraction record types and registry

**Files:**
- Create: `internal/extract/types.go`, `internal/extract/extractor.go`, `internal/extract/extractor_test.go`

- [ ] **Step 1: Write `types.go`** (no test needed for pure data; the test in step 2 covers the registry)

```go
// Package extract defines the records every language extractor produces.
package extract

// Kind classifies a symbol.
type Kind string

// Symbol kinds. Containers (class, struct, interface) nest other symbols.
const (
	KindFunction  Kind = "function"
	KindMethod    Kind = "method"
	KindClass     Kind = "class"
	KindStruct    Kind = "struct"
	KindInterface Kind = "interface"
	KindTrait     Kind = "trait"
	KindEnum      Kind = "enum"
	KindTypeAlias Kind = "type"
	KindConst     Kind = "const"
	KindVar       Kind = "var"
	KindField     Kind = "field"
)

// Visibility is the language's notion of exported or public.
type Visibility string

// Visibility values.
const (
	Public  Visibility = "public"
	Package Visibility = "package"
	Private Visibility = "private"
)

// Span is a half-open byte range with 1-based inclusive line numbers.
type Span struct {
	StartByte, EndByte uint
	StartLine, EndLine int
}

// Symbol is one declaration.
type Symbol struct {
	Kind       Kind
	Name       string
	Qualified  string // dot path inside the module; TS/JS prefix the file path relative to the package root (spec 5.1)
	Container  string // Qualified of the enclosing symbol, "" at top level
	Span       Span   // whole declaration including body
	Signature  Span   // the signature lines only
	SigText    string // signature text, single line, whitespace-collapsed
	Visibility Visibility
	IsTest     bool
	BodyHash   string // xxhash of the body tokens with whitespace removed
	Doc        *Doc   // nil when undocumented
}

// Doc is the documentation attached to a symbol or module.
type Doc struct {
	Text string // comment markers stripped, paragraphs preserved
	Span Span
}

// RefKind classifies a reference.
type RefKind string

// Reference kinds.
const (
	RefCall        RefKind = "call"
	RefType        RefKind = "type"
	RefInstantiate RefKind = "instantiate"
	RefField       RefKind = "field"
	RefExtend      RefKind = "extend"
	RefImplement   RefKind = "implement"
	RefConfig      RefKind = "config" // candidate config key literal
)

// Reference is a use of a name at a position.
type Reference struct {
	Kind      RefKind
	Name      string // the identifier as written, e.g. "Lookup"
	Qualifier string // receiver or package text before the name, e.g. "svc.s" or "store"
	Enclosing string // Qualified of the enclosing symbol, "" at file level
	Line      int
	Arity     int // call argument count, -1 when not a call
}

// Import is one import statement.
type Import struct {
	Raw   string // module path or specifier as written
	Alias string // local name, "" when none
	Names []string // named imports for TS/JS, nil for Go
	Line  int
}

// FileResult is everything extracted from one file.
type FileResult struct {
	Symbols     []Symbol
	References  []Reference
	Imports     []Import
	ModuleDoc   *Doc // package or module level doc found in this file
	ModuleName  string // Go package name; "" for other languages
	ParseErrors int
}
```

- [ ] **Step 2: Write the failing registry test**: `extract.For("go")` returns a non-nil extractor after the Go extractor registers itself (test lives in the golang package later); here test that `extract.For("none")` returns `nil, false` and that `Register` panics on duplicate.

- [ ] **Step 3: Implement `extractor.go`**

```go
// Extractor turns one file into records. Implementations are pure: they
// receive bytes and return records, and must not touch the store.
type Extractor interface {
	Extract(path string, src []byte) (*FileResult, error)
}

var registry = map[string]Extractor{}

// Register installs an extractor for a language name; duplicates panic.
func Register(lang string, e Extractor) { ... }

// For returns the extractor for a language.
func For(lang string) (Extractor, bool) { ... }
```

- [ ] **Step 4: Run, PASS, lint, commit** `feat(extract): record types and extractor registry`

### Task 9: Go extractor, symbols and docs

**Files:**
- Create: `internal/extract/golang/golang.go`, `internal/extract/golang/walk.go`, `internal/extract/golang/golang_test.go`, `internal/extract/golden/go-lib/store/store.go.json` (and one per fixture Go file)

Go grammar node kinds to handle (tree-sitter-go v0.25):

| Node kind | Symbol | Name field | Notes |
|---|---|---|---|
| `function_declaration` | function | `name` | `IsTest` when file ends in `_test.go` and name starts with `Test`, `Benchmark`, `Fuzz`, `Example` |
| `method_declaration` | method | `name` | receiver type from `receiver` > `parameter_declaration` > `type` (strip `*`); Qualified = `Recv.Name`, Container = `Recv` |
| `type_declaration` > `type_spec` | struct / interface / type | `name` | kind by `type` child kind: `struct_type`, `interface_type`, else `type` |
| `type_declaration` > `type_alias` | type | `name` | `type A = B` |
| `struct_type` > `field_declaration_list` > `field_declaration` | field | `name` (may have several names) | Container = struct Qualified |
| `interface_type` > `method_elem` | method | `name` | Container = interface Qualified; no body |
| `const_declaration` > `const_spec`, `var_declaration` > `var_spec` | const / var | `name` | one Symbol per name; top level only |

Visibility: first rune upper case means `Public`, else `Package`. Signature span: from the declaration start to the start of the `body` field (or the whole node when no body), SigText with whitespace collapsed. Doc: consecutive `comment` siblings immediately preceding the node with no blank line between them and the node (compare end row of the last comment with the node's start row minus 1); strip `//` and `/* */`, trim one leading space per line. Module doc: the comment block preceding the `package_clause`. `ModuleName`: text of `package_clause` > `package_identifier`. BodyHash: xxhash of the body node text with all whitespace removed (tokens unchanged).

- [ ] **Step 1: Write the golden test harness**

```go
func TestGolden(t *testing.T) {
	files := walkFixtures(t, "go-", ".go") // filepath.WalkDir over testdata/ws/go-*; Glob does not expand **
	for _, f := range files {
		rel := relativeToWS(f) // "go-lib/store/store.go"
		src, _ := os.ReadFile(f)
		got, err := golang.New().Extract(rel, src)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		golden.Check(t, rel+".json", got) // writes when -update is set, compares otherwise
	}
}
```

Put `golden.Check` in `internal/extract/golden/golden.go` (JSON marshal with indent, `-update` flag). Commit golden files after eyeballing them: every expected symbol from the fixture must be present with correct lines, docs attached, `fallback` as `Package` visibility, `TestMemStorePutGet` with `IsTest`, `Generated` ignored here (discovery flags it).

- [ ] **Step 2: Run, expect failure (package missing)**

- [ ] **Step 3: Implement `golang.go` and `walk.go`.** `walk.go` holds the recursive walker over `parse.Node` with a `switch n.Kind()`; `golang.go` holds the per-kind builders (`funcSymbol`, `methodSymbol`, `typeSymbols`, `valueSymbols`), `docFor(n)`, `signatureOf(n)`, `bodyHash(n)`. Keep each function under 40 lines; the kind table above is the spec. Register with `extract.Register("go", New())` in `init`.

- [ ] **Step 4: Run with `-update`, review golden files by hand, run again without, PASS. Lint. Commit.**

```bash
go test ./internal/extract/golang/ -update && go test ./internal/extract/golang/
git add -A && git commit -m "feat(extract/golang): go symbols, signatures, docs, test detection with golden tests"
```

### Task 10: TypeScript extractor, symbols and docs

**Files:**
- Create: `internal/extract/ts/ts.go`, `internal/extract/ts/walk.go`, `internal/extract/ts/ts_test.go`, golden files under `internal/extract/golden/ts-*/`

Node kinds (tree-sitter-typescript v0.23):

| Node kind | Symbol | Name field | Notes |
|---|---|---|---|
| `function_declaration` | function | `name` | |
| `class_declaration`, `abstract_class_declaration` | class | `name` | children in `body` (`class_body`) |
| `method_definition` | method | `name` | Container = class; `constructor` named `constructor` |
| `public_field_definition` | field | `name` | |
| `interface_declaration` | interface | `name` | `method_signature` children become methods with no body |
| `type_alias_declaration` | type | `name` | |
| `enum_declaration` | enum | `name` | |
| `lexical_declaration` > `variable_declarator` at top level | function when `value` is `arrow_function` or `function_expression`, else const | `name` | |
| `export_statement` | unwrap: the inner declaration is exported | | |

Visibility: `Public` when under an `export_statement` (or `export default`), `Private` for class members with `private`/`#name`, else `Package`. Qualified: `<file path relative to the package root>/<Class>.<member>` per spec 5.1, e.g. `src/records.ts/MemoryRepo.load`; top-level `src/records.ts/isHealthy`. The extractor receives the path relative to the package root; the golden harness passes the repo-relative path (`src/records.ts`), which for the fixtures equals the package-relative path because the package root is the repo root, so Task 16 changes nothing in the goldens. For real repos the pipeline computes the package-relative path from the identity map (Task 16), and when no `package.json` exists the identity is the repo-relative directory (spec 5.1). Doc: a `comment` sibling immediately before the declaration (or before the wrapping `export_statement`) whose text starts with `/**`; strip `/**`, `*/`, leading ` * `. `IsTest`: file name matches `*.test.*`, `*.spec.*`, or lives under `__tests__/`; test symbols are the `it(`/`test(` call sites, recorded as synthetic function symbols named by their string literal argument (`"loads and checks health"`), Qualified `src/records.test.ts/it:loads and checks health`, so `tests` queries have something to point at. Qualified names may contain colons and spaces; the stable-key parser takes the fourth `SplitN` part verbatim, so `NewStableKey` must not reject them. Module doc: a leading `/**` comment before the first statement of `index.ts` only.

- [ ] **Step 1: Golden test harness like Task 9 over `testdata/ws/ts-*/src/*.ts`**
- [ ] **Step 2: Run, expect failure**
- [ ] **Step 3: Implement**, register for `typescript`, `tsx`, `javascript`.
- [ ] **Step 4: Update goldens, review, PASS, lint, commit** `feat(extract/ts): typescript symbols, docs, test detection with golden tests`

### Task 11: Stable keys and storing symbols

**Files:**
- Create: `internal/index/stablekey.go`, `internal/index/stablekey_test.go`, `internal/store/symbols.go`, `internal/store/symbols_test.go`
- Modify: `internal/index/pipeline.go`

- [ ] **Step 1: Write the failing stable key tests**

```go
func TestStableKeyRoundTrip(t *testing.T) {
	k := index.StableKey{Repo: "auth", Lang: "go", Module: "github.com/acme/auth/internal/services", Qualified: "TokenService.Issue", SigHash: ""}
	s := k.String()
	if s != "auth:go:github.com/acme/auth/internal/services:TokenService.Issue" {
		t.Fatal(s)
	}
	back, err := index.ParseStableKey(s)
	if err != nil || back != k {
		t.Fatalf("%+v %v", back, err)
	}
	withHash := index.StableKey{Repo: "p", Lang: "java", Module: "com/acme/ptp", Qualified: "A.get", SigHash: "7f3a"}
	if index.MustParse(withHash.String()) != withHash {
		t.Fatal("hash round trip")
	}
	if _, err := index.ParseStableKey("only:two"); err == nil {
		t.Fatal("expected error")
	}
}
```

- [ ] **Step 2: Implement `stablekey.go`**: `String()` joins `Repo:Lang:Module:Qualified` and appends `#SigHash` when set; `ParseStableKey` strips `#...` from the end, then `strings.SplitN(rest, ":", 4)` requiring 4 parts; identities never contain `:` (`NewStableKey` replaces `:` with `/`, which is also how Maven `group:artifact` and Rust `crate::a::b` identities are written in keys, spec 5.1).

- [ ] **Step 3: Write failing store tests for symbols**: `ReplaceFileSymbols(fileID, moduleID, []store.Symbol)` deletes the file's symbols and inserts the new ones, returning the id map by stable key; `SymbolByKey`, `SymbolsByName(name, kind, repoID)`, `SymbolsInFile(fileID)` ordered by start line, `SymbolsInModule(moduleID)`; container ids resolved in a second pass by `Container` qualified name within the same file. Also `SymbolByLocation(fileID, line)` returning the innermost symbol containing the line (needed by resolve).

- [ ] **Step 4: Implement `symbols.go`**; the `Symbol` store struct mirrors the table.

- [ ] **Step 5: Extend `pipeline.go`**: for each changed file with a registered extractor, parse and extract, build stable keys (`Repo` = repo name, `Lang` = language family, `Module` = module identity from Task 16 or, until then, the directory path relative to the repo root (store `module_id` as NULL until Task 16 creates module rows; a zero id would violate the foreign key), `Qualified`, `SigHash` empty for Go and TypeScript except on a collision of `Qualified` inside a module, where every colliding symbol gets the first 4 hex of xxhash of its file path, spec 5.1), compute `doc_hash`, keep `doc_body_hash` and `doc_state` as `missing`/`ok` placeholders (Plan 3 owns staleness), check `SymbolByKey` for each key owned by another file of the same module (the build-tag collision case) and on a hit apply the path-hash suffix to both symbols, then call `ReplaceFileSymbols`. Record `parse_errors`. Add an index test: after indexing `go-lib`, `SymbolByKey("go-lib:go:store:MemStore.Get")` exists with `start_line` 26 (the doc comment is line 25 and has its own span) and doc text `Get implements Store.`; re-indexing without changes touches no symbol rows (count unchanged, ids unchanged); editing the copy of `store.go` to add a function and re-indexing adds exactly one symbol.

- [ ] **Step 6: PASS, lint, commit** `feat(index): stable keys and symbol storage in the pipeline`

### Task 12: Freshness check

**Files:**
- Create: `internal/index/freshness.go`, `internal/index/freshness_test.go`

- [ ] **Step 1: Failing test**: index a temp copy of `go-lib`; modify `store/null.go`; call `index.Ensure(s, repo, index.Options{MaxFiles: 200})`; expect `fresh == true` and the new symbol visible; then set `MaxFiles: 0` after touching 2 files and expect `fresh == false` with no re-extraction.

- [ ] **Step 2: Implement** `Ensure(s, repo, opts) (Freshness, error)`: `MaxFiles` has no implicit default (the test above uses 0 to mean "re-extract nothing"); `Ctx.EnsureFresh` (Task 13) passes 200, or the value of `CODEDOC_FRESH_MAX_FILES` when set. Run `Discover` again (this finds added files and reveals deleted ones), diff the discovered set against the known file rows, stat known files and compare mtime and size, hash only the changed ones, compare hashes; if the number of added plus changed files is within `MaxFiles`, run the pipeline for those files, delete rows of files that are gone (cascades remove their symbols and edges, and the resolver re-resolves files that pointed at them), and return `Fresh: true`; else return `Fresh: false, Changed: n` without touching the index. Add a test case: create a new file in the temp copy and expect `find` to see its symbol; delete a file and expect its symbols gone. Also add `codedoc index --rebuild` (drops and recreates the database file, then indexes) to Task 6's command, per spec section 15. Also refresh branch and commit from `gitmeta.Head` (cheap) and compute dirty count. Target: under 300 ms for 20,000 files; add a benchmark `BenchmarkEnsure` over a synthetic tree of 5,000 files to keep this honest.

- [ ] **Step 3: PASS, lint, commit** `feat(index): query-time freshness check`

### Task 13: `outline`, `find`, `symbol` queries and symbol addressing

**Files:**
- Create: `internal/query/query.go` (Context: store, config, workspace, output options), `internal/query/addr.go`, `internal/query/outline.go`, `internal/query/find.go`, `internal/query/symbol.go`, `internal/query/query_test.go`, `cmd/codedoc/cmd_query.go`

File and directory addresses for `outline`: a repo-relative path when `Ctx.Repo` is set (cwd inside a repo), otherwise `repo:path`. Symbol address forms (spec 5.1): a full stable key; `repo:path#Qualified` (`go-lib:store/store.go#MemStore.Get`); `path#Qualified` relative to the current repo; a bare qualified name or name (`MemStore.Get`, `Get`). Parsing order from the spec: three or more colons with a known language name in the second segment is a stable key (with or without a `#hash` suffix); else a string containing `#` is a path form; else bare. Resolution: stable key by lookup; `path#` forms by file lookup; otherwise `SymbolsByName` on the last dot segment filtered by the preceding segments matching the end of `qualified_name`. Zero hits returns `ErrNotFound`; more than one returns `ErrAmbiguous` with the candidates rendered as a section (the CLI still prints them, exit 3).

- [ ] **Step 1: Failing tests** against an indexed temp copy of the whole fixture workspace (helper `indexedWorkspace(t)` that indexes all four repos into one temp DB):
  - `outline` of `go-lib/store/store.go` lists `ErrMissing`, `Store` (with nested `Get`, `Put` collapsed as `+2 members`), `MemStore`, `NewMemStore`, `MemStore.Get`, `MemStore.Put` in line order with kinds, one-line docs, and a `[nodoc]` tag on undocumented symbols (doc *state* arrives with Plan 3); `outline` of directory `go-lib/store` lists its three files (`null.go`, `store.go`, `store_test.go`) with symbol counts and line counts.
  - `find Get` returns three symbols (`Store.Get`, `MemStore.Get`, `NullStore.Get`); `find Get --kind method --repo go-lib` the same; `find '*Store' --kind struct` returns `MemStore`, `NullStore`.
  - `symbol MemStore.Get` is ambiguous? No: it is unique. Returns signature, doc, span, container `MemStore`, module, and with `--body` the body text with line numbers starting at the declaration line. `symbol Get` returns `ErrAmbiguous` with three candidates.
  - Every response header shows `fresh:yes`.

- [ ] **Step 2: Run, expect failure**

- [ ] **Step 3: Implement.** `query.go`:

```go
// Ctx carries everything a query needs.
type Ctx struct {
	Store     *store.Store
	Workspace *config.Workspace
	Repo      *config.Repo // current repo (from cwd), may be nil with --workspace
	Fresh     map[int64]index.Freshness // per repo id, filled by EnsureFresh
}

// EnsureFresh runs the freshness check for the repos a query will touch.
func (c *Ctx) EnsureFresh(repoIDs ...int64) error
```

Each query returns `(*output.Response, error)`. The CLI layer (`cmd_query.go`) adds the shared flags `--workspace`, `--repo`, `--format`, `--budget` (default 8000), `--out`, `--offset`, parses them once in a `persistentPreRun`, builds the `Ctx`, calls the query, renders. With `--out`, render unlimited to the file and render a 600-byte summary to stdout ending with `full result: <path>`.

Body rendering uses `Entry.Body` with `BodyStart = start_line`; the text is read from disk (not stored) using the stored byte span, after `EnsureFresh` guaranteed the span is current.

- [ ] **Step 4: PASS, lint, commit** `feat(query): outline, find, symbol with symbol addressing and freshness`

- [ ] **Step 5: Manual smoke test** (and record the output in the commit message of the next task if anything surprising shows up):

```bash
make build
CODEDOC_CONFIG=$PWD/testdata/ws/config.yaml CODEDOC_DATA=/tmp/cd ./codedoc index --workspace fixture
cd testdata/ws/go-lib && CODEDOC_CONFIG=../config.yaml CODEDOC_DATA=/tmp/cd ../../../codedoc outline store/store.go
```

Add `testdata/ws/config.yaml` declaring workspace `fixture` with the four repos. Relative repo paths resolve against the config file's directory, not the cwd: change `normalize` to take the config directory (`Load` has the path) and add a test in Task 2's file.

---

## Step 3: References, modules, resolution, graph queries

### Task 14: Reference and import extraction (Go)

**Files:**
- Create: `internal/extract/golang/refs.go`
- Modify: `internal/extract/golang/walk.go`, golden files

Reference rules for Go:

| Node | Reference |
|---|---|
| `call_expression` with `function` = `identifier` | `RefCall`, Name = identifier, Qualifier = "", Arity = argument count |
| `call_expression` with `function` = `selector_expression` | `RefCall`, Name = `field` text, Qualifier = `operand` text |
| `composite_literal` with `type` = `type_identifier` or `qualified_type` | `RefInstantiate` |
| `type_identifier` / `qualified_type` anywhere else (parameters, fields, results, conversions) | `RefType` (skip predeclared: `string`, `int`, `error`, `bool`, `byte`, numeric types) |
| `selector_expression` not in call position | `RefField`, Name = field, Qualifier = operand |
| `import_spec` | `Import{Raw: path without quotes, Alias: name text or ""}` |

Enclosing: the Qualified of the innermost symbol whose span contains the node. Interface satisfaction is structural in Go, so no `RefImplement`; the resolver derives `implements` edges by method-set matching (Task 18).

- [ ] **Step 1: Extend the golden test expectations** (update goldens after implementing; verify by hand that `service.go` yields `svc.s.Get` as a call with Qualifier `svc.s`, `fallback` as a bare call with Arity 1, and `store.Store` as a type reference).
- [ ] **Step 2: Implement `refs.go`**, called from the walker for every node.
- [ ] **Step 3: Update goldens, PASS, lint, commit** `feat(extract/golang): references and imports`

### Task 15: Reference and import extraction (TypeScript)

**Files:**
- Create: `internal/extract/ts/refs.go`; modify walker and goldens

| Node | Reference |
|---|---|
| `call_expression` with `function` = `identifier` | `RefCall` |
| `call_expression` with `function` = `member_expression` | `RefCall`, Name = `property`, Qualifier = `object` text |
| `new_expression` | `RefInstantiate`, Name = `constructor` text |
| `type_identifier`, `generic_type` name | `RefType` |
| `extends_clause` children (in `class_heritage`) | `RefExtend` |
| `implements_clause` children | `RefImplement` |
| `import_statement` | `Import{Raw: source string, Names: named imports, Alias: default or namespace import}` |
| `export_statement` with a `source` field (`export * from './x.js'`, `export { a } from './x.js'`) | `Import{Raw: source, Names: re-exported names or nil for *}` |

- [ ] Steps as Task 14. Commit `feat(extract/ts): references and imports`

### Task 16: Module identity map and module rows

**Files:**
- Create: `internal/resolve/identity.go`, `internal/resolve/identity_test.go`, `internal/store/modules.go`, `internal/store/modules_test.go`
- Modify: `internal/index/pipeline.go`

Identity rules for this plan:

| Language | Module unit | Identity | Source |
|---|---|---|---|
| Go | directory containing `.go` files | `<go.mod module path>/<dir relative to go.mod dir>` (the go.mod dir itself is the bare module path) | nearest `go.mod` upward |
| TS/JS | nearest `package.json` directory | package `name`; subdirectories are modules with identity `<name>/<relative dir>` | `package.json` |

Module `kind` is `go-package` or `npm-package`/`npm-dir`; `parent_id` links subdirectories to the package root module.

- [ ] **Step 1: Failing tests**: `resolve.Identities(repoRoot, discoveredFiles)` returns, for `go-lib`, the repo identity `github.com/fixture/lib` (from `go.mod`, path `go.mod`) and modules `store` with identity `github.com/fixture/lib/store` and `service` with `github.com/fixture/lib/service`; for `ts-domain`, repo identity `@fixture/domain` (path `package.json`), root module `@fixture/domain` and `src` with `@fixture/domain/src`. Store test: `ReplaceRepoIdentities(repoID, []Identity)`, `ReposProviding(lang, identity)` (in config order), `UpsertModule`, `ModuleByIdentity(identity)`, `ModuleByPath(repoID, lang, path)`.
- [ ] **Step 2: Implement.** The pipeline computes identities before extraction so symbols get real module identities in their stable keys (replace the directory placeholder from Task 11; update the Task 11 test expectation to `go-lib:go:github.com/fixture/lib/store:MemStore.Get`).
- [ ] **Step 3: PASS, lint, commit** `feat(resolve): module identities for go and npm packages`

### Task 17: Import resolution and module edges

**Files:**
- Create: `internal/resolve/imports.go`, test; modify `internal/store/edges.go` (module edges), pipeline

- [ ] **Step 1: Failing tests**: after indexing the workspace, `go-app/main.go` has import rows resolved to `go-lib` modules `service` and `store` (cross-repo), and `module_edge` rows from `github.com/fixture/app` to both; `ts-app/src/main.ts` import `@fixture/domain` resolves to the `ts-domain` root module; a relative import `./records.js` in `ts-domain/src/index.ts` resolves to the `src` module of the same repo. An import with no provider (`fmt`) stays unresolved and produces no edge.
- [ ] **Step 2: Implement**: Go: exact identity match; TS: bare specifier matches a package name or `name/subpath`; relative specifiers resolve against the importing file's directory (strip `.js`/`.ts` extension, `index` files map to the directory). When several repos provide the identity, prefer the importing repo, else the first in config order (spec 7.3).
- [ ] **Step 3: PASS, lint, commit** `feat(resolve): import resolution and module edges, cross-repo`

### Task 18: Reference resolution tiers and edges

**Files:**
- Create: `internal/resolve/resolve.go`, `internal/resolve/tiers.go`, `internal/resolve/implements.go`, `internal/resolve/resolve_test.go`; modify `internal/store/edges.go`, pipeline

Algorithm per reference (spec 7.3), executed after all files of the changed set are stored:

1. Determine `src` = enclosing symbol (by `Enclosing` qualified name in the same file; file-level references with no enclosing symbol are stored in `reference_unresolved` with `symbol_id` null and skipped for edges).
2. Candidate lookup by `Name` (and `Kind` compatibility: `RefCall` matches function/method, `RefType`/`RefInstantiate` match struct/class/interface/type/enum, `RefField` matches field or method, `RefExtend`/`RefImplement` match class/interface).
3. Tier `local`: candidates in the same file, else same module; exactly one wins.
4. Tier `import`: if `Qualifier` equals an import alias (Go: last path segment or alias; TS: a `Names` entry or namespace alias maps the bare name instead), restrict candidates to the resolved module and its descendant modules (walk `parent_id`); exactly one wins. For TS named imports, a bare `isHealthy` whose name appears in an import's `Names` resolves through that import.
5. Tier `unique`: exactly one candidate in the workspace with matching arity (methods: ignore receiver). Receiver-qualified calls like `svc.s.Get` where the qualifier is not an import: candidates are all methods named `Get`; `unique` applies only if one exists, otherwise `ambiguous`.
6. `ambiguous`: edge with null `dst` plus `edge_candidate` rows (cap 20 candidates; beyond that, store as unresolved with a count).
7. Otherwise `reference_unresolved` (also where `RefConfig` candidates go in this plan, with kind `config`; Plan 2 matches them against `config_key`).

Edge kind: `calls` for `RefCall`, `references` for `RefType`/`RefInstantiate`/`RefField`, `extends` for `RefExtend`, `implements` for `RefImplement`; `ref_kind` stores the extractor kind verbatim.

Go `implements`: for each interface in the changed modules, compute the method name set; for each struct in the same repo (and in repos importing that module) whose method set covers it, insert an `implements` edge with confidence `local` (same module) or `unique`, `ref_kind` `implement`, `file_id` and `line` of the struct declaration. Limit to interfaces with at least one method.

Re-resolution scope: the changed files, plus every file holding an unresolved or ambiguous reference whose `Name` matches a symbol added or removed in this run, plus files with edges or candidates pointing at removed symbols. The pipeline collects the names and ids of symbols about to be removed (and the file ids of edges and `edge_candidate` rows pointing at them) before calling `DeleteFile` or `ReplaceFileSymbols`, because the cascade erases that information; an ambiguous edge left with one candidate after a removal is re-resolved to a plain edge this way.

- [ ] **Step 1: Failing tests** (over the indexed fixture workspace):
  - `callers` of `go-lib ... service.fallback` = `Service.Lookup` with confidence `local`.
  - `svc.s.Get` in `Service.Lookup` is `ambiguous` with candidates `Store.Get`, `MemStore.Get`, `NullStore.Get`.
  - `go-app main.run` calls `Service.Lookup` with confidence `unique` (the qualifier `svc` is a local variable, not an import; there is one `Lookup` in the workspace); `main.main` calls `store.NewMemStore` with `import` and `service.New` with `import` (cross-repo).
  - `MemStore` and `NullStore` have `implements` edges to `Store`.
  - `ts-app check` calls `isHealthy` with confidence `import` (the specifier `@fixture/domain` resolves to the root module; the import tier searches that module and its descendants by `parent_id`, which includes `src`); `new MemoryRepo()` is an `instantiate` reference resolved `import`; `repo.load` is `ambiguous` with candidates `EnvironmentRepo.load` and `MemoryRepo.load` (interface members are candidates like any other; SCIP is what makes this precise later).
  - `MemoryRepo implements EnvironmentRepo` edge with confidence `local`.
  - After deleting `NullStore` from the temp copy and re-indexing, the `svc.s.Get` edge has 2 candidates.
- [ ] **Step 2: Implement** across the three files, each function under 40 lines.
- [ ] **Step 3: PASS, lint, commit** `feat(resolve): tiered reference resolution, implements detection, re-resolution on change`

### Task 19: Graph queries

**Files:**
- Create: `internal/query/graph.go`, `internal/query/siblings.go`, `internal/query/deps.go`, `internal/store/edges.go` additions (recursive CTE helpers), tests; modify `cmd/codedoc/cmd_query.go`

Queries (spec section 9):

- `callers <sym> [--depth N] [--no-tests] [--repo R] [--group-by file]`: direct edges `kind=calls` where `dst = sym` or `sym` is an `edge_candidate` (tagged `ambiguous`); depth > 1 renders a tree, breadth-first, with a visited set and a 500-node cap; `--no-tests` drops sources with `is_test`.
- `callees <sym> [--depth N]`: outbound `calls`; ambiguous edges list candidate names inline.
- `refs <sym> [--kind type|instantiate|field]`: `references` edges in, filtered on `edge.ref_kind`.
- `impls <sym>`: `implements` edges in; `supertypes <sym>`: `implements` and `extends` edges out.
- `tests <sym> [--transitive]`: callers whose source `is_test`; `--transitive` walks callers up to depth 3 and reports the test with the path.
- `siblings <addr>`: per spec 9 rules (symbol: same interface implementations, else same container and kind; file: same basename in sibling directories plus same suffix in the same directory; directory: sibling directories with overlapping basenames).
- `deps <module> [--depth N] [--cross-repo]` and `dependents <module>`: `module_edge` traversal; module addressed by identity or repo-relative path.

- [ ] **Step 1: Failing tests** for each query against the fixture expectations from Task 18, plus the text rendering snapshot of `callers Service.Lookup --depth 2` (tree form) and of `tests MemStore.Put` (expects `TestMemStorePutGet`). Snapshot files under `internal/query/testdata/*.txt`, compared exactly (the header is reproducible because `CopyFixture` fixes branch `main` and the commit dates), updated with `-update`.
- [ ] **Step 2: Implement**; recursive CTE example for callers:

```sql
WITH RECURSIVE up(id, depth, path) AS (
  SELECT ?, 0, ',' || ? || ','
  UNION ALL
  SELECT e.src_symbol_id, up.depth + 1, up.path || e.src_symbol_id || ','
  FROM edge e JOIN up ON e.dst_symbol_id = up.id
  WHERE e.kind = 'calls' AND up.depth < ? AND instr(up.path, ',' || e.src_symbol_id || ',') = 0
)
SELECT DISTINCT id, depth FROM up WHERE depth > 0 LIMIT 500;
```

- [ ] **Step 3: PASS, lint, commit** `feat(query): callers, callees, refs, impls, supertypes, tests, siblings, deps, dependents`

### Task 20: End-to-end CLI tests, README, coverage gate

**Files:**
- Create: `cmd/codedoc/e2e_test.go`, `README.md`
- Modify: `Makefile` (coverage threshold)

- [ ] **Step 1: Write e2e tests** that build the command in-process (`newRootCmd` with `CODEDOC_CONFIG` and `CODEDOC_DATA` pointed at temp copies), run `index`, then run each query command once and compare against snapshots in `cmd/codedoc/testdata/`. Include: exit code 2 for `symbol Nope`, exit code 3 for `symbol Get` with candidates printed, `--format json` parses, `--budget 300` truncates with the "more" line, `--out` writes the full file and prints the summary line, and the `fresh:no` header after touching 201 files in a temp copy with `MaxFiles` 200 (set via `CODEDOC_FRESH_MAX_FILES` env for the test).
- [ ] **Step 2: Add the coverage gate to `make test`**: fail when total coverage under `internal/` is below 90% (`go tool cover -func | awk` check).
- [ ] **Step 3: Write `README.md`**: what codedoc is (two paragraphs), install (`make build`, dependencies: gcc, libsqlite3-dev), configuration example from spec 14 restricted to what exists, the query reference for the commands shipped by this plan with one example each, address forms, exit codes, and a "Status" section listing what later plans add (languages, config keys, docs, search, MCP, daemon).
- [ ] **Step 4: `make test && make lint`, commit** `feat: end-to-end cli tests, coverage gate, readme`

---

## Later plans (not part of this plan)

| Plan | Spec steps | Scope |
|---|---|---|
| 2 | 4, 5 | Python and Java extractors with goldens; config extractors (YAML, JSON, XML, properties, .env, HCL, shell, template `${X}` references), code-side config candidates, `config` query |
| 3 | 6, 7 | Doc model and staleness with blame seeding; `doc set` writers per language and module; `doc check`; `gaps` ranking; `changes`, `history`, `at`, `overview` |
| 4 | 8, 9 | Embeddings (port `internal/embed` from an earlier project), `search`, `explain`, `flow`; MCP server with the eleven tools; plugin with the three skills and the optional SessionStart hook |
| 5 | 10 | Daemon: fsnotify watcher, HEAD polling, SCIP runner and merge, autodoc runner with `claude -p`, systemd unit, Unix socket status and session lock |

Each later plan is written when its step starts, against the code as it then exists.

## Implementation notes

This plan is historical; the spec describes the code as built. Main deviations:

- `reference_unresolved` became a view over a `reference` table that stores every extracted reference, with edges pointing back through `reference_id` (migration 4; spec 8).
- Stable keys are built and parsed in `internal/stablekey`; collisions use four hex digits of the path hash, eight when taken, then an ordinal; duplicates within one file get `~N` (spec 5.1).
- Built-in exclusions moved to `internal/exclude`, shared with the daemon: `testdata/` added, `build/`, `dist/`, `target/` only at the root or next to a manifest (spec 7.1).
- The `[nodoc]` tag became `[doc:missing]`/`[doc:stale]` (spec 9).
- Workspace-wide answers got a header naming the repos they draw on, and their locations a repo prefix (spec 9).
- Writers serialize on a file lock with `CODEDOC_LOCK_TIMEOUT`; freshness re-extracts at most 200 files (`CODEDOC_FRESH_MAX_FILES`) and records pending resolution in `meta` (spec 7.8).
- Errors under `--format json` end stdout with `{"error", "code"}`; `make install` stamps the version (spec 9, 13.1).
