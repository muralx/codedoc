---
name: codedoc-maintain
description: Use when asked about documentation coverage or debt in a codedoc workspace, turning on or checking documentation upkeep, running unattended documentation (autodoc), validating docs before a release, adding a repository to a codedoc workspace, indexing or embedding third-party dependencies, enabling SCIP precise edges, setting up the codedoc daemon, or when codedoc reports fresh:no, a missing workspace, or a package that is not indexed.
---

# codedoc maintain

Operations around the index. To read code use the codedoc-lookup skill;
to write a doc, the codedoc-document skill.

## Writes are silent

`codedoc index`, `codedoc doc set`, `codedoc deps index`,
`codedoc deps prune` and `codedoc feedback` print nothing when they
succeed: exit status 0 is the result. A failure is one line on stderr
saying what failed and what to do. Add `--verbose` to see what was done.
Do not run a second command to check a silent write.

Exit status of every command: 0 answered (also the nearest candidates of
a name that matched nothing), 1 nothing to offer or a failed write,
2 usage, 4 internal error. 3 only with `--strict`, which makes an
ambiguous address fail with its candidates.

## Documentation debt

```bash
codedoc gaps --limit 20               # ranked: stale first, then by score
codedoc gaps --kind module            # missing | stale | module
codedoc gaps github.com/acme/auth/internal/services
codedoc doc check --repo auth         # ok/stale/missing counts, placement problems; always exits 0
```

`gaps` prints stable keys: they are what `codedoc doc set` takes. Tests,
generated files, trivial accessors and methods of documented interfaces
are left out. To write many docs in one call:

```bash
codedoc doc set --batch <<'DOCS'
=== store/store.go#MemStore.Get
Get returns the value stored under key.
=== go-lib:store/
Package store keeps values by key.
DOCS
```

Every record that can be written is written. Each one that cannot is one
line on stderr, `<address>: <what>; <what to do>`, and the exit status
is then 1. `doc set` never writes to a near or a best candidate.

## Upkeep: docs after each commit

The daemon documents what a branch changed, outside the session. Off by
default; per workspace in `~/.config/codedoc/config.yaml`:

```yaml
workspaces:
  myapp:
    upkeep:
      enabled: true
      quiet: 5m            # no file event for this long
      max_items: 20        # per run
      commit: true         # one commit "docs: document N changed symbol(s)"; never pushed
      first_run_commits: 20
      max_cost_usd: 1.00   # per run
      daily_cost_usd: 5.00 # per workspace and day
```

It runs when the tree is clean, quiet and on a branch, on the symbols
the branch's own commits changed whose doc is stale or missing. A run
is undone as a whole if a file changes meanwhile. `codedoc status`
shows per repo `upkeep:on`, in parentheses what holds it back
(`waiting for quiet`, `uncommitted changes`, `daily cap reached`),
`upkeep:running`, `pending:N`, `last-upkeep:` and `upkeep-error:`.
`upkeep:needs-daemon` means nothing runs: start `codedoc daemon`.

## Autodoc: the manual run

```bash
codedoc autodoc --repo auth --max-items 20 --max-minutes 30 --commit
```

Takes the repo's top gaps by module and runs headless `claude -p` per
group. Refuses a dirty tree (`--allow-dirty` runs but disables
`--commit`). A group whose keys are not all `ok` is rolled back from a
journal. `--commit` commits `docs(codedoc): document <module>`; it never
pushes. Ask the user first: it spends model budget and edits files.

## Workspace config

```yaml
workspaces:
  myapp:
    repos:
      - path: ~/src/myapp           # name defaults to the directory name
      - path: ~/src/myapp-config
        name: config
        languages: [yaml, properties, shell]
    exclude: ["**/generated/**"]
    deps:
      include: ["eslint*"]              # index although no source file imports it
      exclude: ["@types/react"]
    scip:
      enabled: [myapp]
      every: 6h
```

Then `codedoc index` (`--rebuild` recreates a broken database). A repo
that cannot be indexed is one line on stderr; the others are indexed and
the exit status is 1.

## Dependencies

Only the packages a source file imports are indexed. `codedoc deps`
lists every declared package with its state: `indexed` (with
`symbols:N vectors:N/M`), `pending`, `missing` (not installed), `failed`,
`skipped` (a workspace package) or `unused` (declared, imported by no
source file, not indexed).

- A package that is `unused` but wanted: name it under `deps.include`,
  then `codedoc deps index`.
- `pending`, or `deps: N pending` on an answer: `codedoc deps index`.
- `vectors:0/M`: `codedoc deps index --embed` embeds the packages for
  search by meaning (the daemon does it in the background).
- `codedoc deps prune --dry-run` lists the versions no workspace uses;
  `codedoc deps prune` deletes them.

## SCIP precise edges

Needs the indexer on PATH (`scip-go`, `scip-typescript`, `scip-python`,
`scip-java`). Add the repo under `scip.enabled`, then
`codedoc scip run --repo myapp`. `codedoc status` shows `scip:<commit>`,
`scip:missing <indexer>`, `scip:failed(<date>)` or `scip:never`.

## Daemon and freshness

Without the daemon each query first re-extracts up to 200 changed files,
so `(index behind: ...)` in `codedoc status` after an edit is normal.
Beyond 200 an answer says `fresh:no`: run `codedoc index`.

```bash
codedoc daemon                         # foreground; --workspace W serves only W
cp deploy/systemd/codedoc.service ~/.config/systemd/user/
systemctl --user daemon-reload && systemctl --user enable --now codedoc
```

The daemon watches the repos, follows branch switches, runs upkeep,
SCIP and scheduled autodoc, and reloads an edited config.
`codedoc status` ends with `daemon: running` or `daemon: not running`.
`codedoc feedback --list` prints the answers agents reported as wrong
(`codedoc feedback --clear` once handled).
