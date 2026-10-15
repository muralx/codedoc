# codedoc Plan 5: Daemon, SCIP, autodoc, Docker test suite

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep indexes fresh without being asked (watcher, branch switches), add IDE-quality references from SCIP indexers, fill documentation gaps unattended through `claude -p`, and run the whole test suite, including toolchain-dependent integration tests, inside Docker.

**Architecture:** `codedoc daemon` loads the workspace config, watches every repo with fsnotify, debounces, and runs the incremental pipeline; it polls `.git/HEAD` for branch switches, serves status on a Unix socket, and schedules SCIP and autodoc runs. SCIP runs shell out to the per-language indexer and merge `index.scip` into `precise` edges. The autodoc runner is a command usable by hand and by the scheduler. Docker provides the reproducible environment for tests; the daemon, CLI and MCP server run on the host because they read the user's working trees. Spec: sections 7.4, 11.2, 12, 14, 16, 17 step 10.

**Tech Stack:** `github.com/fsnotify/fsnotify` v1.10.1, `github.com/sourcegraph/scip` v0.10.0 (`bindings/go/scip`, `IndexVisitor.ParseStreaming`, `SymbolRole`), `google.golang.org/protobuf` (pulled by scip).

---

## File structure

```
internal/daemon/daemon.go        lifecycle, per-workspace runners, crash isolation
internal/daemon/watch.go         fsnotify recursive watching, exclusions, debounce
internal/daemon/head.go          .git/HEAD and refs polling
internal/daemon/socket.go        Unix socket JSON-lines status protocol
internal/daemon/schedule.go      every/at schedules for scip and autodoc
internal/scip/run.go             indexer invocation per language
internal/scip/merge.go           occurrence → symbol mapping, precise edges, scip_symbol table
internal/autodoc/runner.go       spec 11.2 steps 1-5
internal/autodoc/prompt.go       prompt text for claude -p
internal/config (extend)         scip:, autodoc:, embed: sections (spec 14)
cmd/codedoc/cmd_daemon.go, cmd_scip.go, cmd_autodoc.go
deploy/systemd/codedoc.service
Dockerfile, docker-compose.yml, .dockerignore
Makefile (docker-test, docker-lint, docker-shell targets)
```

---

### Task 1: Config sections

Extend `config.Workspace` with `SCIP struct{ Enabled []string; Every Duration }`, `Autodoc struct{ Every Duration; At string; MaxItems int; Commit bool }`, and top-level `Embed struct{ ModelDir string }` (if Plan 4 did not add it). `Duration` parses `6h`, `24h`, `30m`, `2d`. Validation: `scip.enabled` names must be repos of the workspace; `autodoc.at` is `HH:MM`. Tests; commit `feat(config): scip, autodoc and embed sections`.

### Task 2: Watcher and incremental indexing

- `watch.go`: add a watch for every directory of each repo except built-in exclusions, workspace excludes, and gitignored directories (`git check-ignore`); add watches for directories created later; coalesce events per repo with a 300 ms debounce; hand the set of changed repo-relative paths to the pipeline.
- The pipeline gets `index.Paths(s, repo, paths)` that re-extracts exactly those paths (deleting rows for missing ones) and re-resolves, reusing Plan 1's machinery.
- `head.go`: every 2 s read `.git/HEAD` (handle worktrees: `.git` as a file with `gitdir:`) and the ref it points to; on change, run `index.Ensure` with no file limit and update the repo row.
- Crash isolation: each repo's work runs in its own goroutine with `recover`; a failure is logged with the repo name and retried on the next event.
- Logging: to stderr and `~/.local/state/codedoc/daemon.log` (honor `XDG_STATE_HOME`), line-oriented, rotated at 10 MB by renaming to `.1`.

- [ ] Tests on temp fixture copies: touch a file → symbol visible within 1 s; create a directory with a file → indexed; delete a file → rows gone; `git checkout -b` plus a commit on another branch then switch back → repo row branch updated and symbols match the branch. Commit `feat(daemon): watcher, debounce, branch switch detection`.

### Task 3: Socket and `codedoc daemon`

- Socket at `$XDG_RUNTIME_DIR/codedoc.sock` (fallback `~/.local/state/codedoc/codedoc.sock`). Protocol: one JSON request per line, one JSON response per line. Requests: `{"op":"status"}` → per workspace and repo: branch, commit, indexed_at, queue length, last error, next scheduled scip/autodoc run; `{"op":"fresh","workspace":"w","repo":"r"}` → `{"fresh":true|false,"pending":N}`.
- `codedoc status` uses the socket when present and adds a `daemon: running|not running` line; the CLI's freshness check asks the socket first and skips its own stat pass when the daemon reports `fresh:true` with no pending events.
- `codedoc daemon [--workspace W]` runs in the foreground until SIGINT/SIGTERM, then drains and exits 0.
- `deploy/systemd/codedoc.service`: a user unit (`ExecStart=%h/go/bin/codedoc daemon`, `Restart=on-failure`), plus README instructions (`systemctl --user enable --now codedoc`).

