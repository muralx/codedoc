# Daemon and background work

The daemon keeps the index fresh as files change, and runs the work
that happens outside a session: documentation upkeep after each commit,
scheduled SCIP runs and scheduled autodoc.

## Daemon

`codedoc daemon` keeps every configured workspace fresh. It runs in the
foreground until SIGINT or SIGTERM, then finishes the current round and
exits 0. It does the following:

- **Startup.** Takes the single-instance lock in the data directory and
  listens on the socket before doing anything else, so a second daemon
  exits at once. Then brings each repo up to date, logging progress per
  repo.
- **Watching.** Watches the working trees with fsnotify and reindexes
  exactly the files that change, new and removed directories included.
  Events are debounced: 300 ms of quiet, at most 2 s after the first.
  Large batches are indexed in rounds of up to 5,000 files.
- **Branch switches.** Polls `.git/HEAD` and refs every 2 s; a branch
  switch or new commit triggers a full freshness pass.
- **Embeddings.** Keeps the embedding model loaded, so vectors follow
  every edit.
- **Socket.** Serves status and freshness: `codedoc status` adds the
  queue, the next SCIP and autodoc runs, the upkeep state and the last
  error per repo.
  While the daemon reports a repo fresh, queries skip their own stat
  pass. A query trusts the daemon only for the same repo directory,
  database, codedoc build and extractor version.
- **Config reload.** Reloads `config.yaml` when its content changes. A
  file that does not load is logged, and the running configuration stays.
- **Degraded repos.** Marks a repo `degraded`, and never fresh, while it
  may miss changes. Queries then run their own check. Causes:
  - a directory it cannot watch, such as when inotify watches run out
    (raise `fs.inotify.max_user_watches`, e.g.
    `sudo sysctl fs.inotify.max_user_watches=524288`, and restart);
  - a watch error or event-queue overflow (followed by a rescan that
    clears it);
  - a repo directory that is gone (`status` shows `missing`, the index is
    kept, and watching resumes when the directory reappears).
