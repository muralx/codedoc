# Architecture

This page describes how codedoc works today, for a developer joining the
project. It is a map, not a specification: the code is the reference,
and the [design history](history/README.md) explains why things are the
way they are. Section references such as "v1 7.3" point into the
[v1 design](history/specs/2026-10-09-codedoc-design.md); the addenda
are named [deps](history/specs/2026-10-10-codedoc-deps-design.md),
[documents](history/specs/2026-10-10-codedoc-documents-design.md) and
[efficiency](history/specs/2026-10-11-codedoc-efficiency-design.md).

## What it is

codedoc is one Go binary, `codedoc`, that indexes the working trees of a
set of repositories into SQLite and answers structural questions about
them: where a symbol is, who calls it, what a change touches, where a
config key is read, what a dependency exports. Coding agents reach it
through the CLI (via a shell tool) or through an MCP stdio server; both
run the same query code. An optional daemon keeps the index fresh in the
background and documents changed code unattended. Everything runs
locally: no network service, no API keys; the embedding model runs
in process through ONNX Runtime.

The index is derived. Documentation lives in the source as doc comments,
package docs and Markdown; codedoc reads it from there, tracks whether
it is stale, and writes it back into the source when asked. Deleting the
database loses nothing but time.

## Components

```
              Claude Code session
   skills, subagents ──Bash──▶ codedoc CLI ─┐
                     ──MCP───▶ codedoc mcp ─┤ (MCP runs the CLI in process)
                                            ▼
                                       query layer ──▶ output (budgeted text / JSON)
                                            │
                          freshness check ──┤ (asks the daemon, else stats files)
                                            ▼
           index pipeline ──────────▶ workspace SQLite db ◀── deps ──▶ deps.db (shared)
  discover → extract → store → resolve → doc states → embed
                ▲
   daemon: watcher, HEAD polling, catch-up, SCIP, autodoc, upkeep ──▶ claude -p
```

### Package map

```
cmd/codedoc              cobra CLI: one command per query, operational commands, exit codes
internal/config          config file (strict YAML), workspace and repo resolution from a directory
internal/exclude         built-in exclusions and workspace globs (discovery and watcher)
internal/parse           tree-sitter wrapper, grammar registry, language detection
internal/extract         record types every extractor produces
internal/extract/...     pure extractors: golang, ts, python, java, cfg (config formats), doc (documents)
internal/extract/golden  golden JSON tests of the extractors over the fixture workspace
internal/stablekey       build and parse stable symbol keys
internal/resolve         module identities, imports, reference tiers, config reads
internal/index           discovery, incremental sync, doc states, freshness, embedding
internal/passage         splits long texts into the passages that are embedded
internal/deps            third-party packages: discovery, extraction into deps.db, usage
internal/store           SQLite schema and migrations, write lock, graph queries, FTS, vectors
internal/gitmeta         git CLI: branch, HEAD, dirty files, listing, blame, log -L, blobs at a ref
internal/docs            staleness model, module docs, gap ranking
internal/docs/write      doc writers per language, verification, run journal
internal/query           one function per query, address resolution, freshness before answering
internal/feedback        the local feedback file
internal/output          text and JSON rendering under a byte budget
internal/embed           ONNX nomic-embed-text embedder, test embedder, text composition
internal/scip            SCIP indexer runs, index.scip parsing, precise-edge merge
internal/mcpserver       MCP stdio server: tools mapped onto CLI arguments
internal/session         session locks held by MCP servers
internal/daemon          watcher, debounce, HEAD polling, socket, single-instance lock, log
internal/daemon/service  the daemon: catch-up, rounds, schedules, upkeep, config reload
internal/autodoc         unattended documentation runner (headless claude -p)
internal/upkeep          documentation of what a branch changed, run by the daemon
internal/xdg             the state directory under the XDG layout
internal/buildinfo       facts about the running build (version) shared with internal packages
internal/testutil        test helpers: fixture copies with git history, isolated environment
internal/toolchain       integration test proving SCIP indexers, git and ONNX Runtime are present
plugin/                  Claude Code plugin: skills, subagents, MCP registration, hook
.claude-plugin/          local marketplace listing the plugin
deploy/systemd/          user unit for the daemon
scripts/usage/           usage and replay measurements over Claude Code transcripts
```

Two rules hold the layering together. Extractors are pure: bytes in,
records out, no store, no file system. `resolve` and `docs` know the
store but not the CLI. The CLI and the MCP server share the query
layer, so every answer has one implementation.

## Concepts

