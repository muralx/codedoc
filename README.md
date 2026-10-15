# codedoc

codedoc is a local code knowledge index for coding agents. It parses the
repositories of a workspace with tree-sitter (Go, TypeScript/JavaScript,
Python, Java, plus eight config formats) into one SQLite database per
workspace. It resolves references into a call and type graph that crosses
repositories, and tracks the documentation of every symbol and module
against the code it describes. Markdown, reStructuredText, AsciiDoc and
text documents are indexed too, one section per heading.

Agents ask it what they need instead of grepping and reading whole
files. Each need takes one call: show me this function, what is in this
file, where is this done, what breaks if I change this, how does this
work, what does this dependency offer, which files changed lately, what
does section 6.2 of the spec say. Every answer:

- is the minimum that answers the question: names, locations and the
  real lines of the file, under a byte budget;
- is never a dead end: a name that matches nothing gets the nearest
  candidates, a cut answer ends with the exact command for the rest;
- is checked for freshness first, so files changed since the last index
  are re-extracted before the query runs.

Writes print nothing when they succeed; their exit status is the result.

codedoc runs as a CLI, an MCP server and an optional daemon. It ships a
Claude Code plugin with three skills and two subagents. The daemon can
also keep the documentation of a branch up to date after each commit,
through headless `claude -p` runs outside the session.

## Features

- **Symbols, files and documents.** `symbol`, `outline`, `ls` and
  `lines` read code and documents by name, path or line; `find` and
  `search` look them up by name, keywords and, with a local embedding
  model, meaning. See the [CLI reference](docs/cli.md) and
  [Search and embeddings](docs/search.md).
- **A graph across repositories.** `callers`, `callees`, `refs`,
  `impls`, `tests` and `impact` follow calls and references between the
  repos of a workspace, each edge with its confidence tier; SCIP
  indexers make them precise where installed.
- **Config keys.** YAML, JSON, XML, properties, env, HCL, shell and
  template files are indexed as keys, and `config` connects them to the
  code that reads them.
- **Third-party packages.** The public API of the npm and Go packages
  the code imports is indexed once and shared, so `api` answers what a
  dependency offers. See [Dependencies](docs/dependencies.md).
- **Documentation tracking.** Every symbol's doc is `ok`, `stale` or
  `missing`; `gaps` ranks the debt, `doc set` writes docs in each
  language's form, and the daemon can document what a branch changed.
  See [Daemon and background work](docs/daemon.md).
- **Agents.** An MCP server with 19 tools and a Claude Code plugin. See
  [Agents](docs/agents.md).

## Prerequisites

| Need | For | Notes |
|---|---|---|
| Go 1.27+ with cgo and a C compiler (gcc or clang) | building | tree-sitter grammars and SQLite compile through cgo. SQLite is bundled, so you don't need `libsqlite3-dev` |
| git | everything | branch, commit, file listing, blame and history all come from the git CLI |
| ONNX Runtime 1.17.1 | semantic search (optional) | without it, search is keyword-only |
| Node.js and `scip-typescript` / `scip-python`; `scip-go`; `scip-java` | precise edges (optional) | without them, edges use the heuristic tiers |
| `claude` CLI | `codedoc autodoc` (optional) | unattended documentation runs |

## Install

```sh
git clone https://github.com/muralx/codedoc.git && cd codedoc
make install        # go install with the version stamped; lands in $(go env GOPATH)/bin (or $GOBIN)
codedoc version     # codedoc 10a64ad
```

To call `go install` without make, pass the build tag and, optionally, the
version:

```sh
CGO_ENABLED=1 go install -tags sqlite_fts5 \
  -ldflags "-X main.version=$(git describe --always --dirty)" ./cmd/codedoc
```

Two details matter:

- The `sqlite_fts5` tag is required. A binary built without it fails on
  the first index with a message that names the tag.
- Without `-ldflags`, `codedoc version` prints the version Go records
  (`v0.0.0-<date>-<commit>`, plus `+dirty` when there are local changes).
  The daemon uses this version to tell a client built from different code
  apart.

