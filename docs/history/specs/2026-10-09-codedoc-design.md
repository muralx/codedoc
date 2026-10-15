# codedoc: a code knowledge index for coding agents

Date: 2026-10-09
Status: implemented (v1), 2026-10-09. This document describes the code as
built; the plans under `docs/history/plans/` are historical.

## 1. Problem

Coding agents working in a large, multi-repository stack lose structural knowledge
between sessions and rebuild it with expensive primitives. Mining 133 Claude Code
sessions on this machine (about 21,000 tool calls) showed the agent averaging 42
lookup calls per session, most of them one of these shapes:

| Need | How the agent did it | Cost |
|---|---|---|
| Body of one symbol by name | `grep -n "func X" -A 40 file`, then `sed -n` | 2 to 3 hops, whole-file reads when unsure |
| Production callers of a symbol | `grep -rn X --include=*.go \| grep -v _test` | noisy, hand-filtered, unresolved names |
| Outline of a file or package | `grep "^export\|^func"`, `wc -l dir/*` | repeated at the start of most tasks |
| Where a config key is defined and read | grep across YAML, properties, env, Terraform, JSP, code | no tool covers it |
| Ground truth before writing | re-read a file already read | 692 files read twice per session, 160 four or more times |
| Session onboarding | `cat README CLAUDE.md`, list tree, `git log` | every session, every repo |
| Tests covering a symbol, precedent to copy, symbol history across branches | ad hoc grep and `git show ref:path` | slow, often incomplete |

The waste is the number of hops and the loss of what was learned. The agent is
already frugal per call (median read result 1.4 KB).

## 2. Goals

1. Answer the questions above in one call each, with exact line numbers and,
   where useful, the text itself, so the agent can act without a second read.
2. Be polyglot through pluggable per-language extractors behind one
   interface, delivered one language at a time. v1 ships Go,
   TypeScript/JavaScript, Python, and Java (the languages the session history
   shows, by file references: TS/JS 130k, Go 46k, Java 43k, Python 42k,
   Rust 171, C# 35), plus configuration and infrastructure files (YAML, JSON,
   XML, properties, .env, HCL, shell, and `${KEY}` references in JSP
   templates). Rust, C#, or Kotlin are one package each when needed.
3. Carry understanding, not just structure: documentation at method, class,
   module, and package level, written by agents, stored in the code itself,
   searchable and attached to the symbol graph.
4. Keep documentation alive: detect undocumented and stale items, rank them,
   and fill them in unattended runs that use the same skills as interactive sessions.
5. Be correct enough to trust: results verified fresh against the working tree
   at query time, with confidence tags on anything inferred.
6. Respect the agent's context window: every answer fits a byte budget by design.

## 3. Non-goals (v1)