- **Schedules.** Runs SCIP and autodoc on the workspace schedules.
- **Upkeep.** Documents what a branch changed once its tree is clean
  and quiet, see [Documentation upkeep](#documentation-upkeep).
- **Dependencies.** Indexes the packages `deps.db` lacks and embeds
  them in the background.
- **Logging.** Logs to stderr and `~/.local/state/codedoc/daemon.log`.

A failure in one repo does not stop the others.

Here is a run on the workspace `fixture-deps`, with
`XDG_RUNTIME_DIR=/run/user/1000`; the line before the last follows an
edit to `src/main.ts`, the last one a SIGINT:

```
$ codedoc daemon --workspace fixture-deps
2026-10-11T01:48:11+01:00 daemon: listening on /run/user/1000/codedoc.sock
2026-10-11T01:48:11+01:00 daemon: serving 1 workspace(s)
2026-10-11T01:48:11+01:00 catch-up fixture-deps: 2 repo(s)
2026-10-11T01:48:11+01:00 catch-up fixture-deps/deps-app (1/2): up to date, 0 file(s) changed in 7ms
2026-10-11T01:48:11+01:00 catch-up fixture-deps/deps-go (2/2): up to date, 0 file(s) changed in 5ms
2026-10-11T01:48:15+01:00 fixture-deps/deps-app: indexed 1 path(s) in 31ms
2026-10-11T01:48:21+01:00 daemon: stopped
```

The socket path may be at most 107 bytes; the daemon names a shorter
`XDG_RUNTIME_DIR` as the remedy when it is longer.

To run it as a systemd user service:

```sh
mkdir -p ~/.config/systemd/user
cp deploy/systemd/codedoc.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now codedoc
journalctl --user -u codedoc -f
```

The unit runs `%h/go/bin/codedoc daemon`; edit `ExecStart` if codedoc
lives elsewhere. Its `PATH` includes `~/.local/bin` so scheduled autodoc
and upkeep find `claude`. Uncomment `CODEDOC_MODEL_DIR` or `CODEDOC_CONFIG` as
needed. `TimeoutStopSec=30` gives the daemon time to finish a round.

## Documentation upkeep

A session writes the doc comment of the code it writes. What is still
stale or missing after a commit is filled by the daemon, outside the
session: the session never checks doc states and is never told to.
Upkeep is off by default; turn it on per workspace:

```yaml
workspaces:
  myapp:
    upkeep:
      enabled: true        # default false
      quiet: 5m            # no file event for this long (absent or 0: 5m)
      max_items: 20        # symbols and modules per run (absent or 0: 20)
      commit: true         # default true: one docs commit per run
      first_run_commits: 20  # commits looked at on a branch never documented
      max_cost_usd: 1.00   # per run (absent or 0: 1.00)
      daily_cost_usd: 5.00 # per workspace and day (absent or 0: 5.00)
      model: sonnet        # model of the claude runs (absent: sonnet)
```

**When.** Every 30 seconds the daemon checks each repo. A run starts
when the workspace has spent less than `daily_cost_usd` today, no file
changed for `quiet`, HEAD is on a branch, the working tree is clean
(everything committed, no untracked file), and there are candidates.

**What.** The candidates are the symbols and modules the branch's own
commits changed since the last upkeep on that branch, whose doc is stale
or missing and that `gaps` would list. The branch's own commits are
those not reachable from the default branch; on a branch never
documented, the newest `first_run_commits` of them. State is kept per
repo and branch, so switching branches loses nothing.

**How.** The autodoc runner documents the candidates in rank order
through headless `claude -p` runs (see [Autodoc](#autodoc) for the
safety model), at most `max_items` per run, as one unit. With
`commit: true` the files it wrote become one commit on the same branch,
`docs: document N changed symbol(s)`, with the keys in the body. Upkeep
never pushes. With `commit: false` the docs stay in the working tree,
and upkeep waits until you commit them.

**Interruption.** If a file changes or HEAD moves while a run is going,
the run is undone as a whole from its journal and retried after the next
quiet time. Your change is left exactly as it is.

**Cost.** The claude runs use `model` (`sonnet` unless set: writing doc
comments does not need the session's larger model). One run may spend
`max_cost_usd`, shared by its claude runs: none of them has a cap of its
own, and `autodoc.max_cost_usd` does not apply to upkeep. What it spent is added to
the workspace's spend of the day; at `daily_cost_usd` upkeep stops until
local midnight. A claude run that ended without reporting its cost
counts at the budget it was given.

`codedoc status` shows the state per repo. Without the daemon nothing
runs, and it says so:

```
$ codedoc status
deps-app@f4292bb deps-go@a414542
== status (2)
deps-app  main@f4292bb files:6 symbols:8 config-keys:0 unresolved-config:0 parse-errors:0 vectors:0/7 dirty:0 indexed:2026-10-11T00:47:08Z scip:never upkeep:needs-daemon
deps-go  main@a414542 files:2 symbols:4 config-keys:0 unresolved-config:0 parse-errors:0 vectors:0/4 dirty:0 indexed:2026-10-11T00:47:08Z scip:never upkeep:needs-daemon
daemon: not running; upkeep needs the daemon (codedoc daemon): nothing is documented without it
```

With the daemon, a few seconds after its start:

```
deps-app  main@f4292bb files:6 symbols:8 config-keys:0 unresolved-config:0 parse-errors:0 vectors:0/7 dirty:0 indexed:2026-10-11T00:47:08Z scip:never queue:0 upkeep:on (waiting for quiet) pending:0
```

| Field | Meaning |
|---|---|
| `upkeep:on`, `upkeep:off` | enabled for the workspace or not |
| `(...)` after `on` | what holds it back: `waiting for quiet`, `uncommitted changes`, `daily cap reached`, `HEAD is not on a branch`, `no commit yet`, `not a git repository`, `not indexed` |
| `upkeep:running` | a run is going |
| `pending:N` | the candidates the last check counted |
| `last-upkeep:` | when a run last finished |
| `upkeep-error:` | why the last check or run failed or was undone; cleared by a run that finishes |

JSON carries the same under `extra.upkeep` of each repo entry.

## SCIP precise edges

`codedoc scip run [--repo R]` first brings the index up to date. It then
runs the SCIP indexer of every language family the repo uses:

| Family | Indexer |
|---|---|
| Go | `scip-go` |
| TS/JS | `scip-typescript index`, with `--infer-tsconfig` when there is no `tsconfig.json` |
| Python | `scip-python index .` |
| Java | `scip-java index` |

`CODEDOC_SCIP_*` names other binaries, and each run times out after 20
minutes. The merge works at the repo's HEAD commit:

- Files changed since that commit (committed, uncommitted or untracked)
  keep their heuristic edges until the next run.
- Each reference occurrence is matched to the extracted reference at the
  same line and column. It gets one `precise` edge to the symbol SCIP
  names, in this repo or another repo of the workspace. All repos of a
  run are mapped before any is merged, so their order doesn't matter.
- A precise edge from an earlier run that this run does not confirm is
  dropped, and its reference is resolved again by the heuristic tiers.

```
$ codedoc scip run
warning: scip: go indexer "scip-go" not found; go files keep heuristic edges
warning: scip: ts indexer "scip-typescript" not found; ts files keep heuristic edges
warning: scip: py indexer "scip-python" not found; py files keep heuristic edges
warning: scip: java indexer "scip-java" not found; java files keep heuristic edges
8 repos (go-lib@0a93839 +7)
== scip (8)
go-lib  go: missing indexer scip-go
go-app  go: missing indexer scip-go
ts-domain  ts: missing indexer scip-typescript
ts-app  ts: missing indexer scip-typescript
py-lib  py: missing indexer scip-python
java-lib  java: missing indexer scip-java
cfg-repo  no language with a SCIP indexer
doc-repo  no language with a SCIP indexer
```

A missing indexer is a warning, not an error. `status` reports the
repo's SCIP state once, instead of every query warning about it:

| State | Meaning |
|---|---|
| `scip:<commit>` | merged at that commit |
| `scip:missing scip-go` | the indexer isn't installed |
| `scip:failed(<date>)` | the last run failed; shown until a run succeeds |
| `scip:never` | no run yet |

The daemon runs the repos in `scip.enabled` every `scip.every`; a repo
never merged runs at daemon start.

## Autodoc

`codedoc autodoc --repo R` documents a repo's top gaps through headless
Claude Code runs. It is the manual way, and the runner
[upkeep](#documentation-upkeep) uses after each commit:

1. **Dirty trees.** It refuses a dirty working tree unless you pass
   `--allow-dirty`, which disables `--commit`.
2. **Grouping.** It takes the top `--max-items` gaps (default 40) and
   groups them by module, at most `autodoc.group_size` keys per group.
3. **One run per group.** Each group gets one `claude -p` run from the
   repo root, in its own process group, with this codedoc first on
   `PATH`. The prompt names the stable keys and says what a good doc
   holds. The agent reads through `codedoc` queries and writes every doc
   of the group with one `codedoc doc set --batch`: no output and exit
   status 0 are the success, and a line per record that failed names
   what to set again.
4. **Checks.** After each group, the touched files are reindexed and
   checked. A group succeeds when all its keys are `ok`. With `--commit`,
   the group's files are committed as `docs(codedoc): document <module>`,
   with the keys in the body. Autodoc never pushes.
5. **Limits and summary.** It stops at `--max-items`, at
   `--max-minutes` (default 30) or when the queue is empty. It then
   prints the counts documented, skipped and failed, and the cost from
   claude's JSON. A run that crashed or timed out before reporting is
   counted as unknown cost.

**Safety model.** The agent is confined in layers, and the runner checks
its work afterwards:

- **Tools.** claude runs with
  `--tools Bash,Read --allowedTools "Bash(codedoc:*),Read" --disallowedTools "Edit,Write,MultiEdit,NotebookEdit" --permission-mode dontAsk`.
  The editing tools don't exist in the session, and every call that is
  not pre-approved is denied without a prompt.
- **Limits.** `--max-turns` is `10 + 8 × keys` (at most 60), and
  `--max-budget-usd` is `autodoc.max_cost_usd` (default 1.00).
- **Journal.** Every `doc set` under the run journals the original bytes
  of a file before the run first changes it, in
  `~/.local/state/codedoc/runs/<id>/`.
- **Guard.** Before each group, the runner records `git status` with
  content hashes, HEAD and every symbol's doc hash. A group fails if the
  agent:
  - changed a path outside the journal,
  - journaled a file outside the repo,
  - or changed the doc of a key outside the group.
- **Restore.** A failed group's journaled files are restored from the
  journal, never with `git checkout`. Other files that were clean go back
  to HEAD, with a copy kept in the journal.
- **Your own edits.** A file you edit during the run is kept and
  reported. If the agent changed a file that was already dirty, or moved
  HEAD, the run stops and names them.
- **Crash recovery.** Every autodoc run, manual or scheduled, first
  restores the journals of runs whose process died.

Scheduled runs (`autodoc.every`/`at`) are serialized per repo. They skip
a repo whose tree is dirty or whose session lock an MCP server holds. A
manual run ignores the lock. Set `CODEDOC_CLAUDE_BIN` to use another
claude binary.

```
$ codedoc autodoc --repo go-lib
codedoc: autodoc: working tree has uncommitted changes (use --allow-dirty)
```