Other targets: `make build` (writes `./codedoc`), `make test`, `make lint`,
`make download-model`, and the Docker targets (see
[Development](docs/development.md#tests)).

## Quickstart

### Try it on the bundled fixture workspace

`testdata/ws` holds two workspaces of small repos: `fixture` (Go,
TypeScript, Python, Java, a config repo and a repo of documents) and
`fixture-deps` (two repos with third-party packages). Each repo needs
its own git history, so copy them out first:

```sh
cp -r testdata/ws /tmp/ws && cd /tmp/ws
for r in */; do (cd $r && git init -q -b main && git add -A && git commit -qm "fixture: initial"); done
export CODEDOC_CONFIG=/tmp/ws/config.yaml CODEDOC_DATA=/tmp/ws-data
cd go-lib
codedoc index --verbose
```

```
8 repos (go-lib@0a93839 +7)
== index (8)
go-lib  main@0a93839 files:+8 ~0 -0
go-app  main@f9911b0 files:+2 ~0 -0
ts-domain  main@970edb8 files:+5 ~0 -0
ts-app  main@6a9560e files:+2 ~0 -0
py-lib  main@ce7cd4d files:+5 ~0 -0
java-lib  main@b5cd276 files:+6 ~0 -0
cfg-repo  main@c93f4d0 files:+9 ~0 -0
doc-repo  main@c713ac5 files:+6 ~0 -0
== deps (8)
go-lib  indexed:0 pending:0 missing:0 failed:0 skipped:0 unused:0
go-app  indexed:0 pending:0 missing:0 failed:0 skipped:1 unused:0
ts-domain  indexed:0 pending:0 missing:0 failed:0 skipped:0 unused:0
ts-app  indexed:0 pending:0 missing:0 failed:0 skipped:1 unused:0
py-lib  indexed:0 pending:0 missing:0 failed:0 skipped:0 unused:0
java-lib  indexed:0 pending:0 missing:0 failed:0 skipped:0 unused:0
cfg-repo  indexed:0 pending:0 missing:0 failed:0 skipped:0 unused:0
doc-repo  indexed:0 pending:0 missing:0 failed:0 skipped:0 unused:0
```

`codedoc index` alone prints nothing: a write that succeeds is silent,
and `--verbose` prints what was done. (Commit hashes depend on your git
identity and clock.) Some first queries:

```
$ codedoc symbol MemStore.Get
go-lib@0a93839
MemStore.Get  store/store.go:26-32
25	// Get implements Store.
26	func (m *MemStore) Get(key string) (string, error) {
27		v, ok := m.data[key]
28		if !ok {
29			return "", ErrMissing
30		}
31		return v, nil
32	}

$ codedoc outline store/store.go
go-lib@0a93839
== store/store.go, 38 lines (6)
ErrMissing  :7  var ErrMissing = errors.New("missing")  ErrMissing is returned when a key has no value.
Store  :10-15  type Store interface  Store persists string values by key.  [+2 members]
MemStore  :18-20  type MemStore struct  MemStore is an in-memory Store.  [+1 member]
NewMemStore  :23  func NewMemStore() *MemStore  NewMemStore creates an empty MemStore.
MemStore.Get  :26-32  func (m *MemStore) Get(key string) (string, error)  Get implements Store.
MemStore.Put  :35-38  func (m *MemStore) Put(key, value string) error  Put implements Store.

$ codedoc impact Service.Lookup
go-lib@0a93839 go-app@f9911b0
Service.Lookup  go-lib:service/service.go:17-23  func (svc *Service) Lookup(key string) string  Lookup returns the value for key, or "" when missing.
== callers (1)
run  go-app:main.go:16  func run(svc *service.Service) string { return svc.Lookup("k") }
exported  yes (public member of Service)  used by go-app

$ codedoc config LIB_MODE
go-lib@0a93839 cfg-repo@c93f4d0
== defines LIB_MODE (2)
cfg-repo:.env:2  env  dev
cfg-repo:config/settings.json:2  json  prod
== references LIB_MODE (2)
cfg-repo:.env:4  env
cfg-repo:scripts/setup.sh:5  shell
== reads LIB_MODE (1)
Mode  go-lib:service/service.go:28  func Mode() string  Mode returns the library mode from the environment.
```

### Your own workspace

```sh
mkdir -p ~/.config/codedoc
cat > ~/.config/codedoc/config.yaml <<'EOF'
workspaces:
  work:
    repos:
      - path: ~/src/api
      - path: ~/src/web
EOF
cd ~/src/api
codedoc index        # first run: full extraction; later runs are incremental
codedoc overview     # repos, entry points, doc coverage, recent commits, module tree
```

After that, keep the index current in one of three ways: rerun
`codedoc index`, let each query refresh what changed (it does that on its
own), or run the [daemon](docs/daemon.md#daemon).

## Configuration

The config file is `~/.config/codedoc/config.yaml`. The workspace above
is all it needs; a few keys most setups add:

```yaml
embed:
  model_dir: ~/src/codedoc/models    # semantic search, see docs/search.md
workspaces:
  work:
    repos:
      - path: ~/src/api
      - path: ~/src/web
    exclude: ["**/generated/**"]     # doublestar globs, relative to each repo root
    upkeep:
      enabled: true                  # document what a branch changed, after the commit
```

Every key, the environment variables and the files codedoc writes are in
[Configuration](docs/configuration.md).

## Documentation

- [CLI reference](docs/cli.md): output conventions, addresses, every
  command and its flags, languages and config formats.
- [Configuration](docs/configuration.md): the config file, environment
  variables, the files codedoc reads and writes.
- [Search and embeddings](docs/search.md): how `search` and `where`
  rank, and how to enable the local embedding model.
- [Dependencies](docs/dependencies.md): the index of third-party
  packages and the `api` and `deps` commands.
- [Agents](docs/agents.md): the MCP server and the Claude Code plugin.
- [Daemon and background work](docs/daemon.md): the daemon,
  documentation upkeep, SCIP precise edges, autodoc.
- [Development](docs/development.md): tests, Docker, measuring usage.
- [Architecture](docs/architecture.md): packages and design.
- [docs/README.md](docs/README.md): the index of all of the above, the
  design history and the list of potential improvements.

## License

Apache License 2.0, see [LICENSE](LICENSE).