- Service-boundary edges (HTTP routes to clients, queues). The edge model leaves room.
- Indexing third-party dependency sources (`node_modules`, `.m2`, module caches).
- A shared team server. Data is keyed so one can be added later.
- IDE integration, web UI.
- Languages beyond the four code languages and the config formats listed
  (Rust, C#, Kotlin, PHP are later additions behind the same interface).

## 4. Decisions

| Decision | Choice | Why |
|---|---|---|
| Implementation language | Go, single binary `codedoc` | Same as an earlier project; one static binary; CGO already required for SQLite and ONNX |
| Parsing | tree-sitter via `github.com/tree-sitter/go-tree-sitter` with per-grammar modules | Uniform syntax layer for all languages; incremental; spans for every node |
| Precise resolution | Optional SCIP import (scip-go, scip-typescript, scip-python, scip-java) | Reuses mature type-aware indexers; call hierarchy at IDE quality where the toolchain exists |
| Config formats | Native parsers (yaml.v3, hcl/v2, encoding/json and encoding/xml with offsets, line parsers for properties/.env), tree-sitter-bash for shell, a `${KEY}` scanner for JSP and other templates | Key paths and line numbers are easier from real parsers than from grammar trees |
| Storage | One SQLite file per workspace: tables, FTS5, embedding vectors as BLOBs | Zero infrastructure; recursive CTEs cover graph traversal at this scale |
| Embeddings | Local ONNX nomic-embed-text, same pipeline as an earlier project | No API cost, offline |
| Docs | Live in source as doc comments and package docs; index derives from them | One source of truth; humans and IDEs benefit; rebuildable index |
| Deployment | Local-first daemon plus standalone CLI; same core | Must see uncommitted work on the current branch |
| Unattended docs | Daemon drives `claude -p` with the documenting skill | Same skill in both modes; no second prompt system |
| Agent surface | CLI and MCP (official Go SDK) over the same query layer; budgeted output | CLI output through Bash benefits from Claude Code's large-output spill to disk; MCP is convenient for small structured answers |

The parser sits behind a small interface (`Parse(lang, bytes) -> Tree`, `Query`),
so the pure-Go runtime `odvcencio/gotreesitter` can replace the CGO binding later
without touching extractors.

## 5. Concepts

- **Workspace**: a named set of repositories that are allowed to reference each
  other. Hard isolation: queries never cross workspaces. One SQLite file each.
- **Repo**: a working tree on disk (a git checkout or worktree). The index
  describes the tree as it is, and records branch and commit. Two checkouts of
  the same repository on different branches are two repos.
- **Module**: a language-level unit of code with an identity used by imports:
  Go package, npm package or TS directory, Python package, Java package,
  Rust crate or module, C# namespace or project. Also a plain directory when
  no language unit applies. Modules nest. A file belongs to the module
  of its language at its directory (a Python file module: the file
  itself). The **size** of a module, in files and lines, is that of its
  own files plus that of every module nested under it by `parent_id`
  (the directories of an npm package, the modules and subpackages of a
  Python package), so an npm package whose sources all live in `src/`
  has the size of `src/`. A module below its directory that is not
  nested under it (another npm package, a Go package in a subdirectory)
  is a unit of its own and is not counted. Files of another language, or
  of none, are not counted.
- **File**: a source or config file inside a repo.
- **Symbol**: a named declaration with a span: function, method, class, struct,
  interface, trait, enum, type alias, constant, variable, field, test. Symbols
  nest (method in class, field in struct).
- **Edge**: a typed, directed relation between symbols (`calls`, `references`,
  `implements`, `extends`) or between modules (`imports`). Each edge carries a
  confidence. Relations to config keys are a separate relation (see Config key).
- **Config key**: a named setting found in a config file (`oauth.require_scope`,
  `FIX_GW_HOST`, `variable.region`). It has definitions (where a value is set)
  and uses (where code or templates read it).
- **Doc**: the documentation text attached to a symbol or module, extracted from
  the source, with a staleness state.

### 5.1 Symbol identity

Every symbol has a **stable key** that survives line moves and reindexing:

```
<repo>:<lang>:<module identity>:<qualified name>[#<signature hash>]
auth:go:github.com/acme/auth/internal/services:TokenService.Issue
domain:ts:@acme/domain:records.ts/Environment.health
platform:java:com.acme.ptp.services:AccountDTOServiceImpl.getById#7f3a
```

Parsing: split off an optional `#<hash>` suffix, then split the rest on the
first three colons; the remainder is the qualified name. Module identities
never contain a colon: Maven identities are written `group/artifact` in keys.
Module identity and qualified name per language:

| Language | Module identity | Qualified name |
|---|---|---|
| Go | package import path | `Name` or `Receiver.Name` |
| TypeScript/JavaScript | npm package name; when no `package.json` exists, the repo-relative directory path (the plain-directory fallback of section 5) | path of the file relative to the package root, a slash, then the dotted name: `src/records.ts/Environment.health` |
| Python | dotted module path of the file (`pkg.sub.mod`), rooted at the nearest `pyproject.toml`/`setup.py` directory or its `src/` child; every `.py` file gets a module row nested under its package's row | dotted name inside the file |
| Java | package name | `Class.member`, nested classes joined with dots |

The repo name is the first segment, so two checkouts of the same repository in
one workspace (two repo rows) never share a key. Repo names must be unique
within a workspace; config load fails when two repos resolve to the same name
and the user has to set `name` explicitly.

The language segment of a key is the language family: `go`, `ts`
(TypeScript, TSX, and JavaScript together), `py`, `java`; a file row keeps
the precise parser language. Java methods and constructors (constructors
are named `<init>`) always carry a signature hash, the first four hex
digits of the hash of the normalized parameter type list; TypeScript
overload signatures collapse into the implementing symbol and get none.
Disambiguation, in order:

- a qualified name declared twice in one file (same signature hash) gets
  `~2`, `~3` in source order;
- a collision inside a module (Go build-tagged files declaring the same
  function; Java main and test sources of one package) appends to every
  colliding symbol the first four hex digits of the hash of its file path
  (eight when four are taken, then an ordinal `-N`), after the signature
  hash: `Q#<signature hash>#<path hash>`. The first collision changes the
  key of the earlier symbol, which is accepted; when the collision goes
  away the lone symbol gets its plain key back. The signature hash is part
  of the key's base, so a field `X.m` and a method `X.m#7f3a` never
  collide. Parsing splits off both suffixes.

Agents address symbols by stable key, by `repo:path#qualified.name`, by
`path#qualified.name` relative to the current repo, or by bare name. An
address that names several symbols is answered for the best-ranked one,
the others named in one line, and one that names none with the nearest
candidates (efficiency addendum, section 4.1); `--strict` makes both
errors, the ambiguous one with a candidate list. A bare name that matches
no qualified name is tried Go style, `pkg.Name`: the last segment of the
module identity, then the qualified name (`store.NewMemStore`). The address parser
disambiguates in this order: a string with at least three colons whose second
segment is a known language name is a stable key (hash suffix or not); else a
string containing `#` is a path form (`repo:path#name` has at most one colon
before the `#`); anything else is a bare name (so Rust's `Type::method` is a
bare name). All outputs print the stable key so the agent can pass it back.

## 6. Architecture

```
                      ┌──────────────────────────────────────────────┐
  Claude Code         │ codedoc binary                               │
  ┌────────────┐      │                                              │
  │ skills     │─Bash─┼─▶ cli ─────┐                                 │
  │ (lookup,   │      │            ▼                                 │
  │ document,  │─MCP──┼─▶ mcp ──▶ query engine ──▶ store (SQLite)    │
  │ maintain)  │      │            ▲    ▲             ▲              │
  └────────────┘      │            │    │             │              │
                      │   freshness check        indexer pipeline    │
                      │   (stat + hash)          ┌────────────────┐  │
                      │                          │ discover       │  │
  claude -p ◀─────────┼── autodoc runner ◀───────│ extract (ts)   │  │
  (headless)          │         ▲                │ resolve refs   │  │
                      │         │                │ modules/config │  │
                      │      daemon ─────────────│ docs/staleness │  │
                      │   (fsnotify, git HEAD,   │ git metadata   │  │
                      │    scheduler, SCIP)      │ embeddings     │  │
                      │                          └────────────────┘  │
                      └──────────────────────────────────────────────┘
```

Packages (Go):

| Package | Responsibility | Depends on |
|---|---|---|
| `internal/config` | config file (strict YAML), workspace and repo resolution from a directory, `languages`, `scip`, `autodoc`, `upkeep`, `embed` sections, durations (`6h`, `2d`, `1w`) | exclude |
| `internal/exclude` | built-in directory exclusions and workspace globs, shared by discovery and the watcher | doublestar |
| `internal/xdg`, `internal/buildinfo` | the state directory `${XDG_STATE_HOME:-~/.local/state}/codedoc`; facts about the running build (version) | — |
| `internal/parse` | tree-sitter wrapper, grammar registry, language by extension, language families | tree-sitter |
| `internal/extract` | record types (symbol, reference, import, doc), container hashing, `~N` suffixes, config candidates | parse |
| `internal/extract/{golang,ts,python,java}` | one pure extractor per language | extract, parse |
| `internal/extract/cfg` | config key extractors per format, value previews and masking | extract, parse, yaml.v3, hcl/v2 |
| `internal/extract/golden` | golden JSON tests of the extractors over the fixtures | extract, testutil |
| `internal/stablekey` | build and parse stable keys | — |
| `internal/resolve` | manifests to repo identities, module identities, import resolution, reference tiers, config reads, Go implements | parse, store |
| `internal/store` | SQLite schema, migrations, writes, write lock, graph queries, FTS, vectors | go-sqlite3 |
| `internal/gitmeta` | git CLI: branch, HEAD, dirty files, file listing, last commit per file, blame, `log -L`, blobs at a ref, a branch's own commits and the files they touched | git CLI |
| `internal/index` | discovery, extraction, incremental sync, doc state seeding, pending resolution, freshness, embedding of changed rows | config, docs, embed, extract/*, gitmeta, resolve, store |
| `internal/docs` | staleness model, module doc discovery, gap loading and ranking | extract, gitmeta, store |
| `internal/docs/write` | doc writers per language, verification by re-extraction, restore, per-run journal | extract/*, parse, xdg |
| `internal/output` | text and JSON rendering under a budget: header, sections, offset, `--out` | — |
| `internal/query` | one function per query, address resolution, freshness check before answering | config, docs, docs/write, embed, gitmeta, index, output, resolve, store |
| `internal/embed` | ONNX nomic-embed-text embedder (loaded lazily), deterministic test embedder, text composition | onnxruntime_go |
| `internal/scip` | SCIP indexer runs, `index.scip` parsing, precise edge merge | gitmeta, index, resolve, store, scip bindings |
| `internal/mcpserver` | MCP stdio server: nineteen tools mapped onto CLI arguments (section 13.2) | query, session, go-sdk |
| `internal/session` | session locks held by MCP servers, which the scheduled autodoc respects (upkeep does not); `CODEDOC_HEADLESS` | — |
| `internal/daemon` | watcher, debounce, HEAD polling, degraded state, socket server and client, single-instance lock, log | fsnotify, exclude, xdg |
| `internal/daemon/service` | the daemon: catch-up, incremental rounds, embedding, SCIP and autodoc schedules, the upkeep trigger (quiet time, interruption of a run), config reload | autodoc, daemon, docs/write, index, query, scip, session, store, upkeep |
| `internal/autodoc` | unattended runner: gap groups, `claude -p`, guard, journal restore, commit; held runs that are committed, kept or reverted whole | docs/write, gitmeta, index, query, session, store |
| `internal/upkeep` | documentation upkeep (efficiency addendum section 7): readiness of a repo, the candidates of a branch, one run with its commit, per-branch state and daily spend | autodoc, config, embed, gitmeta, query, store |
| `internal/testutil`, `internal/toolchain` | test helpers (fixture copies with git history, isolated environment); an integration test proving the SCIP indexers, git and ONNX Runtime are present | extract |
| `cmd/codedoc` | cobra CLI | everything |

Rule: `extract` packages are pure (bytes in, records out) and know nothing about
the store. `resolve` and `docs` know the store but not the CLI. This keeps every
unit testable with fixtures.

## 7. Indexing pipeline

### 7.1 Discovery

Files come from `git ls-files --cached --others --exclude-standard` (a
directory that is not a git checkout is walked). Built-in exclusions:
`vendor/`, `node_modules/`, `.git/` and `testdata/` at any depth;
`build/`, `dist/` and `target/` at the repo root or in a directory holding
a build manifest (`go.mod`, `package.json`, `pom.xml`, `build.gradle[.kts]`,
`pyproject.toml`, `setup.py`, `Cargo.toml`), elsewhere they are ordinary
packages; and dependency lock files by name at any depth
(`pnpm-lock.yaml`, `package-lock.json`, `npm-shrinkwrap.json`,
`yarn.lock`, `bun.lockb`, `bun.lock`, `go.sum`, `go.work.sum`,
`poetry.lock`, `uv.lock`, `Pipfile.lock`, `Cargo.lock`, `composer.lock`,
`Gemfile.lock`, `gradle.lockfile`): they yield no file row, symbol or
config key, and the rows an older index holds for them are deleted by
the next index run or freshness check, like those of any file no longer
listed. Workspace `exclude` globs are doublestar patterns relative to
each repo root, validated at config load; a glob covering everything below
a directory skips the directory. A repo's `languages` keeps only those
languages and formats; files of no language (manifests, READMEs) are
always read. Files above 2 MB and binary files (a NUL in the first 8 KB)
are skipped. Generated files are indexed but flagged (`Code generated`,
`<auto-generated>`, `@generated`, `DO NOT EDIT` in the first 2 KB) so docs
tooling skips them.

Language by extension: Go `.go`; TypeScript `.ts .mts .cts`, TSX `.tsx`,
JavaScript `.js .jsx .mjs .cjs` (TSX grammar); Python `.py`; Java `.java`;
config formats in 7.2. An extension-less file whose shebang runs `sh` or
`bash` is a shell config file; other scripts without an extension are not
indexed. Each file row stores size, mtime, line count and a content hash
(xxhash64) used for incremental updates.

### 7.2 Extraction (per file, pure)

Each language extractor returns:

- symbols: kind, name, qualified name, container, span (start/end line and byte),
  signature span, signature text, visibility, body hash, flags (test, generated, exported)
- references: at a span, a callee or type name as written, receiver text if any,
  the enclosing symbol, and the reference kind (`call`, `type`, `instantiate`,
  `field`, `extend`, `implement`); for Go and TS/JS also the type of the
  local variable the receiver starts with, where its declaration makes it
  evident or declared types lead to it (7.3, local variables and typed
  receiver chains)
- imports: raw import path or name, alias, span
- docs: the doc text attached to each symbol, its span, and the module doc if
  the file carries it
- tests: which symbols are tests and, where syntactically visible, what they target

Symbol kinds per language (summary; the extractor files own the full table):

| Language | Declarations | Call sites | Doc form | Package doc | Test detection |
|---|---|---|---|---|---|
| Go | func, method, type (struct, interface, alias), const, var, field | call_expression, method call with selector | comment block above | package comment, `doc.go` | `_test.go` and `func Test` |
| TypeScript/JS | function, class, method, interface, type alias, enum, const/let at top level, exported arrow functions | call_expression, new_expression | `/** */` above | `README.md` in dir, `index.ts` header comment | `*.test.ts`, `*.spec.ts`, `__tests__/` |
| Python | def, async def, class, module-level assignments | call | docstring | `__init__.py` docstring | `test_*.py`, `*_test.py`, `tests/` |
| Java | class, interface, enum, record, method, constructor, field | method_invocation, object_creation | Javadoc | `package-info.java` | `src/test`, `@Test` |

Language details: Java constructors are methods named `<init>`, and Java
methods and constructors always carry the signature hash
(`MemStore.<init>#a1b2`); a record component yields a private field and a
public accessor `x()` (unless the body declares it), and enum constants
are `const` symbols. In Python, `self.<name> = ...` in `__init__` (the
first parameter, whatever its name) declares a field of the class. In
TypeScript, every `it(...)` or `test(...)` call with a literal title (also
`.only`, `.skip`, `.each(t)(...)`) is a test function named `it:<title>`
or `test:<title>`, prefixed with its `describe:<title>/` path only when
the callee and title repeat within the file. In Go, an undocumented member
of a documented `const`/`var`/`type ( ... )` group carries the group's doc
(7.5). Constants with a string initializer (Go, TS and Python constants,
Java final fields) record that value (`symbol.value`).

Config extractors return keys with line, value preview, and references to
other keys:

| Format | Files | Keys | References |
|---|---|---|---|
| YAML, JSON | `.yaml .yml`; `.json` except `package.json`, `package-lock.json`, `tsconfig*`, `*.lock.json` | dotted path of mapping keys, list items by index | `${X}` in values |
| XML | `.xml` | element path, attributes as `path@attr`; Maven `<project><properties>` children also by bare name | `${X}` |
| properties, env | `.properties`; `.env`, `.env.*` | each key | `${X}`, `$X` (env) |
| HCL | `.tf .hcl .tfvars` | `var.x`, `local.x`, top-level and resource attributes | `var.x`, `local.x` traversals |
| shell | `.sh .bash`, extension-less sh/bash scripts | `X=` and `export X=` | `$X`, `${X}` |
| template | `.jsp .tpl .tmpl .j2 .template` | — | `${X}`, `${X:default}` |

Value previews are one trimmed line of at most 80 bytes; a key naming a
secret (password, secret, token, credentials, an api, private, access,
signing or encryption key) is previewed as `***`.

Code-side config reads are extracted by the language extractors as references
of kind `config`, without any knowledge of which keys exist (extractors stay
pure). Two sources: recognized accessor patterns (Go `os.Getenv`,
`os.LookupEnv`, `viper.Get*`; TS `process.env.X`, `process.env["X"]`;
Python `os.getenv`, `os.environ[...]`, `os.environ.get`/`setdefault`; Java
`System.getenv`, `System.getProperty`, Spring `env.getProperty`,
`environment.getProperty`, `getEnvironment().getProperty`,
`@Value("${x}")`),
and string literals in call arguments whose text looks like a key: env style
`^[A-Z][A-Z0-9_]{2,}$` or dotted `^[a-z][a-z0-9_-]*(\.[a-z0-9_-]+)+$`. The
`resolve` stage matches candidates against `config_key`; matches become
`config_use` rows of kind `reads`, and unmatched candidates are stored in
`reference_unresolved` with kind `config`, so that when a key is defined later
the files that read it are re-resolved through the same name index as symbols.
An accessor whose key argument is a name (`os.Getenv(KeyConst)`, `pkg.KEY`,
`Class.KEY`, `this.KEY`) reads the key that the constant's value spells:
the name is resolved like a field reference, and the resolver records the
key in `reference.config_key`; a name that resolves to no constant, or to
constants with different values, matches nothing.

### 7.3 Resolution (needs the store)

References are resolved to symbols in tiers. Each resolved edge stores the tier
as its confidence:

| Tier | Rule | Confidence |
|---|---|---|
| `precise` | SCIP occurrence maps the reference to a definition | highest |
| `local` | the name resolves through language scoping without an import: the same file, the same package (Go, and Java for names other than calls), the enclosing class for `this`/`self`/`cls`, the receiver of the enclosing Go method, a field whose declared type the field signature gives (Go, TS/JS, Java; one or more hops, see typed receiver chains), a member a Go struct gets from a struct of its package it embeds, a local variable or parameter whose declaration names a type of its file or package (Go, TS/JS; see below), or a member a Java class inherits from a supertype in its package | high |
| `import` | the receiver or qualifier maps through the file's imports to a module in the workspace, and that module or one of its descendant modules (by `parent_id`) has exactly one definition with that name; also a local variable or parameter whose type an import of the file names, and a receiver chain that passes through a type declared in another file or package | high |
| `unique` | qualified references only (`x.Name` whose receiver codedoc cannot type and is not a variable of unknown type, see local variables): exactly one definition with that name and arity in the workspace | medium |
| `ambiguous` | several candidates, or the single candidate of a reference through a variable of unknown type; one `edge` row with `dst_symbol_id` null and one `edge_candidate` row per candidate; `callers` surfaces the edge under every candidate, marked ambiguous | low |
| `unresolved` | no candidate; reference kept for text search | — |

A bare name (no qualifier) never reaches the `unique` tier: it resolves only
through the scoping of its language, namely the file, the package (Go; Java
for names other than calls), dot imports (Go) and star imports (Python,
`from m import *`) at `import` confidence over the union of the imported
modules and their descendants, members inherited along resolved `extends`
edges (Java bare calls), wildcard imports (Java) and declared globals. A
name none of these declares is unresolved. A qualified reference takes no
same-file or same-package shortcut either: it resolves through what its
receiver names (an import, `this`/`self`/`cls`, the Go method receiver,
a typed field chain such as `svc.s.Get` with `s store.Store`), and only
when the receiver cannot be typed falls to `unique` and `ambiguous`. A Go
reference qualified by a package name alone (`pkg.Name`) names a
function, var, const or type of that package, never a method.

**Local variables.** The Go and TS/JS extractors record on a qualified
reference the type of the variable its receiver starts with
(`reference.recv_type`, schema migration 14, extractor version 8) where
the declaration makes it evident:

- Go: `var x T`, `var x *T`, `x := T{...}`, `x := &T{...}`, `x := new(T)`,
  typed parameters and named results (not the method receiver, which the
  signature gives), and `x := f(...)` or `x := pkg.F(...)`, recorded as
  the callee followed by `()`: resolution reads the first result from the
  signature of the one function of that name in the package (the index)
  or in the dependency (`deps.db`), and gives up when there is none or
  the result is no plain named type. Of several variables initialised by
  one call, the first gets its first result.
- TS/JS: `const|let|var x = new T(...)`, `x: T` on a declaration or a
  parameter (the constructed class wins over the annotation), a class
  property initialised with `new T(...)` and `this.x = new T(...)`,
  recorded for the class. A property with a type annotation is left to
  the field rule; the first declaration or assignment of a property
  decides. A type parameter in scope is not a type.

Variables are scoped lexically (extractor version 9). The extractor
keeps a stack of scopes while it walks a file in source order: the file
itself, then one per function, method, function literal or arrow
function, block, loop, `catch` clause, `if`/`switch` initializer and
`case` clause, popped on the way out. A lookup walks outward from the
innermost scope and sees what was declared above it, so a variable of
an inner block, a closure parameter, an `if x, err := f(); ...`
variable or a redeclaration inside `go func() {...}` hides the outer
one only inside its scope. Besides the typed declarations above:

- every declaration without an evident type still declares its name:
  `x := other()`, the second variable of a multi-assign, a range,
  type-switch, `select` or `catch` variable, an untyped or bare arrow
  parameter (`x => ...`), a `for...of`/`for...in` variable (declared
  before the loop body is visited), and every identifier a
  destructuring pattern binds (`const { a, b: c, ...rest } = v`,
  `({ x }) => ...`). Such a variable is recorded on the references it
  qualifies as `recv_type` `?`;
- Go: `x := y` copies the type of the variable `y`; `x := y.(T)` has
  type `T`; a callee that is itself a variable (a function value, or a
  method of a variable: `kit := m; kit.New()`) is never taken for a
  package function;
- TS/JS: a variable assigned an object of another class than the one
  recorded (`let z: A | B = new A(); z = new B()`) is untyped from that
  assignment on; a named import under another name (`import { A as B }`)
  is stored as `A=B` in `import.names` and binds `B` to the exported
  `A`; `const x = require('m')` is an import, not a variable;
- the file scope holds the module-level variables, declared before the
  walk so a function sees those written below it (`const store = new
  Store()` at the top of a TS file, Go `var x = T{}` / `var x T`). A Go
  package variable declared in another file of the package is found at
  resolution time through its symbol: the type its signature names
  (`var x T`, `x = T{...}`, `x = &T{...}`, `x = new(T)`) or the first
  result of the function it is initialised with (`x = pkg.New()`).

A variable hides an import, a package name, the receiver of the
enclosing method and a package variable of the same name. A reference
through a variable of unknown type (`?`), or of a type recorded but not
resolvable (the result of an unknown callee), never becomes a confident
guess: where the `unique` tier would find the one definition with its
name and arity, the edge is stored as `ambiguous` with that single
candidate (`callers` lists it, marked ambiguous); several candidates
are `ambiguous` and none `unresolved`, as before.
Resolution looks the type up from the reference's file as it does a
field type: through the imports of the file (`import` confidence, or
the dependency tier of the dependency addendum when the import names a
package outside the workspace), else in the package (Go) or the file
(TS; an alias `type A = B<...>` stands for `B`) at `local` confidence.
The tier is never `precise`. A builtin type (Go's predeclared types,
the TS standard library, an array, `string`), a type a Go function
declares in its body (no symbol holds its members), a type of a package
that is not indexed, and in TS a type the file neither imports nor
declares are outside the workspace: the reference stays unresolved
instead of falling to `unique`. A type that is found but does not
declare the member (inheritance, an embedded type the index cannot see
into) falls to `unique` and `ambiguous` as before. The result type of a
function or method is read when the reference is resolved; a later
change of that signature alone is picked up by the next full
resolution.

**Typed receiver chains** (extractor version 10, no schema change). A
receiver is followed through declared types, hop by hop, each hop read
from the signature of the member it names and looked up from the file
that declares that member:

- a field hop is the declared type of a struct field (Go), of a class
  field, an interface property or a property of a type alias whose
  target is an object type (TS; such an alias now has its property and
  method signatures as member symbols, like an interface), or a Java
  field. In TS the type is the annotation, else the class the
  initializer constructs (`store = new Store()`), and for a `get`
  accessor its result; `T | undefined`, `T | null` and `readonly T`
  stand for `T`. A constructor parameter property (`constructor(private
  store: Store)`) types `this.store` inside its class;
- in Go a member that a struct does not declare is looked for in the
  structs it embeds, as the language promotes it: the one member of
  that name at the shallowest depth (three at most). The search gives
  up when that depth has several, or when a depth it passed embeds a
  type the index cannot see into (a type of a package that is not
  indexed, an interface, a named non-struct type);
- hops chain: `options.client.register()`, `this.deps.stores.agents.get()`,
  `s.opts.Store.First.Touch()`. A call segment stands for the declared
  result of that method (`s.pick().Touch()`);
- a variable whose type follows from declared types carries a **type
  path** in `reference.recv_type`, a root and the steps taken from it:
  `Options#client` (the declared type of a field), `Store#get()` (the
  declared first result of a method), `Options#items@` (an element:
  the variable of `for (const x of xs)`, the second variable of `for
  _, x := range xs` over a slice, an array or a map), `Store#get()^`
  (awaited: `Promise<T>` gives `T`). Roots besides a type name: `this`
  (TS), `$name` for a Go name no scope of the file declares (the method
  receiver or a package variable), and a call `f()` (Go: 7.3 above; TS:
  the declared result of a function a named import of the file brings
  in, or one the file declares). The extractors write a path for: a
  destructured parameter or local with a typed source (`({ client }:
  Options)`, `const { stores } = deps`, nested patterns), `const x =
  a.b`, `const r = store.get(id)`, `const r = await store.get(id)`, a
  loop variable, and the Go forms `x := s.field`, `r, err :=
  s.store.Get(id)`;
- a hop into a type of a dependency reads the member from `deps.db`;
  the type it declares must be one of the same package. The reference
  then lands in the dependency tier.

The chain keeps the tier a typed local has: `local` while every type
lies in the file (TS) or package (Go) of the reference, `import` as
soon as one is reached through an import or declared elsewhere, and
never `precise`. A hop that cannot be read from a declaration leaves
the reference exactly where it was before the chain was tried (the
`unique` tier, or `ambiguous` through a variable of unknown type): an
unannotated field, `any`/`unknown`/`object`, a union of several types,
a function type, an inline object type, a type parameter of the member
or of the type that holds it, a member the type does not declare (TS
`extends` is not followed), a signature cut at its first line, a
method that declares no result. A hop that lands on a builtin or on a
type outside what is indexed leaves the reference unresolved, as a
variable of such a type does. Not followed: Python attributes (the
extractor records no annotation), Java method results, a field of a
class reached through `extends`, an index expression (`xs[0].m()`),
the variable of a channel range, an array destructuring, a package
variable of another Go package (`pkg.Var.M()` as the source of a
variable), and the default or namespace import of a TS function.

A field whose declared type changes, and a field added or removed,
re-resolves the references that go through its name: those whose
qualifier ends with it and those whose type path has it as a step.

Resolution runs per changed file and re-runs for files whose candidates changed
(tracked through a name index), so an edit never leaves stale edges.

Cross-repo resolution uses the **module identity map**: each repo declares the
identities it provides (Go module path from `go.mod`, the npm name from
`package.json`, the Python distribution name from `pyproject.toml` or
`setup.py`, Maven `groupId/artifactId` from `pom.xml` with the groupId
inherited from the parent, Gradle `rootProject.name` from
`settings.gradle[.kts]`; Rust and C# manifests come with those languages). An import that matches another
repo's identity resolves into that repo. This yields module-level `imports`
edges across repos and lets `import`-tier symbol resolution cross repos.
When two repos in a workspace provide the same identity (two checkouts of one
repository), `codedoc index` prints one warning per identity (`warning:
<lang> identity <id> is provided by repos A, B; imports from other repos
resolve to A`, repos in config order), an import from inside one of them
resolves to itself, and an import from any other repo resolves to the one
listed first in the workspace config.

### 7.4 SCIP import

`codedoc scip run` (or the daemon) first brings the repo up to date, then
runs the indexer of each language family the repo uses, in the repo root
with a 20 minute timeout: `scip-go`, `scip-typescript index` (with
`--infer-tsconfig` when there is no `tsconfig.json`), `scip-python index .`,
`scip-java index`; `CODEDOC_SCIP_GO`, `_TS`, `_PY`, `_JAVA` name other
binaries. A missing indexer is an outcome, not an error. The merge runs
under the write lock in one transaction, at the indexed HEAD
(`repo.scip_commit`); files changed since that commit (committed,
uncommitted or untracked) are skipped and keep their heuristic edges until
the next run.

- A definition occurrence maps its SCIP symbol (package version stripped)
  to our symbol of that file whose span contains the line and whose name
  is the identifier there, innermost on ties; the pairs are kept in
  `scip_symbol`.
- A reference occurrence takes the reference row at its line and column
  (the column read as bytes, code points or UTF-16 units), else a row at
  that line with that name and a fitting kind, else a row is added
  (`reference.added`). Its edges are replaced by one `precise` edge to the
  mapped symbol, in this repo or another repo of the workspace.
- Type implementation relations become `precise` `implements` edges unless
  an `extends` edge already links the pair.
- Revalidation: precise edges of trusted files that this run does not
  confirm are dropped, with the rows earlier merges added, and their
  references are resolved again by the heuristic tiers.
- Several repos run in two passes (map every repo's definitions, then
  merge each), so cross-repo edges do not depend on the order.
- Each run records `{at, failed, missing}` in meta `scip_run:<repo path>`;
  `status` shows `scip:<commit>`, `scip:missing scip-go`,
  `scip:failed(<date>)` until a run succeeds, or `scip:never`.

### 7.5 Docs and staleness

Doc text is extracted per symbol and per module. The index keeps, per
symbol, `doc_hash`, `body_hash`, and `doc_body_hash` (the body hash
observed when the doc last changed); state = `missing` without a doc,
`stale` if `body_hash != doc_body_hash`, else `ok`. Modules are `ok` or
`missing` (no package doc source).

- Body hash: the xxhash64 of the signature and body text with all
  whitespace removed except inside string and character literals (the
  literal content nodes of the syntax tree; code inside an f-string or
  template substitution is still normalized), so reformatting and
  whitespace-only edits of code and comments do not count, while editing
  a literal's spaces (`"a b"` to `"ab"`) and signature changes (a renamed
  parameter) do; a Python body starts after its docstring. A container (struct, class,
  interface, enum, record) hashes its own signature and the declaration
  text of each direct member inside its span, never member docs or bodies:
  documenting or editing a member leaves the container's doc `ok`, adding,
  removing or changing a member declaration makes it `stale` (Go methods
  are not members of their type).
- Seeding: a new symbol (no prior row with its stable key anywhere in the
  repo, so a moved symbol keeps its state) or one whose `doc_hash` changed
  is seeded from `git blame --porcelain -M -C` (lines moved in a commit keep
  the time of their origin): when the body's newest line is newer than the
  doc's newest line, the doc is `stale` with `doc_body_hash = ''` ("older
  than the body") until the doc changes; otherwise `doc_body_hash =
  body_hash`. Uncommitted lines are newer than any commit, so a doc just
  edited is current and a doc git brought back (checkout, revert, branch
  switch) is as stale as its commit. For a container only its header and
  member declaration lines count. Untracked files seed `ok`.
- `doc set` marks the doc `ok` even when its text is unchanged (the agent
  reviewed it against the current body): `doc_body_hash = body_hash`.
- An undocumented member of a documented Go declaration group (`// Kinds.`
  above `const ( ... )`, `var ( ... )` or `type ( ... )`) is `ok`.