- [ ] Tests: daemon started in-process on a temp config answers both ops; graceful shutdown. Commit `feat(daemon): socket protocol, daemon command, systemd unit`.

### Task 4: SCIP runner and merge

- Indexer commands, run in the repo root with a 20 minute timeout, output to a temp file:
  - Go: `scip-go --output <tmp>`
  - TypeScript/JavaScript: `scip-typescript index --output <tmp>` (adds `--infer-tsconfig` when no `tsconfig.json`)
  - Python: `scip-python index . --output <tmp>`
  - Java: `scip-java index --output <tmp>`
  The language set is chosen from the repo's dominant code languages in the index. A missing binary yields a recorded, non-fatal status (`status` shows it once).
- Merge (`merge.go`), streaming with `IndexVisitor.ParseStreaming`:
  1. For each document, map its `RelativePath` to a file row; skip files whose current hash differs from the hash at the indexed commit (files changed since the SCIP run keep heuristic edges, spec 7.4). Simplest correct rule: record `repo.scip_commit = HEAD` and skip files listed by `git diff --name-only <scip_commit>` plus untracked files.
  2. Definition occurrences (`SymbolRole_Definition`): find our symbol in that file whose span contains the occurrence start line and whose name equals the occurrence text's identifier; store `scip_symbol(stable_key, scip_symbol)`.
  3. Reference occurrences: the source is the innermost symbol containing the occurrence line; the target is the symbol mapped from the SCIP symbol string (via `scip_symbol`), possibly in another repo of the workspace when that repo was also SCIP-indexed. Replace the heuristic edge at the same file and line (any confidence) with a `precise` edge (`ref_kind` `call` when the heuristic reference at that line was a call, otherwise `type`). References to external symbols (no mapping) leave heuristic edges alone.
  4. Implementations: `SymbolInformation.Relationships` with `is_implementation` produce `precise` `implements` edges.
- `codedoc scip run [--repo R]` runs indexer and merge now; the daemon schedules it per `scip.every` for repos in `scip.enabled`.

- [ ] Tests: a unit test feeding a hand-built `scip.Index` (constructed in Go, serialized with protobuf) for the Go fixture: ambiguous `svc.s.Get` becomes a precise edge to `Store.Get`; a file modified after the SCIP commit keeps its heuristic edges. An integration test that runs the real `scip-go` on the Go fixture copy, skipped when `scip-go` is not on PATH (it is installed in the Docker image). Commit `feat(scip): indexer runner and precise edge merge`.

### Task 5: Autodoc runner

`codedoc autodoc --repo R [--max-items N=40] [--max-minutes M=30] [--commit] [--allow-dirty]` per spec 11.2:

1. Refuse when the working tree is dirty unless `--allow-dirty`, which disables `--commit`.
2. Take the gaps queue for the repo (Plan 3), group by module, take groups in rank order until `--max-items`.
3. For each group: generate a run id; build the prompt (`prompt.go`): the stable keys to document, the instruction to use the `codedoc-document` skill, to read context only through `codedoc` commands, to write only through `codedoc doc set`, and to stop after the listed keys. Invoke `claude -p <prompt> --permission-mode acceptEdits --allowedTools "Bash(codedoc:*),Read" --max-turns 40 --output-format json` with env `CODEDOC_HEADLESS=1`, `CODEDOC_RUN_ID=<id>`, working directory the repo root. The binary is `claude` on PATH, overridable with `CODEDOC_CLAUDE_BIN` (tests use a stub). Parse the JSON envelope for `total_cost_usd` and `is_error`.
4. After each group: re-index the journaled files, run `doc check` on them; the group succeeds when every listed key is now `ok`. On failure, `journal.Restore(id)`. With `--commit`, commit only the journaled files: `docs(codedoc): document <module>` with the stable keys in the body.
5. Stop on limits or an empty queue; print documented, skipped, failed, cost.

Scheduler: daemon runs autodoc per `autodoc.every` / `autodoc.at` for each repo whose MCP session lock is not held (`mcpserver.Held`), serialized per repo. Manual runs ignore the lock.

- [ ] Tests with a stub `claude` script (a small Go test binary built in `TestMain`, or a shell script) that calls `codedoc doc set` for each key it finds in the prompt; paths covered: success with commit, a stub that writes a broken doc → restored and not committed, dirty tree refusal, `--allow-dirty` with the user's own uncommitted edit preserved after a failure. Commit `feat(autodoc): unattended documentation runs through claude -p`.

### Task 6: Docker test suite

The project's full test suite runs in Docker; only the host-facing commands (CLI, MCP server, daemon) run on the host because they read the user's working trees.

