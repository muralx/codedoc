# codedoc Plan 3: Documentation model, write-back, gaps, history, overview

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Track whether every symbol and module is documented and whether its doc is stale, write docs back into source per language, rank documentation gaps, and answer history and onboarding questions (`changes`, `history`, `at`, `overview`).

**Architecture:** Staleness is computed in the pipeline from body and doc hashes (spec 7.5), seeded from git blame for symbols seen for the first time. Module docs come from the per-language package doc sources. Doc writers in `internal/docs` replace byte ranges using spans from the index, re-parse to verify, and journal originals for revert. Spec: sections 7.5, 7.6, 9 (`doc set`, `doc check`, `gaps`, `changes`, `history`, `at`, `overview`), 10, 11.1, 17 steps 6 and 7.

**Conventions:** as Plans 1 and 2.

---

## File structure

```
internal/gitmeta/blame.go            per-file line → commit time, uncommitted lines = now
internal/gitmeta/history.go          git log -L for a span, git show ref:path, merge-base helpers
internal/docs/state.go               staleness rules (pure functions)
internal/docs/moduledoc.go           module doc discovery per language
internal/docs/write/writer.go        Writer interface, registry, verify-and-revert, journal
internal/docs/write/golang.go, ts.go, python.go, java.go   per-language placement and formatting
internal/docs/write/journal.go       CODEDOC_RUN_ID journal under ~/.local/state/codedoc/runs/<id>/
internal/docs/gaps.go                ranking (spec 11.1)
internal/query/doc.go                doc set, doc check, gaps
internal/query/changes.go, history.go, at.go, overview.go
```

---

### Task 1: Blame and history primitives

- `gitmeta.Blame(dir, path) (map[int]int64, error)`: runs `git blame --porcelain -- path`, returns line → committer time; lines not committed (`0000000...` commit) map to `math.MaxInt64` ("newer than any commit", spec 7.5). Untracked files return `ErrUntracked`.
- `gitmeta.LogRange(dir, path, start, end int, limit int) ([]Commit, error)`: `git log -L start,end:path --format=%H%x00%ct%x00%an%x00%s --no-patch`; parse into `Commit{Hash, At, Author, Subject}`.
- `gitmeta.Show(dir, ref, path) ([]byte, error)` and `gitmeta.ResolveSince(dir, since string) (string, error)`: a ref is returned as its full hash; a duration (`3d`, `12h`, `2w`) resolves to the newest commit older than now minus the duration (`git rev-list -1 --before=<date> HEAD`).
- `gitmeta.RecentCommits(dir, n)`.

- [ ] Tests on a temp repo with known commits (use fixed `GIT_*_DATE` values); commit `feat(gitmeta): blame, log -L, show, since resolution`.

### Task 2: Staleness in the pipeline

Pure rules in `internal/docs/state.go`:

```go
// State is the documentation state of a symbol or module.
type State string

const (
	Missing State = "missing"
	Stale   State = "stale"
	OK      State = "ok"
)

// Prior is what the index knew about a symbol before this run; nil when new.
type Prior struct{ DocHash, BodyHash, DocBodyHash string }

// Next computes doc_body_hash and state for a symbol (spec 7.5).
// seed is consulted only when prior is nil and the symbol has a doc.
func Next(prior *Prior, docHash, bodyHash string, seed func() Seed) (docBodyHash string, st State)
```

`Seed` reports whether blame says the body is newer than the doc (`Stale` with `docBodyHash = ""`), or the doc is at least as new (`OK` with `docBodyHash = bodyHash`), or the file is untracked (`OK`). Body hash: Plan 1 hashes the body with whitespace removed; confirm it excludes the doc (Go/TS/Java docs are outside the body; Python docstrings are excluded by Plan 2) and that signature changes are included: change the body hash input to "signature text + body text, whitespace removed" for all languages, and update goldens.

Pipeline: before `ReplaceFileSymbols`, load prior rows by stable key for the file; compute `doc_body_hash` and `doc_state` per symbol; blame the file lazily (once per file, only if some new documented symbol needs seeding).