- A change to extraction semantics (such as the body hash rule) bumps the
  extractor version (currently 10: type paths through declared field types and method results; 8 added the receiver types of references), which each file row records
  (`file.extracted_version`); a sync re-extracts every file of another
  version and re-seeds its states, without a schema migration, and a
  catch-up interrupted part way keeps what it finished.

### 7.6 Git metadata

Per repo: branch (`git symbolic-ref --short -q HEAD`), HEAD commit, dirty
files (`git status --porcelain -z --untracked-files=all`). Per file: last
commit and its time, loaded with one `git log --name-only` pass over
`<last indexed commit>..HEAD` (the whole history when that commit is not an
ancestor). Per-symbol history and symbol-at-ref are computed on demand with
`git log -L<start>,<end>:<path>` and `git cat-file blob <ref>:./<path>`
(one `cat-file --batch` process for `changes`), parsed through the
extractor, not stored. A duration (`12h`, `3d`, `2w`) resolves with
`git rev-list -1 --before=<cutoff> HEAD`. Every git command runs with
`--no-optional-locks`, so a status the daemon runs in the background
never holds the index lock a command of the user needs.

### 7.7 Embeddings and text search

FTS5 over symbol name, qualified name, signature, doc text, and module
path, identity and doc. Every documented symbol, every section, every
undocumented symbol of more than one line that is no field, constant or
test, and every documented module is embedded by nomic-embed-text-v1.5 (ONNX Runtime 1.17.1, loaded only when
a process first embeds; `search_document: ` and `search_query: `
prefixes). A symbol has one vector per passage of its text (efficiency
addendum, section 3, lists what is embedded and how it is split): a
documented symbol with a doc of at most 1,200 bytes one vector over
`path + container + signature + doc + first 10 lines of body`; a longer
doc, a section's own text and the body of an undocumented symbol one
vector per passage of about 800 bytes of whole lines; an undocumented
container one over its signature and member names. A module has one over
`identity + path + doc`. Vectors are stored normalized as little-endian
float32 BLOBs in `symbol_vec` (one row per passage, with the lines it
covers relative to its symbol) and `module_vec` next to the hash of the
text they came from (a symbol's `text_hash` is
`p1:doc_hash:body_hash`, a module's the xxhash of its text), and scored
by a brute-force dot product (cosine) in Go with the kind, language and
repo filters pushed into SQL: a symbol scores as its best passage and
is one row of the result with its three best passages; modules stream
through a bounded top-k heap. 100,000 vectors of 768 dimensions scan in
well under 300 ms, so no vector extension is needed.

`codedoc index` embeds every symbol and module whose text changed, in
batches of at least 32 texts; a freshness check embeds what it
re-extracted only when the model is already loaded and at most 64
symbols and modules (and 128 texts) are stale; `search` catches up at
most as many before answering, else names the backlog; the daemon keeps
the model loaded and embeds everything it re-extracts. `status` shows
`vectors:N/M`, counting symbols and modules, not passages. Vector passes
never take the write lock (a model call can take seconds): the vectors of
a symbol are stored with its text hash only while its row exists, so
passes are idempotent and one computed from text that changed meanwhile
is redone by the next pass.

Hybrid search ranks in four steps (`internal/query/rank.go`):

1. *Terms.* The query's words without stop words (`a`, `the`, `how`,
   `where`, ...; kept when it has no other), each cut to its stem
   (`checked` to `check`, `agents` to `agent`; never below four
   letters), matched as prefixes and joined with OR.
