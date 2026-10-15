# Development

How to build and test codedoc, and how to measure what it costs and
saves in real sessions. The package layout and the design rules are in
[Architecture](architecture.md).

## Tests

```sh
make test     # race detector; fails below 90% statement coverage of internal/
make lint     # gofmt, go vet, golangci-lint (v2)
```

The whole suite also runs in Docker, including the integration tests
that need real toolchains:

```sh
make docker-build        # build the codedoc-test and codedoc-integration images
make docker-test         # make test
make docker-lint         # make lint
make docker-integration  # go test -tags "sqlite_fts5 integration" ./...
make docker-all          # the three above
make docker-shell        # a shell in the test image
```

**The image** (`Dockerfile`) has, all at pinned versions:

- Go 1.27.1 and gcc;
- ONNX Runtime 1.17.1;
- golangci-lint v2.14.0;
- Node.js with `scip-typescript`, `scip-python` and pnpm;
- `scip-go`. `scip-java` is not installed.

**The integration image** bakes the model into `/models` (`WITH_MODEL=1`
by default; set `WITH_MODEL=0` to skip the download). It also holds
`testdata/realdeps` installed for real: `pnpm install --frozen-lockfile`
of `npm-app` and `go mod download` of `go-app` into a module cache of
its own, both under `/opt/realdeps` (`CODEDOC_REALDEPS`). The dependency
integration tests read that install, so a test run fetches nothing; the
lockfiles (`pnpm-lock.yaml`, `go.sum`) pin every version.

**Isolation.** The source is mounted read-only at `/src` and copied into
`/work`, so a run never writes to your checkout. Integration tests carry
the `integration` build tag. They fail rather than skip when their tool
is missing; only the tests over the real dependency install are skipped
where `CODEDOC_REALDEPS` is not set, as with `make integration` on a
host.

To run the model tests on the host:

```sh
make download-model
CODEDOC_MODEL_DIR=$PWD/models make test
```


## Measuring

Two scripts under `scripts/usage/` measure what codedoc costs and saves
in real sessions. Both read the JSONL transcripts Claude Code keeps for
a repository (`~/.claude/projects/<repo path>`), never write to them,
use the standard library only, and print markdown tables. Each binary
they run gets an index, config and state of its own under `--env-dir`
(a fresh temporary directory by default): your real index is never
touched.

**`usage.py`** reports how a project's sessions used codedoc: calls by
command, the shell reads made beside them (`grep`, `sed`, `cat`, Read),
episodes of paging through a file for one symbol, dead ends, result
sizes, the tokens each result cost over the turns it stayed in context,
the skills that loaded, and the fixed cost of the plugin.

```sh
python3 -I scripts/usage/usage.py --repo ~/src/web-app --no-measure
python3 -I scripts/usage/usage.py --repo ~/src/web-app --old /path/to/old/codedoc --new ./codedoc
```

With `--old` and `--new` it also measures counterfactuals: what plain
shell would have returned for the lookups made, and what codedoc would
have returned for the shell reads made. `--since` and `--until` bound
the calls counted (ISO UTC), `--plugin DIR` sizes the skills of a
source tree.

**`replay.py`** reruns what was recorded against two builds and prints
bytes and calls for each:

```sh
python3 -I scripts/usage/replay.py --repo ~/src/web-app --old /path/to/old/codedoc --new ./codedoc
```

- Part A reruns every recorded codedoc lookup on both binaries.
- Part B maps each shell read (a whole file, a paged read, a search, a
  listing) to the codedoc commands that serve it, through a profile per
  build (`profiles.py`), and compares the bytes with what the shell
  returned.
- Part C is the fixed cost: the size of every skill and agent of the
  plugin and of the MCP `tools/list`.
- The last block prints the acceptance numbers of the
  [efficiency spec](history/specs/2026-10-11-codedoc-efficiency-design.md),
  section 10, each with PASS or FAIL.

`--old` alone records a baseline; `--json FILE` saves every measured
item and `--baseline FILE` loads the old side from such a file.
`--details` adds one row per shell read.
