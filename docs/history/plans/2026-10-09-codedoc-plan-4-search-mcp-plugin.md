# codedoc Plan 4: Embeddings, search, explain, flow, MCP server, plugin and skills

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Semantic plus keyword search over docs and symbols, the understanding queries (`explain`, `flow`), an MCP stdio server exposing the query layer as eleven tools, and a Claude Code plugin with the three skills.

**Architecture:** An `Embedder` interface with an ONNX implementation (ported from an earlier project) and a deterministic fake for tests. Vectors are stored as float32 BLOBs in SQLite and scored by brute-force cosine in Go; this replaces the spec's optional sqlite-vec dependency (a workspace holds at most a few hundred thousand documented symbols, and a linear scan of 768-dim vectors at that size takes tens of milliseconds) and removes a native extension from the install. The spec is updated accordingly in Task 1. Search fuses FTS5 BM25 and cosine ranks with reciprocal rank fusion. The MCP server uses the official Go SDK and calls the same query functions as the CLI. Spec: sections 7.7, 9 (`search`, `explain`, `flow`), 13.2, 13.3, 17 steps 8 and 9.

**Tech Stack:** `github.com/yalue/onnxruntime_go` v1.27.0 (ONNX Runtime 1.17.1 at `/usr/local/lib`, already installed on the development machine and installed in the Docker image), `github.com/modelcontextprotocol/go-sdk` v1.8.0.

---

## File structure

```
internal/embed/embed.go, onnx.go, tokenizer.go   ported from an earlier project's internal/embed (same API)
internal/embed/fake.go                           deterministic hashing embedder for tests (dims 64)
internal/embed/text.go                           embedding input composition
internal/store/vectors.go                        symbol_vec / module_vec tables, upsert, scan
internal/index/embeddings.go                     incremental embedding pass
internal/query/search.go, explain.go, flow.go
internal/mcpserver/server.go, tools.go, lock.go
cmd/codedoc/cmd_mcp.go
plugin/.claude-plugin/plugin.json
plugin/.mcp.json
plugin/skills/codedoc-lookup/SKILL.md
plugin/skills/codedoc-document/SKILL.md
plugin/skills/codedoc-maintain/SKILL.md
plugin/hooks/session-start.sh                    opt-in hook script (not registered by default)
.claude-plugin/marketplace.json                  local marketplace so the plugin installs from this repo
```

---

### Task 1: Embedder port and vector storage

- Copy `internal/embed/{embed.go,onnx.go,tokenizer.go}` from an earlier project into `internal/embed/`, keep the API (`Embedder`, `NewEmbedder`, options), fix the package path, add tests that run only when `CODEDOC_MODEL_DIR` points at a directory with `nomic-embed-text-v1.5.onnx` and `tokenizer.json` (skip otherwise). nomic requires the `search_document: ` / `search_query: ` prefixes; add `EmbedDocument` and `EmbedQuery` helpers that apply them.
- `fake.go`: `NewFake(dims int)` hashes lower-cased word tokens into a bag-of-words vector, L2-normalized, so similar texts score higher. Used by every non-model test.
- Configuration: `embed:` section in the config file (`model_dir`), overridden by `CODEDOC_MODEL_DIR`. No model configured means no embeddings and search is FTS-only, stated in the response note.
- Migration 2: `CREATE TABLE symbol_vec(symbol_id INTEGER PRIMARY KEY REFERENCES symbol(id) ON DELETE CASCADE, text_hash TEXT NOT NULL, vec BLOB NOT NULL)` and the same for `module_vec`. Store: `UpsertSymbolVec`, `SymbolVecs(filter)` streaming rows, `StaleVecs` (rows whose `text_hash` differs from the current composed text hash, or missing).
- `text.go`: composition per spec 7.7: `path + "\n" + container + "\n" + signature + "\n" + doc + "\n" + first 10 lines of body` for symbols; `identity + path + doc` for modules. Only documented symbols and documented modules are embedded.
- `index/embeddings.go`: after a repo is indexed, embed changed rows in batches of 32; `codedoc index` runs it when an embedder is configured.
- Update spec section 7.7 and section 8 to describe BLOB vectors with brute-force cosine instead of sqlite-vec (one paragraph each).