2. *One text list.* The BM25 top 50 of the symbol index, of the section
   texts and of the module index, and the symbols whose bodies use a
   name holding a term (the `reference` rows: calls, type uses; a term
   of three letters or more matches anywhere in the name, so `hub` finds
   a call of `spawnHubProcess`), are merged by coverage: the hits
   holding more of the terms first, then the better rank in their own
   list, then symbols before sections, modules and body uses. Hits
   holding fewer than half the terms of the best one are dropped, unless
   the vector list holds them too: with OR, an index returns every row
   holding any one term.
3. *One vector list.* The cosine top 50 of symbols (a symbol stands
   once, scored as its best passage) and of modules, of the workspace
   and with `--deps` of its dependencies, are merged by score, which one
   model makes comparable; hits more than 0.1 below the best are
   dropped, since the model puts unrelated text at 0.45 to 0.6 and a
   fixed depth would rank it. A hit's rank in this list is its distance
   from the best score in steps of 0.005, not its place: a clear lead
   counts for more than one place, and hits the model cannot tell apart
   share one.
4. *Fusion.* Reciprocal rank fusion (k = 60) of the two lists; a hit in
   test code scores 0.75 of its sum, since a test names in words what
   the code it tests does; ties put production code first, then the
   better best rank, symbols, the workspace. Then a section that would
   directly follow another section of the same document moves behind the
   next hit from elsewhere.