- **Workspace**: a named set of repositories that may reference each
  other. Queries never cross workspaces. Each workspace has its own
  database.
- **Repo**: a working tree on disk. The index describes the tree as it
  is, uncommitted edits included, and records branch and commit.
- **Module**: a language unit with an identity used by imports (Go
  package, npm package or directory, Python module, Java package), or a
  plain directory. Modules nest through `parent_id`.
- **Symbol**: a named declaration with a span. Documents and their
  headings are symbols too (kinds `document` and `section`).
- **Edge**: a typed relation (`calls`, `references`, `implements`,
  `extends`) carrying a confidence tier. Module imports are module
  edges. Config relations are separate (`config_use`).
- **Config key**: a setting found in a config file, with the places
  that define it, reference it and read it from code.

## Data model

Each workspace has one SQLite file, `<data dir>/<workspace>.db`
(`$CODEDOC_DATA`, default `~/.local/share/codedoc`), opened in WAL mode.
The schema is a list of numbered migrations in
[`internal/store/schema.go`](../internal/store/schema.go), applied in
order at open (21 today); a database newer than the binary is refused.

Main tables, in a few lines:

- `workspace`, `repo` (path, branch, commit, indexed time, SCIP commit),
  `repo_identity` (the module identities a repo provides, from its
  manifests);
- `module`, `file` (size, mtime, content hash, line count, extractor
  version, title), `symbol` (spans, signature, body and doc hashes, doc
  text and state);
- `reference` (every extracted reference, kept so it can be resolved
  again), `edge` and `edge_candidate` (ambiguous edges), `module_edge`,
  `import`;
- `config_key`, `config_use` (`defines`, `references`, `reads`);
- `doc_link` (relative links between documents), `section_fts` (the
  own text of each section);
- `symbol_fts`, `module_fts` (FTS5, external content, kept by triggers),
  `symbol_vec` (one row per passage), `module_vec`;