- [ ] Tests (fake embedder): vectors written for documented symbols only, re-index without change writes nothing, doc edit rewrites one row, deleting a symbol cascades its vector. Commit `feat(embed): onnx embedder port, fake embedder, vector storage`.

### Task 2: `search`

`codedoc search <text> [--kind K] [--lang L] [--repo R] [--limit N]`:

1. FTS: query `symbol_fts` and `module_fts` with the text turned into an FTS5 query (tokens joined with `OR`, each quoted, special characters stripped, as the earlier project does), top 50 each by `bm25`.
2. Vectors (when an embedder is configured): embed the query, scan vectors with filters applied, top 50 by cosine.
3. Reciprocal rank fusion with k = 60 across the up to four lists; modules and symbols interleave.
4. Each entry: stable key (or module identity), path:line, signature, and a snippet (the doc sentence with the best token overlap).

- [ ] Tests with the fake embedder: a query matching a doc word ranks that symbol first; filters work; FTS-only mode works and says so. Commit `feat(query): hybrid search`.

### Task 3: `explain` and `flow`

- `explain <symbol-or-path>`: the doc stack: repo (README first paragraph), module chain from the repo root module down to the symbol's module (each module's one-paragraph doc), the container's doc, the symbol's signature and full doc, then the top 5 callers and top 5 callees by fan-in with one-line docs, and the doc state. A path argument explains the module or file.
- `flow <symbol> [--depth N (default 3)] [--documented-only]`: depth-first call tree from the entry symbol over `calls` edges (ambiguous edges listed with their candidates and marked `[ambiguous]`, not expanded), each node rendered as `signature — one-line doc`, cycles marked `[cycle]`, node cap 200. `--documented-only` prunes undocumented nodes but keeps their documented descendants attached to the nearest documented ancestor.

- [ ] Snapshot tests over the fixture workspace; commit `feat(query): explain and flow`.

### Task 4: MCP server

`codedoc mcp` starts a stdio MCP server (official Go SDK) exposing eleven tools (spec 13.2). Each tool's input schema mirrors the CLI flags of the queries it covers; each returns text content rendered through the same output frame:

| Tool | Covers | Required input |
|---|---|---|
| `codedoc_overview` | overview | — |
| `codedoc_outline` | outline | `target` |
| `codedoc_find` | find | `pattern` |
| `codedoc_symbol` | symbol | `symbol` |
| `codedoc_graph` | callers, callees, refs, impls, supertypes, tests, siblings, deps, dependents | `relation`, `target` |
| `codedoc_config` | config | `key` |
| `codedoc_search` | search | `query` |
| `codedoc_explain` | explain | `target` |
| `codedoc_flow` | flow | `symbol` |
| `codedoc_history` | changes, history, at | `mode` plus mode inputs |
| `codedoc_doc` | gaps, doc set, doc check | `action` plus action inputs |

Common optional inputs: `workspace`, `repo`, `budget` (default 6000), `offset`, `out` (a path, or `true` for `$TMPDIR/codedoc/<pid>/<tool>-<n>.txt`), `format`. Errors for not-found and ambiguous are returned as normal text results (the candidate list), not protocol errors. Tool descriptions are short (one or two sentences) and say that body-returning or long results are better fetched with the CLI and `--out` through Bash.

To share code with the CLI, move the CLI's per-command argument handling into `internal/query` request structs (`OutlineRequest`, `GraphRequest`, ...) with one `Run(ctx, req) (*output.Response, error)` per query, if Plan 1 did not already; the CLI and MCP both build requests.