Fusing every index's list as an equal (the first design) let the first
row of a weak list, a module whose doc holds one of five query words,
stand beside the first row of a strong one; merging by what makes rows
comparable removes that, and the total of an answer counts what
matched instead of what five lists of fifty returned (measured on
a TypeScript monorepo used for trials: 132 hits before, 64 after, for "send a notification to the
client"; 88 and 12 for "presence grace period").

A hit lists the passages that matched (JSON `extra.passages`: `start`,
`end`, `score`): for a vector hit those of its embedded text; for a hit
found only by the names its body uses, the lines of those uses, with
score 0. Without a configured model, or when ONNX Runtime cannot load,
search is text only and says so.

### 7.8 Incremental update and freshness

The daemon watches repo roots with fsnotify, debounces events (300 ms of
quiet, at most 2 s after the first), re-extracts the changed files, then
re-resolves affected references. Branch switches are just many changed
files; the repo row's branch and commit are refreshed from `.git`.

Without a daemon, every CLI or MCP call performs a freshness check: run
discovery again for the repos touched by the query (new files found,
deleted ones removed), stat every known file, hash those whose mtime or
size changed (and racily clean ones), and re-extract them before answering
when at most 200 changed (`CODEDOC_FRESH_MAX_FILES`). Above that, answer
from the index, exit 0, and print `fresh:no` with a hint to run
`codedoc index`. A stale answer is still an answer, never a non-zero exit.
The check plans without a lock, then takes the write lock, plans again and
writes. When a daemon runs, the query first asks it over the socket with
`{workspace, repo, root, db, db_id (device, inode), version, extractor}`;
the daemon answers fresh only for the repo directory and database file it
keeps current, from the same build and extractor version, with nothing
pending, and the query then skips its stat pass. Anything else answers not
fresh and the query runs its own check.

Writers (index runs, freshness updates, the daemon, `doc set`) serialize
on an flock of `<db>.lock`, waiting at most `CODEDOC_LOCK_TIMEOUT` (60 s)
and then failing with the holder's pid and command line, which the holder
writes into the lock file. Readers never lock. The database is opened in
WAL mode with a 5 s busy timeout, foreign keys on and immediate
transactions.

A sync records the resolution it leaves to do in meta
`pending_resolution` inside its write transactions: the re-extracted files,
the touched modules' edges, the names added, removed or reshaped, fields
whose declared type changed, Go method-set modules, or everything (`full`)
when modules, identities or the configured repo order changed. A sync
spanning several transactions records `full` in its first one and narrows
it on finish; the transaction that finishes resolving clears it, so an
interrupted run is finished by the next freshness check or index run.
More than 5,000 changed files are written in rounds of 5,000, checking for
cancellation between rounds; only the last round resolves. Large syncs
drop the FTS triggers and rebuild the FTS tables (meta
`fts_rebuild_pending`), finished at the next open if interrupted.

## 8. Data model (SQLite)

Schema version 21, from the migrations in `internal/store/schema.go` (the
tables of the dependency, document and efficiency addenda are described
there):

```sql
meta(key PRIMARY KEY, value)   -- schema_version, pending_resolution, repo_order, fts_rebuild_pending, scip_run:<repo path>
workspace(id, name UNIQUE, created_at)
repo(id, workspace_id, name, path UNIQUE, vcs, branch, commit, indexed_at, scip_commit)  -- UNIQUE(workspace_id, name)
module(id, repo_id, path, lang, kind, identity, parent_id, doc_text, doc_source, doc_state)  -- UNIQUE(repo_id, lang, path)
file(id, repo_id, path, lang, size, mtime, hash, lines, generated, parse_errors,
     last_commit, last_commit_at, extracted_version)                     -- UNIQUE(repo_id, path)
symbol(id, file_id, module_id, kind, name, qualified_name, stable_key UNIQUE, container_id,
       start_line, end_line, start_byte, end_byte, sig_start_line, sig_end_line,
       signature, visibility, is_test, body_hash, doc_text, doc_hash, doc_body_hash,
       doc_state, doc_start_line, doc_end_line, value)   -- value: string value of a constant
reference(id, file_id, symbol_id, kind, name, qualifier, line, arity, candidates,
          col, added, config_key)  -- every extracted reference; symbol_id is the enclosing symbol;
                                   -- col: 0-based byte column (-1 unknown); added: row a SCIP merge added;
                                   -- config_key: key an accessor reads through a constant
edge(id, src_symbol_id, dst_symbol_id, kind, ref_kind, confidence, file_id, line, reference_id)
     -- dst null when ambiguous; ref_kind keeps the extractor's sub-kind; reference_id null for detected edges (Go implements)
edge_candidate(edge_id, symbol_id)                             -- candidates of ambiguous edges
module_edge(src_module_id, dst_module_id, kind, confidence)    -- PRIMARY KEY(src, dst, kind)
import(file_id, raw, alias, resolved_module_id, line, names)   -- names: JSON array of named imports, null otherwise
config_key(id, repo_id, key)                                   -- UNIQUE(repo_id, key)
config_use(key_id, file_id, symbol_id, line, kind, format, value_preview, reference_id)
     -- defines | reads | references; one row per occurrence; reads point at the reference they were matched from
repo_identity(repo_id, lang, identity, path)                   -- PRIMARY KEY(repo_id, lang, identity)
scip_symbol(stable_key PRIMARY KEY, scip_symbol)
symbol_vec(symbol_id, ord, from_doc, start_line, end_line, text_hash, vec)  -- PRIMARY KEY(symbol_id, ord):
     -- one row per passage; lines relative to the symbol's doc (from_doc) or to the symbol; little-endian float32 BLOB
module_vec(module_id PRIMARY KEY, text_hash, vec)
upkeep_branch(repo_id, branch, commit, ran_at)                 -- PRIMARY KEY(repo_id, branch): last documented commit
                                                               -- of a branch and when a run last finished there
upkeep_spend(day PRIMARY KEY, usd)                             -- what upkeep runs spent on a local day
symbol_fts(fts5, external content symbol: name, qualified_name, signature, doc_text)
module_fts(fts5, external content module: path, identity, doc_text)   -- both kept by insert/delete/update triggers
VIEW reference_unresolved(file_id, symbol_id, name, line, kind, candidates)
     -- references with no edge and no config_use pointing at them
```

Indexes: `module(identity)`, `module(parent_id)`;
`module_edge(dst_module_id)`; `symbol(name)`, `symbol(stable_key)` unique,
`symbol(file_id)`, `symbol(module_id, kind)`, `symbol(kind)`,
`symbol(container_id)`; `edge(dst_symbol_id, kind)`,
`edge(src_symbol_id, kind)`, `edge(reference_id)`, `edge(file_id)`;
`edge_candidate(symbol_id)`, `edge_candidate(edge_id)`; `import(file_id)`,
`import(resolved_module_id)`; `config_key(key)`; `config_use(file_id)`,
`config_use(reference_id)`, `config_use(key_id, kind)`,
`config_use(symbol_id)`; `reference(file_id)`, `reference(symbol_id)`,
`reference(name)`, `reference(config_key)` where not null. Unique
constraints cover `file(repo_id, path)` and the other natural keys. The
referencing column of every foreign key leads an index or a primary key
(migration 18 added the last ones), so a delete looks up the rows that
cascade with it; a test holds the schema to that.

Edge kinds stored in `edge` are `calls`, `references`, `implements`,
`extends`; `ref_kind` keeps the extractor's reference kind so `refs --kind`
filters on it. "Tests" is not a stored edge kind: the `tests` query selects
edges whose source symbol has `is_test`. Module-level `imports` edges live
in `module_edge`, and the identities a repo provides (7.3) in
`repo_identity`, one row per manifest.

Config relations live only in `config_use` (`defines`, `reads`,
`references`). A `config_key` row names a key once per repo for every key
a config file of the repo defines or references; each definition is a
`defines` use with its format and value preview, so a key defined in YAML
and in a properties file has one key row and two `defines` rows. Reads
match workspace-wide, only key rows with a `defines` use, one `reads` row
per matching key row; a key row left without `defines` and `references`
uses is deleted with its reads.

Every extracted reference is stored, and each edge resolved from it points
back through `reference_id`, so re-resolution picks rows: the references
of re-extracted files, every reference whose `name` is that of a symbol
the sync added, removed, or changed in kind or arity, and the references
through a Go or Java field whose declared type changed, after deleting
their edges. Symbol ids are stable: rewriting a file updates in place the
rows whose stable key it still declares, inserts new keys and deletes
vanished ones, so edges into unchanged keys survive and a body-only edit
re-resolves nothing outside its file. A reference inside no symbol never
yields an edge; one with more than 20 candidates gets none and records the
count in `candidates`. `reference_unresolved` is a view, so it never
disagrees with the edges; SCIP merging replaces edges per reference row.

Graph traversals (callers to depth N, flow trees) use recursive CTEs with a
visited set and a node cap. Vectors are described in 7.7.

## 9. Query API

All query commands (every command in the table below) share:

- `--workspace` (default: the workspace containing the current directory),
  `--repo` filter, `--format text|json` (text is the compact agent default),
  `--budget <bytes>` (default 8000; `0` means unlimited), `--out <file>`
  (write the full result to a file, unbudgeted, and print a summary of at
  most about 600 bytes ending `full result: FILE`), `--offset N` (skip the
  first N ranked entries of each section; when that leaves a tree answer
  starting below its root, the note names the skipped ancestors once:
  `callers of X continues under A > B`). Negative values are errors.
  `--strict`: an address that names nothing is an error (exit 1) and one
  that names several symbols another (exit 3, after the candidates),
  where a query otherwise answers for the nearest or the best candidate
  (efficiency addendum, section 4.1).
- Path arguments are contained. A command that takes a path (`lines`,
  `outline`, `ls`, `symbol path:LINE`, `explain`, `feedback --path`; the
  MCP tools run the same code) resolves it in one place, which cleans it,
  resolves its symbolic links and refuses it unless it lies in a repo of
  the workspace or in a package directory recorded for one of the
  workspace's dependency uses: `<path>: path is outside the repos of the
  workspace and the directories of its dependencies` (exit 1, also for
  `symbol path:LINE`, which under `--strict` is not found with that note;
  `feedback --path` records the report without file hashes). A path that climbs out of its
  repo with `..`, an absolute path elsewhere and a symbolic link inside a
  repo that points elsewhere are all refused; nothing of such a file is
  read.
- Budget handling: results are ranked, then emitted until the budget is
  reached; a cut section ends with the exact command to get more
  (`37 more: codedoc find 'Get*' --offset 20`). When not one entry of
  a section fits, continuing at the same offset would repeat the answer,
  so the line names the command of the section, else the command that
  ran with the budget that prints the answer whole (`11 more: codedoc
  find '*Store*' --budget 1600`). Sections after a cut one are
  summarized with the same command (`... N more sections (a, b, +k
  more): <command>`, shorter `... N more sections: <command>`), one
  section alone with its own. A budget too small for the first section
  still names it: its title, then that line; only when that does not
  fit either is the answer `... N more`. A note longer than what the
  budget leaves does not cost the answer its place: the entries come
  first, and the note keeps the whole sentences that fit (efficiency
  addendum, 3.1). Only a response rendered without a command (a
  library caller) reads as before (`rerun with --offset N`, `budget too
  small for one entry; raise --budget or use --out FILE`). The MCP
  server runs the CLI, so its answers have the command, and rewrites
  it as the tool call (efficiency addendum, 9.1). JSON carries the command in `continue` of the section,
  next to `next_offset`. A body is never
  dropped (efficiency addendum, section 3): the source of a symbol with
  members is answered with the outline of its members, and any other
  body (a function, a document section, the range of `codedoc lines`) is
  cut to the lines that fit and ends with the exact command for the
  rest, `2838 more lines: codedoc lines repo:docs/big.md 171-3008`;
  pasting each such command in turn prints the whole range once. When
  not one line fits, the entry line is followed by that command for the
  whole range (JSON: `body_omitted` with `continue`). `[body omitted:
  lines A-B; use --out]` is left for a body no command prints (a symbol
  at another revision, a diff). The
  renderer does the cutting, so text and JSON both stay within the
  budget.
- Header: at most one line, first, holding only what changes a decision
  (efficiency addendum, section 3). A fresh, clean answer in a workspace
  of one repo has none. In a workspace of several, an answer about one
  repo (the query covers one repo, or `--repo` names it and the answer
  draws on no other, or the answer draws on exactly one) prints
  `repo@commit`; otherwise it lists the repos the answer draws on (all
  of them when none) in config order: `go-app@8490d5a go-lib@f8fd258`;
  with more than three repos, their count and the one most entries come
  from: `4 repos (go-lib@f8fd258 +3)`. `dirty` follows when a repo has
  uncommitted changes and `fresh:no` when the index is behind, with
  `fresh:no(other)` when another repo it touched is stale (`overview`
  counts every repo it
  describes and names the current one, else the first: a repo of
  documents has no entry keyed by its name); stale repos are named:
  `fresh:no(go-lib)`. Locations in a workspace answer are prefixed with
  their repo: `go-lib:service/service.go:18`.
- Sections open with `== title (N)` (N counts entries `--offset`
  skipped, and the hits after the page a `search --limit` prints: the
  total is the same on every page). An entry is one line: the short
  qualified name (`AgentStore.register`; the stable key with `--keys`),
  `path:line` or a range, then what answers the question: signature and
  the first sentence of the doc, whole, for `find` and `outline`; the
  line of the call for call sites; nothing for `symbol`, whose numbered
  lines follow. Tags in brackets come last, two spaces apart; tree levels
  indent two spaces. A run of entries of one file prints the path once
  and `:lines` per entry. Bodies and fragments print as `N\tcode`, the
  Read tool's layout, so the agent can quote and edit from them.
- Tags: by default only those that change what the reader does: `[test]`,
  `[stale]`, `[dep name@version]`, `[+N members]`, `[cycle]`, the
  candidate count of an ambiguous edge (`[1 of 2]`, on a line that starts
  with `?` in a section `possibly ...`). `--detail` adds the rest:
  confidence (`[local]`, `[import]`, `[unique]`, `[precise]`), kind,
  container, module, fan-in, fan-out, doc state `[doc:ok|stale|missing]`,
  and with them the signature and doc a call site replaces. `gaps`,
  `changes`, `doc check`, `config`, `deps` and `dependents` always print
  their tags: they are the answer.
- JSON: `{header, sections: [{title, entries: [{key, name, loc, text, doc, tags,
  body, body_start, body_end, body_omitted, continue, depth, fragments, extra}],
  omitted, next_offset}], note}` (`continue` is the command for the rest
  of a body cut to the lines that fit; `body_end` is then the last line
  kept; `fragments` are `{start, end, text}` line ranges of the file in
  `loc`; titles carry stable keys); the header is `{repo, branch, commit, dirty,
  fresh}` or `{workspace, repos: [{name, branch, commit, dirty, fresh}],
  dirty, fresh, stale}`. A failing command also prints
  `{"error": "...", "code": N}` as the last line of stdout.

| Query | Input | Output | Replaces |
|---|---|---|---|
| `overview` | workspace or `--repo` | sections: repos (branch, commit, dirty, size), README.md and CLAUDE.md headings, entry points, doc coverage, the last 10 commits, the module tree to depth 2 with one-line docs and file and line counts (the module size of section 5, which includes nested modules; cut first by the budget). Entry points are: `main` functions and `package main` directories, `cmd/*` and `bin/*` directories, files named `main.*`, `index.*`, `app.*`, `server.*`, and symbols that call a known server start (`ListenAndServe`, `app.listen`, `uvicorn.run`, `SpringApplication.run`) | session onboarding reads |
| `outline` | file, directory, or module (`repo:path`, a path in the current repo, a module identity); `--doc` (the whole doc in place of its first sentence) | a file's symbols as `name  :start-end  signature  first doc sentence`, the kind before a signature that does not say it, members collapsed (`[+2 members]`), doc state tags with `--detail`; a directory's or module's files and subdirectories with symbol and line counts. A path the index does not have is answered for the only indexed path it may mean (`bin.ts` for `packages/hub/src/bin.ts`), or lists the paths it may mean; one that exists outside the index says why in one line, and nothing near is one line too (exit 1; efficiency addendum, section 4.1) | `grep ^func`, `wc -l` |
| `find` | name, qualified name or glob; `--kind`, `--lang` (family or precise language), `--doc` | matching symbols as `name  path:start-end  signature  first doc sentence`; a glob with `.` or `/` matches qualified names. With `--deps`, or when no workspace symbol matches, the dependency symbols follow, grouped by package version, their doc sentence with `--detail` or `--doc`. A name nothing matches, and words, are answered by `search` (efficiency addendum, section 4.1) | `grep -rn "func X"` |
| `symbol` | one symbol address, or several (answered together under the one budget, efficiency addendum 5.1); `--no-body`, `--doc-only`, `--context N` (N more lines around the source), `--body` (accepted, the default) | `name  path:start-end` and the numbered lines of the file from the doc comment to the end of the symbol. `--no-body`: location, signature and doc. `--detail`: kind, container, module, fan-in/out counts, doc state. Over the budget: the outline of its members, or the lines that fit and the command for the rest | `grep -A 40`, `sed -n` |
| `callers` / `callees` | symbol; `--depth N`, `--no-tests`, `--group-by file`, `-C N`/`-A N`/`-B N`/`--context symbol\|file` | each symbol once as `name  path:line  <the line of the call>`, the lines of every call between the two when there are several, with context lines on request; tree form for depth > 1; ambiguous edges after the certain ones under `possibly callers of X`, each `? name ... [1 of K]` | caller greps |
| `refs` | symbol; `--kind type\|instantiate\|field`, `--no-tests`, `--group-by file`, `-C`/`-A`/`-B`/`--context` | non-call references, each `name  path:line  <the line>`; the kind with `--detail` | usage greps |
| `impls` | type; `--no-tests`, `--group-by file`, `-C`/`-A`/`-B`/`--context`; `supertypes` (`--group-by file`) for the reverse | implementing or extending types with the line that declares it | manual search |
| `tests` | symbol or module; `--transitive`, `-C`/`-A`/`-B`/`--context` | tests that reference the symbol directly, with the line that does, or through callers up to depth 3 with `--transitive` (signature and `via` path) | "what covers this" |
| `siblings` | symbol, file, directory, or config key | things of the same kind in the same place. For a symbol: other implementations of the same interface, or other members of the same container with the same kind. For a file: files with the same basename in sibling directories (`providers/*/server.py`) and files in the same directory sharing its suffix (`*_store.go`). For a directory: sibling directories whose file sets overlap by basename. For a config key, addressed `config:KEY` (or by a bare name when no symbol has it; `config:` names a repo only when no key matches and a repo is called `config`): the other keys under the same parent path | precedent hunting |
| `deps` / `dependents` | module; `--depth`, `--cross-repo` (without it the walk stays in the module's repo) | module import graph | manual import tracing |
| `config` | key or glob; `--defines`, `--reads` | sections defines (file, format, value preview), references (config files and templates) and reads (code symbols) per key; a glob first lists the matching keys with counts. Reads of a key no indexed file defines are tagged `[undefined]`; a glob lists such a key only when an accessor reads it | cross-format greps |
| `search` | free text; `--kind` (a symbol kind, or `module`), `--lang`, `--limit N` (results per page, default 20: the section title counts every ranked hit on every page, and the page ends with the `--offset` that continues) | hybrid results over docs, names, signatures, each with the doc sentence that best matches the query | keyword guessing |
| `explain` | symbol, path or module identity | the doc stack: workspace (repos with their README first sentences), repo README summary, module chain, container, symbol signature, doc and state, then the top 5 callers and callees by fan-in with one-line docs. A path or module explains the module or file and lists its main symbols | reading three files to understand one method |
| `flow` | entry symbol; `--depth N` (default 3), `--documented-only` | call tree printed in call order, each node `signature — one-line doc`. Built breadth first: each symbol is expanded once, at its shallowest node, its other nodes tagged `[see above]` or `[see below]`; ambiguous calls list their candidates unexpanded; calls back up the path are tagged `cycle`; 200 nodes at most. `--documented-only` attaches the documented callees of undocumented symbols to the nearest documented caller | tracing by hand |
| `impact` | symbol; `--only callers\|refs\|impls\|tests\|mentions\|config`, `-C`/`-A`/`-B`/`--context`; default budget 3000 | what a change touches, one section per question under a shared budget: callers as call lines (a file with more than ten call sites prints three and a count), references, implementations and overrides, tests (direct, then `via ...`), mentions in documents and doc comments, config keys read, and `exported  yes\|no  used by <repos>` (efficiency addendum 5.1) | `callers` + `refs` + `impls` + `tests` + grep |
| `how` | symbol, or the path of a code file | `explain` (doc stack, top callers) and the first two levels of `flow` under a shared budget; a file gets the tree of each entry function; anything else is answered as `explain` answers it | `explain` + `flow` |
| `api` | package (a declared name, `name@version`, or an import specifier), optional name; `--doc` | the surface of a dependency without `--deps`: README sentence, exported names per module (main module, then the most used), the list of modules; with a name, that type or function with its doc and members, inherited ones named after the declaring type; a package that is declared but not indexed says why in one line | `outline --deps` + `find --deps` |
| `changes` | `--since <ref\|duration>`; `--repo` | changed files (`modified`, `added`, `deleted`, `renamed from X`) mapped to symbols, each tagged with the delta since the ref: `added`, `removed`, `moved`, `body-changed`, `doc-changed`, `doc-added`, `doc-removed`, plus the current doc state. Renames are detected; a symbol removed from one file and added to another in the changeset (matched by module and qualified name, else by a unique qualified name) is `moved`. A duration resolves to the newest commit older than it; the ref-side view comes from running the extractor on the blob at the ref, so no journal table is needed. Uncommitted and untracked changes count | `git diff --stat` plus reading |
| `history` | symbol; `--limit N` (0: all) | commits touching the symbol's span (`git log -L`), with dates and subjects | `git log -S` |
| `at` | symbol, `--ref A`, optional `--diff[=B]` | the symbol as it exists at ref A (found by qualified name in its current file), or with `--diff`, a unified diff of just that symbol between A and B; `--diff` alone means the working tree, a ref is given as `--diff=B` | `git show ref:path \| sed -n` |
| `gaps` | optional module, else the workspace or `--repo`; `--kind missing\|stale\|module`, `--limit N` (0: all within the budget) | ranked worklist (section 11.1), entries tagged `[#rank] [score S] [doc:state]` with a reason (`public, fan-in 2, changed 3d ago`); module entries carry a `repo:dir/` key | — |
| `doc set` | symbol or module (identity, `repo:dir/`, or a Python file module by its `.py` path), text from `--text` or stdin (empty text is an error) | writes the doc into the source and reindexes the file; marks the doc `ok` even when the text is unchanged. Prints nothing on success (the exit status is the result); `--verbose` prints the new span and state, `--format json` the result object; a failure is one line on stderr, `codedoc: doc set <address>: <what>; <what to do>`. `--batch` (instead of an address and `--text`) reads records from stdin, each a line `=== <address>` then the doc text, writes every one it can with one freshness check and one re-index per repo, prints one line per failed record (`<address>: <what>; <what to do>`) and exits 1 when any failed (efficiency addendum, section 6) | editing the file by hand |
| `doc check` | the workspace or `--repo` | brings the scope fully up to date (the freshness sync without its file limit, like `index` for that scope), then per repo `symbols ok:N stale:N missing:N  modules ok:N missing:N` (tests excluded) and problems: Go docs not starting with the symbol name (warning), Javadoc detached from its declaration by a blank line, Python docstrings in single quotes. Always exits 0 | — |

Operational commands take no budget flags:

| Command | Flags | Does |
|---|---|---|
| `index` | `--workspace`, `--repo`, `--format`, `--rebuild`, `--verbose` | full (`--rebuild` deletes the database first) or incremental index, then resolution, the duplicate identity warnings (7.3) and embedding. Prints nothing on success; `--verbose` prints the summary per repo, `--format json` the result object. A repo that cannot be indexed is one line on stderr (`<repo>: <what>; <what to do>`), the others are still indexed, and the exit status is 1 (efficiency addendum, section 6) |
| `status` | `--workspace`, `--repo`, `--format` | per repo: files, symbols, config keys, unresolved config reads, parse errors, `vectors:N/M`, dirty, indexed time, SCIP state; `status` never writes, so after an edit or a commit the header says `fresh:no` and the repo line ends with why the index is behind and what brings it up to date: `(index behind: <N files changed since indexed \| extractor updated, N files to re-extract \| the head moved or files were removed since indexed>; the next query or codedoc index brings it up to date)`, or `; run codedoc index to bring it up to date)` when more files changed than a query re-extracts (7.8); the note `daemon: running` or `daemon: not running`, and with a daemon per repo `queue:N`, `degraded: <why>`, `next-scip:`, `next-autodoc:`, `upkeep:on\|off\|running` with what holds it back in parentheses, `pending:N`, `last-upkeep:`, `last-error:`, `upkeep-error:` (efficiency addendum section 7; JSON `extra.upkeep`); without a daemon a workspace with upkeep enabled says `upkeep:needs-daemon` |
| `scip run` | `--workspace`, `--repo`, `--format` | section 7.4 |
| `daemon` | `--workspace` (default: all) | section 12 |
| `autodoc` | `--repo` (required), `--workspace`, `--max-items` (40), `--max-minutes` (30), `--commit`, `--allow-dirty` | section 11.2 |
| `mcp` | — | section 13.2 |
| `version` | — | the build version (`-X main.version`, else the version Go records) as `codedoc <version>`; the root flag `--version` (`-v`) prints the same line |

`index --verbose`, `status` and `scip run` print the query header (see
above): `repo@commit` with `--repo` in a workspace of several repos, the
target repos otherwise, nothing for a fresh, clean workspace of one
repo. `dirty` reflects the live `git status` of each repo.

## 10. Documentation write-back

`doc set` accepts symbols and modules; config keys are not documentable in
v1. Placement by language:

| Language | Symbol doc | Module doc |
|---|---|---|
| Go | `// Name ...` comment block directly above the declaration, the symbol name prefixed when the text does not start with it | the package comment (text prefixed `Package <name>`), so a package never gains a second one: in `doc.go` when it exists (another file that also carries a package comment is left alone and named in the note); else replaced in place in the first non-test file by name that carries one; else in a new `doc.go` with the package clause |
| TS/JS | `/** ... */` above (above `export`) | `README.md` in the directory, section `## Purpose` (replaced, appended, or a new README) |
| Python | docstring as the first statement, triple double quotes; a constant cannot carry one and is refused | `__init__.py` docstring (file created if absent); a file module's own docstring |
| Java | Javadoc `/** ... */` above the declaration and its annotations, with `@param`/`@return`/`@throws` preserved if present | `package-info.java` (created if absent) |

Rules:

- Existing docs are replaced in place; the span comes from the index so the
  edit is a byte-range replacement, never a search-and-replace.
- Text is wrapped at 80 columns (never narrower than 40 for deep
  indentation); paragraph breaks are kept.
- The target file is locked (flock under `$XDG_STATE_HOME/codedoc/locks/`)
  during planning and writing, and written atomically.
- The writer re-extracts the file after writing and verifies the symbol is
  still found with the new doc attached and that parse errors did not
  grow; otherwise it restores the original bytes and reports.
- Generated files are refused.
- Formatting follows the language's doc conventions, not Markdown, except in
  `README.md` targets.
- The index is updated synchronously so the next query sees `doc_state = ok`.
- Writes go to the working tree on the current branch. `doc set` never commits.
- Under `CODEDOC_RUN_ID` the original bytes are journaled before the
  first change of each file in that run (section 11.2).

## 11. Gaps and unattended documentation

### 11.1 Ranking

Each candidate gets `score = visibility × (1 + ln(1 + fan_in)) × kind_weight × recency`:

- visibility: exported or public 3, package-private 1.5, private 1;
  modules count as public
- fan_in: distinct symbols with an edge into the symbol (self-edges
  excluded); for a module, distinct modules importing it
- kind_weight: module `1 + ln(1 + files)` capped at 4 (`files`: the
  file count of the module size of section 5, the one `overview` prints,
  nested modules included), interface or trait 3, class or struct 2,
  field 0.5, everything else 1
- recency: 1.5 when changed in the last 30 days or uncommitted (for a
  module, any file below its directory), 1 otherwise
- order: every stale gap before every missing gap, because a wrong doc is
  worse than none; then score descending, then key

Excluded: tests and test paths (`_test.go`, `test_*.py`, `*_test.py`,
`.test.`, `.spec.`, `tests/`, `__tests__/`, `src/test/`), generated
files, anything under `testdata/`, `fixtures/`, `examples/`, `vendor/`,
`node_modules/` or `third_party/`, trivial accessors (a function or method
of at most 5 lines whose body is one `return` of a field or name, or one
assignment of a name to a field), symbols shorter than three lines unless
public, methods of documented interfaces, synthetic symbols the language
implies (Java record accessors, TS `it:`/`test:` symbols), members
documented by their declaration group (state `ok`, see 7.5), and anything
the doc writer would refuse (a Python constant cannot carry a docstring):
the head of the ranked list is checked by planning the write without
applying it, until `--limit` writable gaps are kept, else for the first
200.

### 11.2 Runner

`codedoc autodoc --repo R [--max-items N=40] [--max-minutes M=30] [--commit] [--allow-dirty]`

1. Restore leftovers: a run directory whose `autodoc.running` marker can be
   locked (its process died) has its journal restored and its marker removed.
2. Refuse a dirty working tree (untracked files included) unless
   `--allow-dirty`, which disables `--commit`.
3. Take the top N gaps, grouped by module (modules in the order of their
   best gap), at most `autodoc.group_size` keys (default 5) per group.
4. For each group, run Claude Code headless from the repo root, in its own
   process group (killed as a whole on timeout, cancellation, and on Linux
   when codedoc dies), with the running codedoc's directory first on `PATH`
   and `CODEDOC_HEADLESS=1`, `CODEDOC_RUN_ID=<id>` exported:
   `claude -p <prompt> --output-format json --tools Bash,Read --allowedTools "Bash(codedoc:*),Read" --disallowedTools "Edit,Write,MultiEdit,NotebookEdit" --permission-mode dontAsk --max-turns <10 + 8 per key, at most 60> --max-budget-usd <autodoc.max_cost_usd, default 1.00>`.
   The editing tools do not exist in the session and `dontAsk` denies every
   call not pre-approved, so the agent can only read and run `codedoc`. The
   self-contained prompt lists each key as `<key> (<state>, path:line)`,
   gives the `explain`, `symbol`, `callers`, `tests` and `siblings`
   commands to gather context for each key, tells the agent to write all
   the docs of the group in one `codedoc doc set --batch` call with a
   heredoc (records `=== <key>` then the text), that `doc set` prints
   nothing on success and one line `<key>: <what>; <what to do>` per doc
   it could not write, names the `codedoc-document` skill, and says to
   skip and report a key it cannot resolve and to stop after the listed
   keys.
5. Guard: before the group the runner records HEAD, `git status` with a
   SHA-256 of every dirty or untracked path, and every symbol's doc hash.
   The group fails when the agent journaled a file outside the repo root,
   changed a path outside the run's journal, ended with an error or an
   `is_error` envelope (the reason carries its `subtype`), left a key not
   `ok` once the journaled files are reindexed, or changed the doc of a key
   outside the group.
6. On failure the journaled files are restored, in every repo; a file
   whose content no longer matches the hash of the run's last write was
   edited by someone else and is kept, reported, and its journal kept.
   Paths clean before the group are copied to `<run>/reverted/` and put
   back to HEAD. A path already dirty before the group (`--allow-dirty`)
   that changed, or a moved HEAD, stops the run with an error naming them,
   since nothing could restore them safely. On success, `--commit` commits
   exactly the journaled paths as `docs(codedoc): document <module>` with
   the keys in the body, one per line; a commit that fails (a hook, no
   identity) unstages them again before the group is restored. Autodoc
   never pushes.
7. Stop at `--max-items`, `--max-minutes`, or an empty queue, and print
   `documented N, skipped N, failed N, cost $X.XXXX` from the envelopes,
   plus `unknown` for runs that timed out or crashed before reporting.

The journal: every `doc set` (each record of a `doc set --batch` too)
under a `CODEDOC_RUN_ID` saves a file's
original bytes before its first change in that run to
`$XDG_STATE_HOME/codedoc/runs/<id>/`, keyed by repo root
(`<basename>-<8 hex of its sha256>`) and repo-relative path, and appends
`{root, path, existed, copy, after}` to `manifest.jsonl` (`after`: the hash
of what the write left). Restores never use `git checkout`, which would
discard the user's own uncommitted edits. The journal of a run that wrote
outside the root is never deleted.

The daemon schedules the runner per repo: every `autodoc.every`, pinned to
the local time `at` when the interval is a day or more (`at` alone means
daily); missed runs are skipped, scheduled runs are limited to 30 minutes
and `max_items` (absent or 0: 40, the `--max-items` default), and runs
are serialized per repo. A scheduled run skips, with a log line, a repo
whose tree is dirty or whose session lock is held: a file lock at
`<data dir>/<workspace>.locks/<repo>` that the MCP server takes for every
repo of its workspace for the whole session, so it works without the
daemon. A manual `codedoc autodoc` ignores the lock:
the user asked for it from inside their session. An MCP server started
under `CODEDOC_HEADLESS` takes no lock, so a headless session that loads
the codedoc plugin cannot block its own run.

Documentation upkeep (efficiency addendum, section 7) is a second,
separate trigger of the same runner: it documents what a branch changed
once its repo is clean and quiet, as one held run (`autodoc.RunHeld`)
whose groups are neither committed nor dropped from the journal until
the whole run is committed as one commit, kept, or reverted. It does
not check the session lock, and it leaves a change outside its journal
alone instead of putting it back to HEAD (step 6), since under upkeep
that change is the user's. Leftover journals (step 1) are restored
newest first, because the groups of a held run write on top of each
other.

## 12. Daemon

`codedoc daemon [--workspace W]` runs in the foreground until SIGINT or
SIGTERM, then finishes the round in progress and exits 0 (systemd user
unit `deploy/systemd/codedoc.service`: `Restart=on-failure`,
`TimeoutStopSec=30` so a round can finish, a `PATH` with `~/.local/bin` so
scheduled autodoc finds `claude`):

- takes a single-instance lock of the data directory
  (`<data dir>/daemon.lock`) and listens on the socket before watching or
  indexing anything, so a second daemon fails at once naming the lock; then
  brings every repo up to date, logging catch-up progress per repo
- socket: `$XDG_RUNTIME_DIR/codedoc.sock` when that variable is an absolute
  path, else `${XDG_STATE_HOME:-~/.local/state}/codedoc/codedoc.sock`; a
  path too long for a Unix socket fails with a hint to set a shorter
  `XDG_RUNTIME_DIR`. It answers `codedoc status` and the freshness
  question of section 7.8 (the session lock is a file lock, section 11.2)
- watches repo roots with fsnotify (the exclusions of 7.1); debounces
  events 300 ms, delivered at most 2 s after the first pending one; indexes
  in rounds of at most 5,000 files, checking for shutdown between rounds
- polls `.git/HEAD` and refs every 2 s (worktrees included) to catch
  branch switches and commits that fsnotify misses in `.git`
- keeps the embedding model loaded and embeds what it re-extracts
- schedules SCIP runs (`scip: {every: 6h}`; a repo never merged runs at
  start) and autodoc runs (11.2), serialized per repo
- where a workspace sets `upkeep.enabled`, checks each of its repos on
  every tick (30 s) and documents what the checked-out branch changed
  once the repo is clean and quiet, serialized with the scheduled jobs
  of the repo; a change that is not the run's own cancels and undoes
  the run (efficiency addendum, section 7)
- reloads the configuration file when its content changes (checked every
  2 s): the workspaces are closed and reopened and the repos re-watched; a
  file that does not load is logged once and the running configuration kept
- marks a repo degraded, never fresh, while it may miss changes: a
  directory it cannot watch (running out of inotify watches says how to
  raise `fs.inotify.max_user_watches`), a watch error or event overflow
  (followed by a full rescan that clears it), or a missing repo directory
  (re-watched once it reappears). Other repos keep being served
- logs to stderr and `${XDG_STATE_HOME:-~/.local/state}/codedoc/daemon.log`,
  rotated to `.1` at 10 MB; a failure or panic in one repo is logged and
  kept as that repo's `last-error` (cleared by the next success of the
  same kind) without stopping the others

The CLI and MCP server never depend on the daemon; they fall back to the
synchronous freshness check from section 7.8.

## 13. Agent surface

### 13.1 CLI

`codedoc <query> [args] [--format text|json] [--budget N] [--offset N] [--out file]`
(operational commands: section 9). Exit status (efficiency addendum,
section 4.1, which replaced the statuses 2 "not found" and 3 "ambiguous"
of this section):

| Status | Meaning |
|---|---|
| 0 | answered, including answers whose header says `fresh:no`, the nearest candidates of an address that matched nothing, the best of several candidates, and a `find` or `search` without hits |
| 1 | nothing at all could be offered (not found, a path outside the index or the workspace), a failed write, failures a command listed itself, and any other failure |
| 2 | usage: an unknown command or flag, a wrong argument count, an invalid flag value |
| 3 | only with `--strict`: an ambiguous address, after its candidates |
| 4 | internal: the database, the file system (other than a missing file), a panic |

Errors go to stderr as `codedoc: <message>`; with `--format json` the last
line of stdout is also `{"error": "...", "code": N}`, N being the exit
status. The compact text
format is the default because the agent reads it; JSON is for scripts.

### 13.2 MCP server

`codedoc mcp` (stdio, official Go SDK `modelcontextprotocol/go-sdk`, logs on
stderr). Nineteen tools: the eleven of this design, `codedoc_deps` of the
dependency addendum (`2026-10-10-codedoc-deps-design.md`), `codedoc_ls`,
`codedoc_lines` and `codedoc_feedback` of the documents addendum
(`2026-10-10-codedoc-documents-design.md`), and `codedoc_where`, `codedoc_impact`,
`codedoc_how` and `codedoc_api` of the efficiency addendum
(`2026-10-11-codedoc-efficiency-design.md`, section 9.1, which also lists
the inputs each tool lost: every input is paid for in each request that
lists the tools). Each call is translated to the arguments of the matching
CLI command and run in process, so the CLI and MCP answers are the same:

| Tool | Inputs (besides the common ones) |
|---|---|
| `codedoc_overview` | — |
| `codedoc_outline` | `target`, `detail` |
| `codedoc_ls` | `paths`, `since`, `sort`, `long` |
| `codedoc_lines` | `path`, `range` |
| `codedoc_find` | `pattern`, `kind`, `deps`, `detail` |
| `codedoc_symbol` | `symbol`, or `names` (several addresses in one call), `no_body`, `deps`, `detail` |
| `codedoc_impact` | `symbol`, `only` (one section: callers, refs, impls, tests, mentions, config), `context`, `detail` |
| `codedoc_how` | `target` |
| `codedoc_api` | `package`, `name`, `detail` |
| `codedoc_graph` | `relation` (callers, callees, refs, impls, supertypes, tests, siblings, deps, dependents), `target`, `depth`, `no_tests`, `context`, `detail` |
| `codedoc_config` | `key` |
| `codedoc_search` | `query`, `kind`, `deps`, `context`, `detail` |
| `codedoc_where` | `query`, `limit`, `context`, `deps` |
| `codedoc_explain` | `target` |
| `codedoc_flow` | `symbol`, `depth` |
| `codedoc_history` | `mode` (changes, history, at), `since`, `symbol`, `ref`, `diff` (`"true"`: the working tree) |
| `codedoc_doc` | `action` (gaps, set, check), `target`, `text`, `docs` (set: a list of `{address, text}` written as one `doc set --batch`, instead of `target` and `text`), `kind` |
| `codedoc_deps` | `action` (list, index, prune) |
| `codedoc_feedback` | `text`, `path`, `list` |

Every tool also accepts `workspace`, `repo`, `offset` and `budget` (default
6000 bytes, 3000 for `codedoc_impact`; capped at 60000, and 0, unlimited on
the CLI, means 60000, so one call cannot flood the agent's context). The
flags left out of the schemas (`--format`, `--out`, the filters of each
command) remain on the CLI. A write that succeeds (doc `set`, a feedback
report, deps `index` and `prune`) answers `ok`, since the CLI prints
nothing then and a tool result must have content; a `docs` batch in which
records failed is a tool error whose text is its failure lines.
The nearest candidates of an address that matched nothing and the best
of several candidates are the answer of the tool; the one line of an
address or path nothing can be offered for (`not found`, `is not
indexed`) is a normal text result too, not a tool error. An error saying the server's directory
lies outside every workspace suggests the `workspace` input. Each tool is
described in one sentence.

On start the server takes the session lock (11.2) of every repo of the
workspace of its directory, non-blocking (a lock another session holds is
logged, not fatal), and holds them until it exits; none under
`CODEDOC_HEADLESS`, and none when its directory is in no workspace.

### 13.3 Plugin and skills

Shipped as a Claude Code plugin `codedoc`. Layout:

```
.claude-plugin/marketplace.json   local marketplace "codedoc" listing the plugin (source ./plugin)
plugin/.claude-plugin/plugin.json
plugin/.mcp.json                  server "codedoc": stdio, `codedoc mcp`
plugin/skills/codedoc-lookup/SKILL.md
plugin/skills/codedoc-document/SKILL.md
plugin/skills/codedoc-maintain/SKILL.md
plugin/agents/codedoc-explorer.md     subagent: answers a question from the index (efficiency addendum, section 8)
plugin/agents/codedoc-documenter.md   subagent: documents given addresses with one doc set --batch
plugin/hooks/session-start.sh     not registered by the plugin; opt-in through settings.json
```

Installed with `claude plugin marketplace add <repo>` and
`claude plugin install codedoc@codedoc`. A test walks the skills and the
agents and fails on any `codedoc <command>` that is not a real
subcommand, on agent front matter without `name`, `description`,
`tools` and `model`, and on the size limits below.

The skills were rewritten by the efficiency addendum (sections 7, 8 and
9): the two a session carries hold at most 60 and 40 lines and say
nothing about how the tool works.

| Skill | Content |
|---|---|
| `codedoc-lookup` | a table of need to command (`symbol`, `outline`, `impact`, `where`, `how`, `api`, `ls`, `lines`), when to delegate a question to the `codedoc-explorer` agent, and the cross-check (`lines`, `feedback`). Its description triggers on the habit: about to read or search the repository's code with grep, sed, cat or Read |
| `codedoc-document` | while writing or changing code, always write the doc comment in the edit, for what only the session knows; what a good doc contains per level (function, type, module); `doc set` or the `codedoc-documenter` agent for code that is not being edited; never look for stale docs (upkeep does) |
| `codedoc-maintain` | silent writes and exit statuses, `gaps`, `doc check`, `doc set --batch`, upkeep, `autodoc`, adding a repo, dependency states, enabling SCIP, the daemon and its unit |

- Hook (optional, off by default): `SessionStart` prints a title line and
  the first 40 lines of `codedoc overview --budget 3000` for the current
  workspace, and nothing when codedoc is missing or the directory is in no
  workspace.

## 14. Configuration

`$XDG_CONFIG_HOME/codedoc/config.yaml` (default `~/.config`), or the file
`CODEDOC_CONFIG` names. Decoding is strict: an unknown key fails with
`file:line: unknown key "x" in <section>`. Every key:

```yaml
embed:
  model_dir: ~/src/codedoc/models   # nomic-embed-text-v1.5.onnx + tokenizer.json; CODEDOC_MODEL_DIR wins
workspaces:
  myapp:
    repos:
      - path: ~/src/myapp        # relative paths resolve against the config file's directory
      - path: ~/src/myapp-config
        name: config                 # default: the directory name; unique per workspace
        languages: [yaml, properties, shell]   # default: all
    exclude: ["**/generated/**"]     # doublestar globs, validated at load
    scip:
      enabled: [platform]            # repo names of this workspace
      every: 6h                      # Go duration or Nd / Nw; absent or 0: not scheduled
    autodoc:
      every: 24h                     # absent with at: daily; neither: off
      at: "02:00"                    # HH:MM local time, for intervals of 24h or more
      max_items: 40                  # gaps per scheduled run; absent or 0 means 40
      commit: true
      group_size: 5                  # keys per claude run; 0 means 5
      max_cost_usd: 1.00             # --max-budget-usd per claude run; 0 means 1.00
    upkeep:                          # efficiency addendum, section 7; needs the daemon
      enabled: true                  # default false
      quiet: 5m                      # no file event for this long; absent or 0: 5m
      max_items: 20                  # candidates per run; absent or 0: 20
      commit: true                   # one docs: commit per run; default true
      first_run_commits: 20          # commits a first run on a branch looks back; absent or 0: 20
      max_cost_usd: 1.00             # per run; absent or 0: 1.00
      daily_cost_usd: 5.00           # per workspace and local day; absent or 0: 5.00
    deps:                            # third-party packages (deps addendum, sections 4 and 10)
      enabled: true                  # default true
      include: ["eslint*", zod-to-json-schema]   # names or doublestar globs indexed although no source file imports them
      exclude: ["@types/react"]      # doublestar globs on the name; never indexed, not listed
  personal:
    repos:
      - path: ~/src/api
```

`languages` takes `go`, `ts` (typescript, tsx, javascript), `typescript`
(typescript, tsx), `javascript`, `py`, `python`, `java`, and the config
formats `yaml`, `json`, `xml`, `properties`, `env`, `hcl`, `shell`,
`template`. Config load fails on a repo in two workspaces or listed twice,
two repos with one name, an empty path, an unknown language, a malformed
glob, an unknown `scip.enabled` repo, an `at` that is not `HH:MM`, a
negative `max_items`, `group_size` or `max_cost_usd`, a negative number
in `upkeep`, and an empty name or a malformed pattern in `deps.include`
or `deps.exclude`.
`autodoc --max-items` must be positive.

`deps` selects the third-party packages indexed for the workspace. By
default a package a manifest declares is indexed when a source file of
the workspace imports it, whether the manifest lists it under
`dependencies` or `devDependencies`; the others are listed by
`codedoc deps` as `unused`. `deps.include` indexes a package anyway: a
name or pattern that matches a declared package, or the plain name of a
transitive one. The deps addendum (sections 4 and 10) has the rules.

Environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `CODEDOC_CONFIG` | `$XDG_CONFIG_HOME/codedoc/config.yaml` | config file |
| `CODEDOC_DATA` | `$XDG_DATA_HOME/codedoc` (an absolute `XDG_DATA_HOME` only), else `~/.local/share/codedoc` | databases and their lock files, `daemon.lock`, session locks |
| `CODEDOC_MODEL_DIR` | `embed.model_dir` | embedding model directory; overrides the config |
| `CODEDOC_ONNXRUNTIME_LIB` | `/usr/local/lib/libonnxruntime.so` | ONNX Runtime shared library |
| `CODEDOC_EMBED_THREADS` | half the logical CPUs, at most 12 | threads of one model call; more than 12 measured no faster and burn CPU spinning |
| `CODEDOC_EMBEDDER` | unset | `off` disables embeddings; `fake` selects the deterministic test embedder |
| `CODEDOC_FRESH_MAX_FILES` | 200 | most changed files a query re-extracts before answering |
| `CODEDOC_LOCK_TIMEOUT` | 60s | write-lock wait: a duration or a number of seconds |
| `CODEDOC_SCIP_GO`, `_TS`, `_PY`, `_JAVA` | scip-go, scip-typescript, scip-python, scip-java | indexer binaries |
| `CODEDOC_CLAUDE_BIN` | `claude` | the binary autodoc and upkeep run |
| `CODEDOC_HEADLESS` | unset | set by autodoc and upkeep for `claude -p`; the MCP server then takes no session lock |
| `CODEDOC_RUN_ID` | unset | set by autodoc; `doc set` journals into `runs/<id>` |

Databases live in `<data dir>/<workspace>.db`; the state directory
`${XDG_STATE_HOME:-~/.local/state}/codedoc` (a relative `XDG_STATE_HOME`
is ignored) holds the daemon log, `runs/`, the per-file write locks and the
fallback socket. A directory belongs to the workspace of the innermost
configured repo containing it; a directory inside the config file's
directory that contains repos of exactly one workspace belongs to that
workspace; otherwise commands need `--workspace`, and the error names the
configured workspaces.

## 15. Error handling

- Parse errors: tree-sitter produces partial trees; extractors emit what they
  can and the file row records `parse_errors = N`. Queries never fail because
  one file is broken.
- Missing toolchain for SCIP: the repo stays on heuristic tiers; `status`
  reports it once, not on every query.
- Ambiguity is a result, not an error: candidates are listed with their
  modules so the agent can pick.
- Budget exhaustion is reported in the response with the exact follow-up.
- Doc writes that break parsing are reverted and reported.
- A binary built without the `sqlite_fts5` tag fails with `built without
  FTS5 support: rebuild with -tags sqlite_fts5`.
- Database schema migrations run on open, each in one transaction, under
  an flock of `<db>.init`. A database from a newer codedoc fails with
  `database was created by a newer codedoc (schema N > 10); upgrade
  codedoc or run codedoc index --rebuild`; any other open or migration
  failure names `codedoc index --rebuild`, since all data is derived. The
  daemon and the MCP server never prompt: they fail with that message.
  `--rebuild` is refused while a daemon serves the data directory (stop it
  first).
- The write lock times out after `CODEDOC_LOCK_TIMEOUT` with `the database
  write lock is still held after <d> (held by pid N: <cmdline>); retry
  later or raise CODEDOC_LOCK_TIMEOUT`.
- A repo whose configured path changed keeps its index when the old path
  is gone (the repo moved; file paths are relative to its root); while
  both paths exist, indexing fails with a message naming
  `codedoc index --rebuild`.
- The daemon logs to stderr and to `~/.local/state/codedoc/daemon.log`;
  crashes in one repo's pipeline do not stop the others.
- A configured repo whose directory is missing does not fail queries on the
  other repos: its freshness is `fresh:no` with the note `directory missing,
  answered from the last index: <name> (<path>)`, answers draw on its last
  index (which is kept), and `status` shows it `missing`. A repo never
  indexed adds `not indexed: <name>; run codedoc index`; a database
  extracted by an older extractor with more files to redo than the
  freshness limit adds `extractor updated; N files to re-extract; run
  codedoc index`.

## 16. Testing

- **Fixture workspace** `testdata/ws/`: one small repo per language plus a
  config repo, with a known graph (an interface with two implementations,
  a cross-file call chain of depth 3, an overload, a covered function, a
  generated file, config keys read from code, cross-repo imports). The test
  helper copies a fixture to a temp directory and creates its git history
  there, so the project repository has no nested `.git` directories.
- **Unit tests**: golden JSON per fixture file for the extractors; edge
  lists with confidence tiers; a committed `index.scip` for the SCIP merge
  (precise edges, revalidation, cross-repo); doc writer round trips per
  language at symbol and module level, with a reverted malformed write;
  staleness after body and doc edits and blame seeding; CLI text and JSON
  snapshots per query, budget truncation, freshness; every MCP tool once
  in process; the daemon on a temp workspace (a touched file reindexed
  within 1 s, a branch switch); autodoc with a stub `claude` (commit,
  check, revert paths); the plugin skills name only real commands.
- **Integration tests** (build tag `integration`, `make docker-integration`):
  real `scip-go`, `scip-typescript`, `scip-python`, ONNX Runtime with the
  model, the MCP server and daemon over a built binary, and an acceptance
  run over this repository. They fail, never skip, when a tool is missing.
  The Docker image pins Go, ONNX Runtime, golangci-lint, Node, pnpm and the
  indexers; the source is mounted read-only and copied before the run.
  The dependency addendum adds tests over a real `pnpm install` and a
  real `go mod download` made when the image is built (they are skipped
  only where no image provides that install), the documents addendum
  one over the stdio MCP server.
- Coverage gate 90% on `internal/` (`make test`), per the repository's code
  quality rules.

## 17. Delivery order

Delivered in ten steps, each leaving a usable tool, over five plans
(`docs/history/plans/`, historical): Plan 1 the skeleton, Go and
TypeScript extraction, resolution and the graph queries; Plan 2 Python,
Java and config keys; Plan 3 doc states, write-back, gaps, `changes`,
`history`, `at`, `overview`; Plan 4 embeddings, `search`, `explain`,
`flow`, the MCP server and the plugin; Plan 5 the daemon, SCIP, autodoc
and the Docker test suite.

## 18. Later

- Pure-Go tree-sitter runtime behind the parser interface.
- MCP results streamed to a file by default once Claude Code's handling of
  large MCP results is confirmed.
- Shared sync of derived data for a team.
- Indexing of dependency sources and declaration files.
- Service-boundary edges (HTTP routes, message topics).
- Embedding-based "similar code" for `siblings`.