- `dep_use`, `dep_manifest` (the packages a repo's manifests resolve to);
- `upkeep_branch`, `upkeep_spend` (documentation upkeep state);
- `meta` (pending resolution, FTS rebuild flag, SCIP run records).

Dependencies live in one shared database, `<data dir>/deps.db`, an
ordinary store with the same schema in which every package version is a
repo row named by its package id (`npm/zod@3.23.8`,
`go/github.com/spf13/cobra@v1.9.1`). It also records which workspace
databases use each version (`dep_user`), which is what `deps prune`
keeps. What a workspace uses and the edges from its code into packages
stay in the workspace database.

Writers (index runs, freshness updates, the daemon, `doc set`)
serialize on an flock of `<db>.lock`, waiting at most
`CODEDOC_LOCK_TIMEOUT` (60 s). Readers never lock. `deps.db` has its own
lock, held only while one package is written or pruned. Vector writes
take no lock (below).

## Indexing pipeline

`internal/index` runs the same pipeline for `codedoc index`, for the
freshness check before a query, and for the daemon's rounds.

1. **Discovery.** Files come from `git ls-files --cached --others
   --exclude-standard` (a directory that is not a checkout is walked).
   Built-in exclusions drop `vendor/`, `node_modules/`, `.git/`,
   `testdata/`, build output directories next to a manifest, and
   dependency lock files; workspace `exclude` globs and a repo's
   `languages` narrow further. Files over 2 MB and binary files are
   skipped; generated files are indexed but flagged. Rules: v1 7.1.
2. **Extraction.** Each file is parsed with tree-sitter and handed to
   the extractor of its language: Go, TypeScript/TSX/JavaScript, Python,
   Java; config formats (YAML, JSON, XML, properties, env, HCL, shell,
   templates) through native parsers; documents (Markdown, reStructured
   Text, AsciiDoc, text) through the document parser. Extractors return
   symbols, references (with the receiver's type where the declaration
   makes it evident), imports, docs, and config key definitions and
   reads. Extraction runs on a worker pool. Rules: v1 7.2, documents 2.
3. **Store.** Rows are written by stable key: a rewritten file updates
   the symbols whose key it still declares, inserts new ones and deletes
   vanished ones, so symbol ids, and edges into unchanged symbols,
   survive edits.
4. **Resolution.** References become edges in tiers, each stored as the
   edge's confidence: `precise` (SCIP), `local` (language scoping in the
   file or package, typed receivers, typed field chains), `import`
   (through the file's imports, across repos by module identity),
   `unique` (a qualified reference with exactly one candidate in the
   workspace), `ambiguous` (several candidates, all kept), else
   unresolved. A bare name never reaches `unique`; a reference through a
   variable of unknown type never becomes a confident guess. Resolution
   is scoped to what changed (re-extracted files, names added or
   removed, fields whose type changed) and recorded in meta
   `pending_resolution` inside the write transaction, so an interrupted
   run is finished by the next one. Rules: v1 7.3.
5. **Doc states** are computed from hashes (below).
6. **Embedding** of what changed (below).

A change to extraction semantics bumps the extractor version (`10`,
[`internal/index/version.go`](../internal/index/version.go)); a sync
re-extracts every file recorded with another version, without a schema
migration.

### Incremental updates and freshness

Every file row stores size, mtime and an xxhash64 of its content. A sync
stats every known file, hashes those whose size or mtime moved (and
racily clean ones, git's rule), runs discovery again for added and
deleted files, and re-extracts only what changed. A moved HEAD (a
commit, a branch switch, a rebase) is just the files it changed plus a
refresh of the repo's branch and commit and of per-file git metadata.

Without a daemon, every CLI or MCP query runs this check on the repos it
touches before answering. When at most 200 files changed
(`CODEDOC_FRESH_MAX_FILES`), it re-extracts them first; above that it
answers from the index, says `fresh:no` in the header and points at
`codedoc index`. A stale answer is still an answer, never a failure.
When a daemon runs, the query first asks it over the Unix socket; the
daemon answers fresh only for the repo directory and database file it
keeps current, from the same build and extractor version, with nothing
pending. Otherwise the query runs its own check. Rules: v1 7.8.

## Stable keys and addresses

Every symbol has a stable key that survives line moves and reindexing
([`internal/stablekey`](../internal/stablekey)):

```
<repo>:<lang>:<module identity>:<qualified name>[#<signature hash>][#<path hash>]
go-lib:go:example.com/lib/store:MemStore.Get
web:ts:@acme/domain:src/records.ts/Environment.health
docs:doc:docs/spec.md:6. Delivery > 6.2 Retries
npm/zod@3.23.8:ts:zod:ZodType.parse
```

The language segment is the family (`go`, `ts`, `py`, `java`, `doc`).
Java methods always carry a signature hash; collisions within a module
add a path hash; duplicates within a file get `~2`, `~3`. Agents rarely
type keys: an address may also be `repo:path#name`, `path#name`,
`path:LINE`, a bare name, or Go style `pkg.Name`. An address that names
several symbols is answered for the best one with the others named in
one line; one that names nothing is answered with the nearest
candidates. `--strict` turns both into errors. Rules: v1 5.1,
efficiency 4.1.

## Documentation and staleness

Per symbol the index keeps `doc_hash`, `body_hash` and `doc_body_hash`
(the body hash seen when the doc last changed). The state is `missing`
without a doc, `stale` when the body changed since the doc did, else
`ok`. The body hash ignores whitespace outside literals, so reformatting
does not count; a container hashes its own signature and its members'
declarations, not their bodies or docs. A symbol seen for the first time
is seeded from `git blame`: a doc older than the newest line of its body
starts `stale`. Modules are `ok` or `missing`. Rules: v1 7.5.

`doc set` writes a doc into the source in the language's own form (Go
comment, JSDoc, docstring, Javadoc; package docs in `doc.go`, a
README `## Purpose` section, `__init__.py`, `package-info.java`),
replacing the byte range the index knows, re-extracts the file to verify
the result, restores it on failure, and marks the doc `ok`. Under
`CODEDOC_RUN_ID` it journals each file's original bytes first, which is
what lets autodoc and upkeep undo a run. `gaps` ranks missing and stale
docs by visibility, fan-in, kind and recency, stale before missing.
Rules: v1 10 and 11.1.

## Search

Text search is SQLite FTS5 over symbol names, qualified names,
signatures and docs, over module paths, identities and docs, and over
section text. Semantic search uses nomic-embed-text-v1.5 run through
ONNX Runtime ([`internal/embed`](../internal/embed)), loaded only when a
process first embeds. Documented symbols, sections, multi-line
undocumented code and documented modules are embedded; a text longer
than 1,200 bytes is split into passages of whole lines
([`internal/passage`](../internal/passage)), one vector each, so a hit
can point at the lines that matched. Vectors are normalized float32
BLOBs scored by a brute-force dot product in Go with filters pushed into
SQL; no vector extension is needed at this scale.

Ranking ([`internal/query/rank.go`](../internal/query/rank.go)) merges
the text hits by how many query terms they hold (plus symbols whose
bodies use a name holding a term), merges the vector hits by cosine
with a floor below the best score, fuses the two lists with reciprocal
rank fusion, weighs test code down and keeps sections of one document
apart. Without a model, or without ONNX Runtime, search is text only
and says so. Rules: v1 7.7, efficiency 3.

Vector passes never take the write lock, since a model call can take
seconds: a vector is stored with the hash of the text it came from only
while its row exists, so a pass is idempotent and one computed from text
that changed meanwhile is redone by the next. `codedoc index` embeds the
workspace; a query's freshness check embeds what it re-extracted only
when the model is already loaded and the backlog is small; the daemon
keeps the model loaded and embeds everything it re-extracts, then the
dependencies.

## Dependencies

[`internal/deps`](../internal/deps) indexes the third-party packages a
workspace declares and imports, for npm (npm, pnpm and yarn layouts,
with `@types` packages supplying declarations) and Go modules (from the
module cache). Discovery reads every `package.json` and `go.mod` among
the indexed files and finds each package's installed directory the way
the toolchain does. A declared package no source file imports is listed
as `unused` and not indexed unless `deps.include` names it.

A package version is extracted once, public declarations and doc
comments plus its README, into `deps.db`; it is immutable and never
refreshed. A local, linked or patched source gets a `local-<hash>`
version and is re-extracted when its files change. Inside a package
only `extends` edges are resolved, so inherited members can be listed.
Workspace references that resolve into a package become edges whose
target is the dependency symbol's stable key (`edge.dst_dep_key`,
confidence `import`). A workspace sees only the versions its own
manifests resolve to and its code imports.