Session lock (spec 11.2): on start, unless `CODEDOC_HEADLESS` is set, take an exclusive non-blocking `flock` on `~/.local/share/codedoc/<workspace>.locks/<repo>` for every repo of the resolved workspace (data dir honors `CODEDOC_DATA`); hold until exit. Failing to take a lock (another session) is logged to stderr and not fatal. `internal/mcpserver/lock.go` exposes `Held(workspace, repo) bool` for the autodoc scheduler (Plan 5).

- [ ] Tests: in-process client/server pair from the SDK calls every tool once against the fixture workspace; lock taken and released; `CODEDOC_HEADLESS=1` takes none. Commit `feat(mcp): stdio server with eleven tools and session locks`.

### Task 5: Plugin and skills

- `plugin/.claude-plugin/plugin.json`: `{"name": "codedoc", "description": "...", "version": "0.1.0", "author": {"name": "muralx"}}`.
- `plugin/.mcp.json`: `{"codedoc": {"type": "stdio", "command": "codedoc", "args": ["mcp"]}}`.
- `.claude-plugin/marketplace.json` at the repo root listing the plugin with `"source": "./plugin"`, so `claude plugin marketplace add <repo path>` then `claude plugin install codedoc@codedoc` works.
- Three skills, each with YAML front matter (`name`, `description` that states when to trigger) and a body under 150 lines, written per spec 13.3:
  - `codedoc-lookup`: start sessions with `codedoc overview`; before reading a file run `outline`; to read one function use `symbol --body`; before changing a symbol run `callers --no-tests` and `tests`; for "how does X work" use `explain` then `flow`; for settings use `config`; never grep for a definition; address forms; budgets, `--offset`, `--out`; exit codes; when to fall back to reading files (generated code, fresh:no answers on huge changes).
  - `codedoc-document`: what a good doc contains at method, class/struct, and module level (contract, inputs, errors, side effects, concurrency; role, invariants, lifecycle; purpose, main types, how dependents use it, what it must not do); gather context with `explain`, `callers`, `tests`, `siblings`; write with `codedoc doc set` (stdin for multi-paragraph text); finish every code change with `codedoc changes --since HEAD` and fix every `stale`; never document generated files; one sentence summary first.
  - `codedoc-maintain`: reading `gaps`, triaging, `doc check`, running `codedoc autodoc` (flags and safety: refuses dirty trees, `--commit`, `--max-items`), adding a repo to a workspace config, enabling SCIP, `codedoc daemon` and its systemd unit, `codedoc status`.
- `plugin/hooks/session-start.sh`: prints `codedoc overview --budget 3000` for the cwd's workspace (silently nothing when not configured). Not registered in a `hooks.json` (spec: off by default); the README shows the settings snippet that registers it.
- A test (`plugin_test.go` under `cmd/codedoc` or a small `internal/plugincheck`) parses the JSON files and the skill front matter and checks every `codedoc <command>` mentioned in the skills is a real subcommand (walk the cobra tree).

- [ ] Commit `feat(plugin): claude code plugin with lookup, document and maintain skills`.

### Task 6: README

Document search setup (model download target `make download-model` copied from the earlier project's Makefile into `models/`, `CODEDOC_MODEL_DIR`), the MCP server, plugin installation, and the hook snippet. Commit `docs: search, mcp and plugin`.

## Implementation notes

This plan is historical; the spec describes the code as built. Main deviations:

- Vector tables are migration 7, not 2. A symbol's `text_hash` is `doc_hash:body_hash`, and vector passes run outside the write lock (spec 7.7, 8).
- Freshness checks embed only with a warm model and at most 64 stale vectors; `search` catches up at most 64 before answering (spec 7.7).
- `flow` is built breadth first and printed in call order, with `[see above]`/`[see below]` and `cycle` tags (spec 9).
- MCP budgets are capped at 60000 bytes, `0` included. `out: true` files go under `$TMPDIR/codedoc/<pid>/` and are cleaned up for dead processes (spec 13.2).
- Session locks live in `internal/session`, not `internal/mcpserver/lock.go` (spec 6, 11.2).