- `Dockerfile` (multi-stage):
  - `base`: `golang:1.27-bookworm`; install `gcc`, `git`, `libsqlite3-dev`, `curl`, `ca-certificates`, `nodejs` and `npm` (for scip-typescript), Python 3 with `pip` (for scip-python via npm, which needs node), ONNX Runtime 1.17.1 into `/usr/local/lib` and headers into `/usr/local/include` (as the earlier project's Dockerfile does), `golangci-lint` v2 (install script pinned to the version on the development machine), `scip-go` (`go install github.com/scip-code/scip-go/cmd/scip-go@latest` pinned to a tagged version), `scip-typescript` and `scip-python` (`npm i -g @sourcegraph/scip-typescript @sourcegraph/scip-python`, pinned). Set `git config --global user.email/name` for tests that commit, and `init.defaultBranch main`.
  - `test`: copies `go.mod`/`go.sum` first and runs `go mod download` for layer caching, then the source; default command `make test lint`.
  - `model` (optional build arg `WITH_MODEL=1`): downloads the nomic model into `/models` and sets `CODEDOC_MODEL_DIR=/models` so the embedding integration tests run.
- `docker-compose.yml` services: `test` (runs `make test`), `lint` (`make lint`), `integration` (runs `go test -tags "sqlite_fts5 integration" ./...` with `WITH_MODEL=1`, exercising real scip-go, scip-typescript, the ONNX embedder, the MCP server over stdio against a built binary, and the daemon), `shell`. The source is mounted read-only at `/src` and copied to a writable work dir in the entrypoint so the host tree is never modified; Go build and module caches are named volumes.
- Integration tests carry the build tag `integration` and live next to the code they cover; each checks its tool is present and fails (not skips) when run under the `integration` tag without it, so the Docker run proves the toolchain.
- Makefile: `docker-test` (`docker compose run --rm test`), `docker-lint`, `docker-integration`, `docker-all` (all three), `docker-shell`.
- `.dockerignore`: `.git`, `codedoc`, `coverage.out`, `models/`.

- [ ] Run `make docker-all` until green; fix any environment assumptions it exposes (paths under `$HOME`, `XDG_*`, missing git identity). Commit `build: docker test suite with integration toolchain`.

### Task 7: End-to-end acceptance on a real repository

A test under the `integration` tag that indexes this repository itself (copied into the container work dir) and asserts a set of answers that must hold for the real code: `find Render` finds `output.Render`; `callers` of `index.Repo` include the `index` command; `outline internal/store/store.go` lists `Open`; `config CODEDOC_DATA` finds the code reads; `overview` lists `cmd/codedoc` as an entry point; a `search` for "byte budget" returns the output package (with the model); `gaps --limit 5` returns five entries. Also time the full index and record it in the test log.

- [ ] Commit `test: end-to-end acceptance on the codedoc repository`.

### Task 8: Final README and spec sync

README sections: install, configuration (full spec 14 example), every command with one example, MCP and plugin, daemon and systemd, SCIP, autodoc, Docker test targets, architecture summary pointing at the spec. Sync the spec where implementation decisions differ (vectors as BLOBs, MCP SDK choice, anything reported by implementers). Commit `docs: complete readme and spec sync`.

## Implementation notes

This plan is historical; the spec describes the code as built. Main deviations:

- **Daemon layout and freshness.** The daemon splits into `internal/daemon` (watcher, socket, locks) and `internal/daemon/service` (catch-up, schedules, config reload). The freshness question carries root, database path and inode, build and extractor version (spec 6, 7.8, 12).
- **Daemon behavior.** The daemon:
  - takes a single-instance lock;
  - reloads the config file;
  - marks degraded repos;
  - caps the debounce at 2 s;
  - indexes in rounds of 5,000;
  - runs under `TimeoutStopSec=30` (spec 12).
- **SCIP merge.** References are matched by line and column. Earlier precise edges are revalidated. Repos are mapped in two passes, and failed runs are recorded for `status` (spec 7.4).
- **SCIP module path.** The bindings are `github.com/scip-code/scip`, and `scip-java` is not in the Docker image (spec 7.4, 16).
- **Autodoc confinement.** Autodoc runs claude with `--tools Bash,Read`, `--disallowedTools` for the editing tools, `--permission-mode dontAsk`, `--max-turns` of `10 + 8 per key` (at most 60) and `--max-budget-usd`, not `acceptEdits` and 40 turns (spec 11.2).
- **Autodoc checks.** Groups are `autodoc.group_size` keys and spend at most `autodoc.max_cost_usd`. A guard fails a group that touches anything outside its journal. Leftover journals of dead runs are restored at start. Scheduled runs also skip dirty trees (spec 11.2).
- **Docker.** The Docker image does not install `libsqlite3-dev`: go-sqlite3 bundles SQLite (README).