Queries reach dependencies with `--deps`, through `api` (a package's
surface), and when a workspace lookup finds nothing. Indexing missing
packages on a query's path is bounded to 2 s; the rest is reported as
`deps: N pending`. Dependency vectors are written by the daemon or by
`codedoc deps index --embed`, not by `codedoc index`. Rules: deps spec.

## The query layer

[`internal/query`](../internal/query) holds one function per question:
`overview`, `outline`, `ls`, `lines`, `find`, `symbol`, the graph
relations (`callers`, `callees`, `refs`, `impls`, `supertypes`,
`tests`, `siblings`, module `deps`/`dependents`), `config`, `search`,
`where`, `explain`, `flow`, `impact`, `how`, `api`, the history queries
(`changes`, `history`, `at`), and the documentation commands (`gaps`,
`doc set`, `doc check`). Each resolves its addresses, runs the freshness
check for the repos it touches, and returns a response of sections and
entries to [`internal/output`](../internal/output).

The composite queries answer what used to take several calls: `impact`
(callers, references, implementations, tests, mentions in documents,
config keys, export status under one budget), `how` (`explain` plus the
first levels of `flow`), `where` (the top locations for a
"where is X done" question with the lines that match), and `api`.
Rules: efficiency 5.

The CLI ([`cmd/codedoc`](../cmd/codedoc)) is a thin cobra layer over
these functions. The MCP server ([`internal/mcpserver`](../internal/mcpserver),
official Go SDK, stdio) exposes nineteen tools; each call is translated
to the arguments of the matching CLI command and run in process through
an executor, so the two surfaces give the same answers. The server
rewrites the "for more, run ..." commands in its answers as the
equivalent tool call. On start it takes a session lock on every repo of
its workspace, which the scheduled autodoc respects. Rules: v1 13.2,
efficiency 9.1.

Exit statuses: 0 answered (including stale answers, nearest candidates,
no hits), 1 nothing could be offered or a write failed, 2 usage, 3
ambiguous under `--strict`, 4 internal. Writes that succeed print
nothing.

## Output budgets

Every answer is rendered under a byte budget: 8,000 bytes by default on
the CLI (`--budget`, 0 for unlimited), 6,000 on MCP (3,000 for
`impact`, at most 60,000). Results are ranked and emitted until the
budget is reached; a cut section ends with the exact command (or tool
call) that prints the rest, and a body that does not fit is cut to the
lines that fit with the command for the remaining lines, never dropped
in silence. `--out FILE` writes the whole answer to a file and prints a
short summary. The header is at most one line, and only says what
changes a decision (`repo@commit` in a multi-repo workspace, `dirty`,
`fresh:no`). Entries are one line each, with source lines printed in
the Read tool's `N\tcode` layout. Text is the default; `--format json`
carries the same content. Rules: v1 9, efficiency 3.

## The daemon

`codedoc daemon` ([`internal/daemon`](../internal/daemon) for the
mechanics, [`internal/daemon/service`](../internal/daemon/service) for
the behaviour) runs in the foreground, typically as the systemd user
unit in `deploy/systemd/`. It:

- takes a single-instance lock on the data directory and listens on a
  Unix socket (`$XDG_RUNTIME_DIR/codedoc.sock`, else under the state
  directory) that answers `status` and the freshness question;
- catches up: brings every repo up to date at start, then watches repo
  roots with fsnotify (debounced 300 ms, at most 2 s after the first
  event) and polls `.git/HEAD` and refs every 2 s for commits and
  branch switches, indexing in rounds of at most 5,000 files;
- keeps the embedding model loaded and embeds what it re-extracts, then
  dependency vectors in the background;
- schedules SCIP runs per repo (`scip.every`; a repo never merged runs
  at start): [`internal/scip`](../internal/scip) runs `scip-go`,
  `scip-typescript`, `scip-python` or `scip-java`, and merges their
  occurrences as `precise` edges at the indexed commit, leaving files
  changed since then on their heuristic edges;
- schedules autodoc runs (`autodoc.every`, `at`), skipping a repo that is
  dirty or whose session lock is held;
- runs documentation upkeep where a workspace enables it;
- reloads the configuration file when it changes, and marks a repo
  degraded (never fresh) while it may miss changes, such as when it
  runs out of inotify watches.

The CLI and the MCP server never depend on the daemon; without it they
fall back to their own freshness check. Rules: v1 12.

### Autodoc and upkeep

[`internal/autodoc`](../internal/autodoc) documents gaps unattended. It
takes the top gaps, groups them by module, and for each group runs
`claude -p` headless from the repo root with only `Bash(codedoc:*)` and
`Read` allowed and a cost cap; the agent writes all docs of the group
with one `codedoc doc set --batch`. A guard compares HEAD, the
working-tree status and every doc hash before and after: a group that
touched anything outside its journal, or left a key not `ok`, is
restored from the journal. With `--commit`, exactly the journaled paths
are committed. Autodoc never pushes. Rules: v1 11.2.

[`internal/upkeep`](../internal/upkeep) is the daemon's second trigger
of the same runner. Once a repo with `upkeep.enabled` is on a branch,
clean and quiet (`quiet`, default 5 minutes), it documents the symbols
the branch's own commits changed whose docs are stale or missing, as one
held run committed as one commit (`docs: document N changed
symbol(s)`) or left in the tree. Any outside change during the run
cancels it and undoes it as a whole. Per-branch progress is kept in
`upkeep_branch`; spending is capped per run and per local day
(`upkeep_spend`). Rules: efficiency 7.

## The plugin

`plugin/` is a Claude Code plugin, listed by the local marketplace in
`.claude-plugin/marketplace.json`:

- `.mcp.json` registers the MCP server (`codedoc mcp`);
- three skills: `codedoc-lookup` (which command answers which need, and
  when to delegate), `codedoc-document` (write the doc in the same edit
  as the code), `codedoc-maintain` (operations: gaps, upkeep, autodoc,
  dependencies, SCIP, the daemon);
- two subagents on a small model: `codedoc-explorer` answers a question
  from the index and returns only the answer with `path:line` citations;
  `codedoc-documenter` documents given addresses with one batch write;
- `hooks/session-start.sh`, not registered by default, prints the first
  lines of `codedoc overview`.

A test (`cmd/codedoc/plugin_test.go`) fails on any `codedoc <command>`
in a skill or agent that the binary does not have, on agent front
matter missing a field, and on the size limits of the skills and agents.
Rules: v1 13.3, efficiency 8 and 9.

## Known limits

- **One index state per workspace.** A database describes each repo's
  working tree as it is now. There is no per-branch index: a branch
  switch re-extracts the files it changed, and `changes`, `history` and
  `at` read older states from git on demand.
- **Git worktrees are not indexed.** Repos are configured by path, and a
  linked worktree of a configured repository is a different path, so it
  belongs to no workspace: queries from it are refused, and with
  `--workspace` they answer about the main checkout.
- **Dependencies cover npm and Go only.** Python and Java packages are
  not indexed.
- **Resolution is heuristic below SCIP.** Edges carry their confidence;
  `unique` and `ambiguous` edges can be wrong, and some receiver chains
  (Python attributes, Java method results, index expressions) are not
  followed (v1 7.3).
- **Embedding needs ONNX Runtime and the model files**; without them
  search is text only.
- **Upkeep counts untracked files as a dirty tree** and waits until the
  tree is clean, and after a very large re-index the daemon may leave
  new symbols without vectors, so search falls back to keywords for
  them. Both are tracked in [TODO.md](TODO.md).