- [ ] Table tests for `Next`; pipeline tests on a temp fixture copy with real commits: change a body → `stale`; change the doc → `ok`; brand-new documented symbol in a file whose body lines are newer in blame than its doc lines → `stale`; untracked file → `ok`; no doc → `missing`.
- [ ] Commit `feat(docs): staleness model and blame seeding`.

### Task 3: Module docs

Module doc sources (spec 7.2 table, 10 table):

| Language | Source, in priority order |
|---|---|
| Go | package comment in `doc.go`; else the first package comment found in the directory's files (sorted by name) |
| TS/JS | `README.md` in the module directory (the text under the first heading, up to the next heading); else a leading `/** */` in `index.ts`/`index.js` |
| Python | the `__init__.py` docstring for packages; the module docstring for file modules |
| Java | Javadoc in `package-info.java` |

Store `module.doc_text`, `doc_source` (repo-relative path), and `doc_state` (`ok` when a doc exists, else `missing`; module staleness is not tracked in v1). Feed `module_fts`.

- [ ] Tests per language on fixtures (add `doc.go` to `go-lib/store`, a `README.md` to `ts-domain/src`); commit `feat(docs): module documentation discovery`.

### Task 4: Doc writers

`internal/docs/write`:

```go
// Target identifies what to document.
type Target struct {
	Repo      string // absolute repo root
	Path      string // repo-relative file of the symbol, or module directory
	Lang      string
	Symbol    *store.Symbol // nil for a module doc
	ModuleDir string        // set for module docs
}

// Writer places doc text for one language.
type Writer interface {
	// Plan returns the file to change and its new content. It never writes.
	Plan(t Target, text string, src []byte) (path string, out []byte, err error)
}

// Apply runs Plan, journals the original file (when CODEDOC_RUN_ID is set),
// writes the result, re-parses it with the language extractor, and verifies
// that the symbol (same qualified name) now carries exactly the given doc.
// On verification failure it restores the original bytes and returns an error.
func Apply(t Target, text string) (Result, error)
```

Placement and formatting per spec section 10:

| Language | Symbol doc | Module doc |
|---|---|---|
| Go | `// ` lines above the declaration, first word the symbol name when the text does not already start with it, wrapped at 80 columns, indented like the declaration | package comment in `doc.go`, created as `// Package x ...\npackage x\n` when absent; an existing package comment elsewhere is left alone |
| TS/JS | `/** ... */` above (above the `export` keyword when exported), ` * ` prefixed lines, wrapped at 80 | `README.md` in the directory: replace or create a `## Purpose` section |
| Python | docstring as the first body statement with triple double quotes, indented one level deeper than the `def`/`class` | `__init__.py` docstring (file created when absent); for a file module, its module docstring |
| Java | `/** ... */` above the declaration including its annotations, existing `@param`/`@return`/`@throws` tags preserved when the new text has none | `package-info.java` (created with the package declaration when absent) |

Existing docs are replaced using their stored span (`doc_start_line`..`doc_end_line`), never by text search. Paragraph breaks in the input text are preserved; trailing whitespace is trimmed. The writer refuses generated files (`file.generated`).

Journal: when `CODEDOC_RUN_ID` is set, before the first modification of a file in that run, copy it to `~/.local/state/codedoc/runs/<id>/<repo-name>/<repo-relative path>` (honor `XDG_STATE_HOME`); `journal.Restore(id)` copies every journaled file back and removes the journal; `journal.Files(id)`.

- [ ] Round-trip tests for every language at symbol and module level (create, replace, multi-paragraph, existing tags preserved for Java), a refusal test for generated files, a verification failure test (inject a writer that produces broken code; expect restore), and journal tests.
- [ ] Commit `feat(docs): per-language doc writers with verify, revert and journal`.

### Task 5: `doc set` and `doc check`

- `codedoc doc set <symbol-or-module> [--text T | stdin]`: resolve the address (modules by identity or `repo:dir/` path ending in `/`), call `write.Apply`, re-index the touched file synchronously, print the new doc span and state.
- `codedoc doc check [scope]`: re-index scope, then report per repo counts of `ok`/`stale`/`missing` for symbols and modules, plus problems: docs that do not start with the symbol name in Go (warning), Javadoc blocks detached from declarations by a blank line, Python docstrings with single quotes. Exit 0 always; problems are part of the answer.

