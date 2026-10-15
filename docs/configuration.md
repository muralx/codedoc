# Configuration

`~/.config/codedoc/config.yaml` (`$XDG_CONFIG_HOME/codedoc/config.yaml`;
`CODEDOC_CONFIG` names another file). The example below uses every key:

```yaml
embed:
  model_dir: ~/src/codedoc/models        # nomic-embed-text-v1.5; relative paths resolve against this file's directory
workspaces:
  myapp:
    repos:
      - path: ~/src/myapp                # relative paths resolve against this file's directory
      - path: ~/src/myapp-config
        name: config                     # default: the directory name; must be unique in the workspace
        languages: [yaml, properties, shell]   # index only these (default: all)
    exclude: ["**/generated/**"]         # doublestar globs, relative to each repo root
    scip:
      enabled: [myapp]                   # repos whose SCIP indexers the daemon runs
      every: 6h                          # durations: Go syntax, or whole days/weeks (2d, 1w)
    autodoc:
      every: 24h
      at: "02:00"                        # HH:MM, for intervals of a day or longer
      max_items: 40                      # gaps per scheduled run (absent or 0: 40)
      commit: true
      group_size: 5                      # keys per claude run (0 means 5)
      max_cost_usd: 1.00                 # spend cap per claude run (0 means 1.00)
      model: sonnet                      # model of the claude runs (absent: sonnet)
    upkeep:
      enabled: true                      # document what a branch changed, after the commit (default false), see Documentation upkeep
      quiet: 5m                          # no file event for this long (absent or 0: 5m)
      max_items: 20                      # symbols and modules per run (absent or 0: 20)
      commit: true                       # one docs commit per run (default true)
      first_run_commits: 20              # commits looked at on a branch never documented (absent or 0: 20)
      max_cost_usd: 1.00                 # spend cap per run (absent or 0: 1.00)
      daily_cost_usd: 5.00               # spend cap per workspace and day (absent or 0: 5.00)
      model: sonnet                      # model of the claude runs (absent: sonnet)
    deps:
      enabled: true                      # index the third-party packages the code imports (default true), see Dependencies
      include: ["eslint*", zod-to-json-schema]  # index although no source file imports them; doublestar patterns on the name
      exclude: ["@types/react"]          # never index, do not list
  personal:
    repos:
      - path: ~/src/notes
```

Loading the file follows these rules:

- **Validation.** Loading is strict. An unknown key fails with the file
  and line to fix. A bad value fails too, naming the workspace and key:
  a malformed glob, a duplicate repo name or path, an unknown language or
  `scip.enabled` repo, an `at` that is not `HH:MM`, a `quiet` that is no
  duration, or a negative number.
- **One workspace per directory.** A directory belongs to at most one
  workspace. codedoc picks the workspace of the repo containing the
  current directory; `--workspace` overrides that.
- **Isolation.** Queries never cross workspaces.
- **`languages`** takes language names or families (`go`, `ts`,
  `typescript`, `javascript`, `py`, `python`, `java`) and config formats
  (`yaml`, `json`, `xml`, `properties`, `env`, `hcl`, `shell`,
  `template`). Files of no language (manifests, READMEs) are always read.
- **Never indexed:** `vendor/`, `node_modules/`, `testdata/` and `.git/`
  at any depth; `build/`, `dist/` and `target/` at the repo root or next to
  a build manifest (`go.mod`, `package.json`, `pom.xml`, `build.gradle*`,
  `pyproject.toml`, `setup.py`, `Cargo.toml`); dependency lock files
  (`pnpm-lock.yaml`, `package-lock.json`, `npm-shrinkwrap.json`,
  `yarn.lock`, `bun.lockb`, `bun.lock`, `go.sum`, `go.work.sum`,
  `poetry.lock`, `uv.lock`, `Pipfile.lock`, `Cargo.lock`,
  `composer.lock`, `Gemfile.lock`, `gradle.lockfile`); files over 2 MB and
  binary files; anything git ignores. An index that already holds rows
  for such a file drops them on the next `codedoc index` or query.

## Environment variables

| Variable | Default | Effect |
|---|---|---|
| `CODEDOC_CONFIG` | `$XDG_CONFIG_HOME/codedoc/config.yaml` (`~/.config`) | config file |
| `CODEDOC_DATA` | `$XDG_DATA_HOME/codedoc`, else `~/.local/share/codedoc` | databases (`<workspace>.db`), session locks, daemon lock |
| `CODEDOC_MODEL_DIR` | `embed.model_dir` | embedding model directory; wins over the config |
| `CODEDOC_ONNXRUNTIME_LIB` | `/usr/local/lib/libonnxruntime.so` | ONNX Runtime shared library |
| `CODEDOC_EMBED_THREADS` | half the logical CPUs, at most 12 | threads of one embedding model call |
| `CODEDOC_EMBEDDER` | unset | `off` disables embeddings for one command. `fake` is a test switch: its vectors carry no meaning |
| `CODEDOC_FRESH_MAX_FILES` | `200` | most changed files a query re-extracts before answering |
| `CODEDOC_GOMODCACHE` | `GOMODCACHE`, else `go env GOMODCACHE`, else `~/go/pkg/mod` | the Go module cache the [dependency index](dependencies.md) reads |
| `CODEDOC_LOCK_TIMEOUT` | `60s` | how long a writer waits for the database write lock (`2m`, or seconds) |
| `CODEDOC_SCIP_GO`, `_TS`, `_PY`, `_JAVA` | `scip-go`, `scip-typescript`, `scip-python`, `scip-java` | SCIP indexer binaries |
| `CODEDOC_CLAUDE_BIN` | `claude` | the binary `autodoc` and upkeep run |
| `CODEDOC_HEADLESS` | unset | set by autodoc and upkeep for their `claude -p` runs: an MCP server started under it takes no session lock |
| `CODEDOC_RUN_ID` | unset | set by autodoc and upkeep: `doc set` journals original files under this run |

## Files

| Path | Content |
|---|---|
| `~/.local/share/codedoc/<workspace>.db` (`$XDG_DATA_HOME/codedoc`; `CODEDOC_DATA` wins) | the index; everything in it is derived and can be rebuilt (`index --rebuild` also forgets which commits upkeep documented) |
| `~/.local/share/codedoc/deps.db` | the public API of third-party packages, shared by every workspace (see [Dependencies](dependencies.md)); derived too |
| `~/.local/share/codedoc/<workspace>.locks/<repo>` | session locks held by MCP servers |
| `~/.local/share/codedoc/daemon.lock` | single-daemon lock |
| `$XDG_RUNTIME_DIR/codedoc.sock`, else `~/.local/state/codedoc/codedoc.sock` | daemon socket |
| `~/.local/state/codedoc/daemon.log` (`$XDG_STATE_HOME`) | daemon log, rotated at 10 MB |
| `~/.local/state/codedoc/runs/<run-id>/` | autodoc and upkeep write journals |
| `~/.local/state/codedoc/feedback.jsonl` | the reports filed with `codedoc feedback`, one JSON object per line, at most 4 MB |