- [ ] Tests and e2e; commit `feat(query): doc set and doc check`.

### Task 6: Gaps

`internal/docs/gaps.go` implements spec 11.1 exactly:

`score = visibility × (1 + ln(1 + fan_in)) × kind_weight × recency`, visibility 3/1.5/1, kind weights module 4, interface/trait 3, class/struct 2, function/method 1, field 0.5, const/var/type/enum 1, recency 1.5 when the file's last commit (or uncommitted change) is within 30 days. Stale outranks missing at equal score (sort by state first: stale, then missing; then by score descending). Exclusions: tests, generated files, trivial accessors (a body consisting of a single return of a field or of a single assignment to a field), symbols shorter than three lines unless `Public`, methods of interfaces whose interface is documented, synthetic test symbols.

`codedoc gaps [scope] [--kind missing|stale|module] [--limit N]` prints rank, score, state, stable key, path:line, signature, and a reason (`public, fan-in 7, changed 3d ago`). Scope is a workspace (default), `--repo`, or a module address.

- [ ] Ranking table tests and a query test; commit `feat(docs): ranked documentation gaps`.

### Task 7: `changes`, `history`, `at`

- `changes --since <ref|duration> [--repo R]`: list files changed between the resolved ref and the working tree (`git diff --name-only <ref>` plus untracked files); for each, extract the ref-side version with `git show` (absent when the file is new) and compare with the current index rows by qualified name; tag symbols `added`, `removed`, `body-changed`, `doc-changed`, `doc-added`, `doc-removed`, and show the current doc state.
- `history <symbol> [--limit N]`: `gitmeta.LogRange` over the symbol's current span.
- `at <symbol> --ref A [--diff B]`: extract the symbol at ref A by running the extractor on `git show A:path` and matching the qualified name (if the file moved, report not found at that ref); print its text with line numbers; with `--diff`, print a unified diff of the symbol text between A and B (B defaults to the working tree). Use a small LCS-based line diff in `internal/query/diff.go` (no external dependency).

- [ ] Tests with a temp repo carrying several commits; commit `feat(query): changes, history, at`.

### Task 8: `overview`

`codedoc overview [--repo R]`: for each repo: branch, commit, dirty count; README and CLAUDE.md top-level headings (first 12); module tree to depth 2 with one-line module docs, file counts and line counts; entry points per spec section 9 (`main` functions and `package main` directories, `cmd/*` and `bin/*` directories, files named `main.*`, `index.*`, `app.*`, `server.*`, symbols calling `ListenAndServe`, `app.listen`, `uvicorn.run`, `SpringApplication.run`); last 10 commits; doc coverage summary (`ok/stale/missing` counts). Budget default applies and the module tree is the first thing truncated.

- [ ] Snapshot test over the fixture workspace; commit `feat(query): overview`.

### Task 9: Outline shows doc state

Replace the Plan 1 `[nodoc]` tag with `[missing]`/`[stale]` tags in `outline`, `find`, and `symbol`; `symbol` prints `doc: ok|stale|missing`. Update snapshots; commit `feat(query): doc state in outputs`.

## Implementation notes

This plan is historical; the spec describes the code as built. Main deviations:

- Body hashes cover the signature; containers hash their member declarations only; blame runs with `-M -C`, and a changed doc is re-seeded from blame (spec 7.5).
- The extractor version is recorded per file (`file.extracted_version`, migration 10), so a version bump re-extracts and re-seeds (spec 7.5).
- `doc set` marks the doc `ok` even with unchanged text; writes are locked, atomic and verified by re-extraction (spec 7.5, 10).
- `gaps` weighs modules `1 + ln(1 + files)` capped at 4, puts every stale gap first, and drops gaps the writer would refuse by planning the head of the list (spec 11.1).
- Doc state shows as `[doc:ok|stale|missing]` tags rather than a `doc:` line (spec 9).
- History reads blobs with `git cat-file` rather than `git show`; `at --diff` takes `--diff=B` (spec 7.6, 9).
- `doc check` runs the freshness check rather than a full reindex (spec 9).
