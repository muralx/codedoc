# CLI reference

Every command, its flags and the shape of its answers. Run
`codedoc <command> --help` for the same, one command at a time.

**Contents:**

- [Output conventions](#output-conventions) ·
  [No dead ends](#no-dead-ends) · [Addresses](#addresses)
- [Commands](#commands): [One call per need](#one-call-per-need) ·
  [Orientation and symbols](#orientation-and-symbols) ·
  [Documents](#documents) · [Files](#files) ·
  [Cross-checking the index](#cross-checking-the-index) ·
  [Graph](#graph) · [Config keys](#config-keys) ·
  [Understanding](#understanding) · [History](#history) ·
  [Documentation](#documentation) ·
  [Index and operations](#index-and-operations)
- [Languages and config formats](#languages-and-config-formats)

## Output conventions

**Shared flags.** Every query takes these:

| Flag | Effect |
|---|---|
| `--workspace W` | workspace name (default: the one containing the current directory) |
| `--repo R` | limit the answer to one repo of the workspace |
| `--format text\|json` | compact text (default) or JSON |
| `--budget N` | output budget in bytes (default 8000, `impact` 3000; `0` is unlimited) |
| `--offset N` | skip the first N ranked entries of each section |
| `--out FILE` | write the full answer to FILE and print a summary |
| `--keys` | print stable keys in place of names |
| `--detail` | print every tag, the signature and the doc sentence where the default leaves them out |
| `--strict` | fail on an address that matches nothing or several symbols, see [No dead ends](#no-dead-ends) |

**Path arguments stay inside the workspace.** Every command that takes a
path (`lines`, `outline`, `ls`, `symbol path:LINE`, `explain`,
`feedback --path`, and the MCP tools of the same names) reads only below
the repos of the workspace and the directories its dependencies are
installed in. A path that climbs out (`../../etc/hostname`), an absolute
path elsewhere, or a symbolic link inside a repo that points elsewhere is
refused: `path is outside the repos of the workspace and the directories
of its dependencies`.

**Header.** The first line holds only what changes a decision:

- A fresh, clean answer of a one-repo workspace has no header line.
- In a workspace of several repos the header names the repos the answer
  draws on, `go-lib@0a93839 go-app@f9911b0`. Locations of an answer
  that draws on several read `repo:path:line`.
- Past three repos it gives their count and names the repo most entries
  come from: `8 repos (go-lib@0a93839 +7)`.
- `dirty` is added when a repo has uncommitted changes, `fresh:no` when
  the index is behind.

**Freshness.** Before answering, a query re-extracts the new, changed and
deleted files of the repos it touches, up to 200 of them. If more files
changed, it answers from the index as it is and says so. A stale answer
still exits 0. Here on a one-repo workspace with
`CODEDOC_FRESH_MAX_FILES=1`:

```
$ codedoc find 'Null*'
dirty fresh:no
== find Null* (1)
NullStore  store/null.go:4  type NullStore struct  NullStore is a Store that keeps nothing.
index is stale (3 files changed); run codedoc index
```

**Entries.** A line reads `name  path:start-end  signature  first doc
sentence`, then tags.

- **Names, not keys.** The name is the short qualified name
  (`MemStore.Get`); a test is `test:<title>` or `it:<title>`, a section
  its heading, a document its file name. `--keys` prints the stable key
  in its place, and JSON always carries both. `gaps` and
  `doc set --verbose` print stable keys: they are what `doc set` takes.
- **One path per file group.** Two or more consecutive entries of one
  file print the path once, on a line of its own, and `:lines` per
  entry below it. An answer about one file (`outline <file>`) prints
  only `:lines`.
- **The doc sentence** is the first sentence, whole. `--doc` (`find`,
  `outline`, `api`) prints the whole doc.
- **Tags** are printed only when they change what the reader does:
  `[test]`, `[stale]`, `[dep name@version]`, `[+N members]`. Kind,
  container, module, fan-in, fan-out, doc state and the confidence tier
  of an edge come with `--detail`. The answers whose tags are their
  point print them all (`gaps`, `changes`, `doc check`, `config`,
  `ls --long`).

```
$ codedoc find '*Store*' --budget 400
go-lib@0a93839 py-lib@ce7cd4d java-lib@b5cd276
== find *Store* (11)
NullStore  go-lib:store/null.go:4  type NullStore struct  NullStore is a Store that keeps nothing.
go-lib:store/store.go
  Store  :10-15  type Store interface  Store persists string values by key.
  MemStore  :18-20  type MemStore struct  MemStore is an in-memory Store.
8 more: codedoc find '*Store*' --budget 400 --offset 3

$ codedoc find 'Mem*' --kind struct --keys
go-lib@0a93839
== find Mem* (1)
go-lib:go:github.com/fixture/lib/store:MemStore  store/store.go:18-20  type MemStore struct  MemStore is an in-memory Store.
```

**Fragments.** Where the question is about lines, the entry carries the
lines themselves, as they are in the file, with their numbers
(`N<TAB>code`, the layout of Claude Code's Read tool):

- `callers`, `callees`, `refs`, `impls`, `supertypes`, `tests`,
  `impact`: the call or reference line, on the entry line.
- `search`, `where`: the lines that match, under the entry, two lines
  of context around them.
- `symbol`: the doc comment and the whole source.

`-C N` (`--context N`), `-A N` and `-B N` set the lines shown around
each fragment, as in grep. `--context symbol` widens every fragment to
its enclosing symbol or section; a fragment never leaves it unless
`--context file` is given.

```
$ codedoc callers Store.Get
go-lib@0a93839
== callers of Store.Get (1)
Service.Lookup  service/service.go:18  v, err := svc.s.Get(key)

$ codedoc callers Store.Get -C 1
go-lib@0a93839
== callers of Store.Get (1)
Service.Lookup  service/service.go:18
17	func (svc *Service) Lookup(key string) string {
18		v, err := svc.s.Get(key)
19		if err != nil {

$ codedoc callers Store.Get --detail
go-lib@0a93839
== callers of Store.Get (1)
Service.Lookup  service/service.go:18  func (svc *Service) Lookup(key string) string  Lookup returns the value for key, or "" when missing.  [import]
18		v, err := svc.s.Get(key)
```

A call that may go to another symbol is never listed as certain: it
follows the certain ones in a section of its own, `possibly callers of X
(N)`, each line starting with `?` and ending with `[1 of K]`, K being
the candidates of the call. Inside `py-lib`, where `self.store` has no
declared type:

```
$ codedoc callers src/pylib/store.py#Store.get
py-lib@ce7cd4d
== callers of Store.get (0)
== possibly callers of Store.get (2)
? Service.lookup  src/pylib/service.py:12  found = self.store.get(key)  [1 of 2]
? test_put_get  tests/test_store.py:7  assert s.get("a") == 1  [test] [1 of 2]
```

**Budgets.** Results are ranked, then printed until the budget runs out.
An entry is printed whole or not at all, and the answer ends with the
exact command for the rest (`8 more: codedoc find '*Store*' --budget 400
--offset 3` above). When not even one entry fits, or whole sections are
left out, the line names the command with the budget that prints the
answer whole (`11 more: codedoc find '*Store*' --budget 1600` under
`--budget 120`), or the command of that one section (`2 more: codedoc
impact Store.Get --only impls`). A source that does
not fit is never dropped, see [`symbol`](#orientation-and-symbols). `--out FILE` writes everything to
the file and prints the head plus `full result: FILE`.

**JSON.** `--format json` prints
`{"header": {...}, "sections": [{"title", "entries": [...], "omitted", "next_offset", "continue"}], "note"}`
(`continue` is the command that prints the omitted entries; an entry
whose source was cut or left out carries its own `continue`).
JSON keeps every field the text leaves out: each entry has `key`,
`name`, `loc`, `text`, `doc`, `tags`, `extra`, `depth`, and `body`,
`body_start` and `body_end` when there is a source. The header carries
`repo`, `branch`, the full `commit`, the `dirty` count and `fresh`; in a
workspace-wide answer `workspace` and a `repos` array. When a command
fails, the last line of stdout is `{"error": "...", "code": N}`.

**Writes are silent.** `index`, `doc set`, `deps index`, `deps prune`,
and `feedback` filing or clearing a report print nothing when they
succeed, on stdout or stderr: the exit status is the result. A failure
is one line on stderr saying what failed and what to do. `--verbose`
prints what was done. `--format json` still prints the result object. A
run that succeeded but left something to act on prints one line on
stderr starting `warning:`.

**Exit status**

| Status | Meaning |
|---|---|
| 0 | answered: also the nearest candidates, the best of several candidates, a stale answer, a `find` or `search` without hits |
| 1 | nothing at all could be offered (not found, a path outside the index or the workspace), a failed write, and any failure that is none of the following |
| 2 | usage: an unknown command or flag, a wrong argument count, a flag value that does not parse or is not accepted (`--depth 0`) |
| 3 | only with `--strict`: an ambiguous address, after its candidates |
| 4 | internal: a database error, a file system error other than a missing file, a panic |

The JSON error object carries the same number in `code`.

## No dead ends

An address that does not name exactly one symbol is still answered.

**Several symbols match.** The best candidate is answered and the note
names the others. The order: a file in the working directory, then one
below it; production code before tests; the symbol whose whole
qualified name is the address; the most referenced; the shortest path.

```
$ codedoc callers Get
go-lib@0a93839
== callers of Store.Get (1)
Service.Lookup  service/service.go:18  v, err := svc.s.Get(key)
also: MemStore.Get (go-lib:store/store.go:26), Cache.Get (go-lib:service/cache.go:9), NullStore.Get (go-lib:store/null.go:6)
```

**One plausible candidate** is answered as if it had been asked for:
the same name in another case, a unique name ending with the address,
the only file a path can mean. The note says `answered for`:

```
$ codedoc symbol stor.go#MemStore.Get
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
answered for MemStore.Get
```

**Nothing matches.** The answer is the nearest candidates, exit status
0. With a file part these are the symbols of that file, the near names
first. Without one, the names of the workspace that equal the address
ignoring case, end with it, contain it or are a typo away, those of the
repo the query runs in first. Full-text hits are offered only when no
name is near: five at most, each holding every word of the address.
Typos and parts of names are listed, never chosen. Candidates are
names, locations and signatures, without docs.

The repo the query runs in also decides between two spellings: inside
`go-lib`, `codedoc symbol lookup` is `Service.Lookup` of go-lib, and
the `Service.lookup` other repos spell exactly so are named in the
`also:` line. The exact case wins only where the working repo has no
such name.

```
$ codedoc symbol store/store.go#get
go-lib@0a93839
== no exact match; nearest: (7)
store/store.go
  Store.Get  :12  Get(key string) (string, error)
  MemStore.Get  :26-32  func (m *MemStore) Get(key string) (string, error)
  ErrMissing  :7  var ErrMissing = errors.New("missing")
  Store  :10-15  type Store interface
  MemStore  :18-20  type MemStore struct
  NewMemStore  :23  func NewMemStore() *MemStore
  MemStore.Put  :35-38  func (m *MemStore) Put(key, value string) error

$ codedoc symbol Lokup
4 repos (go-lib@0a93839 +3)
== no exact match; nearest: (4)
Service.Lookup  go-lib:service/service.go:17-23  func (svc *Service) Lookup(key string) string
Service notes > Lookup  doc-repo:docs/notes.rst:7-20  Lookup
Service.lookup  py-lib:src/pylib/service.py:11-15  def lookup(self, key: str) -> str
Service.lookup  java-lib:src/main/java/com/fixture/service/Service.java:23-29  public String lookup(String key)

$ codedoc outline store/stroe.go
go-lib@0a93839
== no exact match; nearest: (1)
store/store.go  38 lines
```

`path:LINE` on a line no symbol encloses lists the symbols of the file
and says `no symbol encloses line 3 of store/store.go (38 lines)`.

**`find`** with a name no symbol has answers with what `search` finds
for it, titled `no symbol named so; search results:`. A pattern of
several words that names no symbol is answered as `where` answers it. A
glob that matches nothing stays an empty answer.

**Nothing to offer** is one line on stderr and exit status 1. For a
name, the line names the dependencies that were not searched (here
inside `deps-app` of the workspace `fixture-deps`):

```
$ codedoc symbol Zzyzxq
codedoc: symbol Zzyzxq: not found; not searched: lint-tool, plain-js, github.com/BurntSushi/toml, github.com/fixture/lib (dependencies no source file imports; deps.include in the config indexes them)
```

**A path that is not indexed** says why, in one line and with exit
status 1. `lines` prints the file anyway, since it reads the disk, and
gives the reason as its note. Here `dist/x.go` and a git-ignored
`out/o.go` were added to `go-lib`, and the third path is in `deps-app`:

```
$ codedoc outline dist/x.go
codedoc: dist/x.go is not indexed: the directory dist/ is excluded (dependencies, fixtures and build output are)

$ codedoc outline out/o.go
codedoc: out/o.go is not indexed: git ignores it (.gitignore)

$ codedoc outline node_modules/lint-tool/index.d.ts
codedoc: node_modules/lint-tool/index.d.ts is not indexed: no source file imports the dependency lint-tool; name it under deps.include in the config to index it

$ codedoc outline store/nope.go
codedoc: outline store/nope.go: not found: no such file or directory in go-lib
```

The other reasons: a dependency that waits to be indexed, failed or is
not installed; a file of a dependency outside its entry points; a
package no manifest declares; a lock file; an exclude glob of the
workspace; not a regular file; over the size limit; a binary file; a
language the repo's `languages:` leaves out; a directory without indexed
files; a file the index has not read yet.

**`--strict`** restores the errors, for scripts that must not get a
neighbour: an address that matches nothing is `not found` with exit
status 1, and one that matches several prints its candidates and exits
3. `find` does not search.

```
$ codedoc callers Get --strict
go-lib@0a93839
== candidates (4)
Cache.Get  service/cache.go:9-12  func (c *Cache) Get(key string) (string, bool)  Get returns the cached value for key.
NullStore.Get  store/null.go:6  func (NullStore) Get(key string) (string, error)
store/store.go
  Store.Get  :12  Get(key string) (string, error)  Get returns the value for key.
  MemStore.Get  :26-32  func (m *MemStore) Get(key string) (string, error)  Get implements Store.
codedoc: symbol Get: ambiguous (4 candidates)
```

**Writes are strict without the flag.** `doc set` never writes to a
nearest or a best candidate. Its failure is one line and exit status 1:

```
$ codedoc doc set Get --text "x"
codedoc: doc set Get: ambiguous (4 candidates); address one by its key: go-lib:go:github.com/fixture/lib/service:Cache.Get, go-lib:go:github.com/fixture/lib/store:NullStore.Get, go-lib:go:github.com/fixture/lib/store:Store.Get (and 1 more: codedoc find lists them)
```

## Addresses

You can address a symbol in six ways:

| Form | Example |
|---|---|
| bare name | `MemStore.Get`, `Get` (several matches: the best one is answered, the others are named) |
| path form | `go-lib:store/store.go#MemStore.Get`, or `store/store.go#MemStore.Get` in the current repo |
| line | `store/store.go:28`: the innermost symbol or section enclosing that line |
| document section | `docs/design.md#6. Delivery`, `docs/design.md#6.2` (see [Documents](#documents)); `docs/design.md` alone is the document |
| Go-style package name | `store.NewMemStore` (the last segment of the module identity) |
| stable key `repo:lang:module:Qualified[#hash]` | `go-lib:go:github.com/fixture/lib/store:MemStore.Get`, `ts-domain:ts:@fixture/domain:src/records.ts/EnvironmentRepo.load`, `py-lib:py:pylib.store:MemStore.get`, `java-lib:java:com.fixture.store:MemStore.put#1d09` |

Answers print names and locations; both go back in as addresses. Stable
keys are for scripts and for `doc set`: `--keys` and JSON print them.
Some symbols have special key forms:

- **Java methods and constructors** always carry a hash of their parameter
  types (`#1d09`), so overloads get distinct keys. Constructors are named
  `<init>`.
- **Duplicates** of a qualified name in one file get `~2`, `~3`.
- **TypeScript tests** are symbols named `it:<title>` or `test:<title>`.
- **Documents and sections** use the language `doc` and the file path as
  the module: `doc-repo:doc:docs/design.md:6. Delivery > 6.2 Retries`
  (the heading path joined with ` > `; empty for the document itself).

Modules are addressed by identity (`github.com/fixture/lib/store`,
`@fixture/domain`, `pylib.store`, `com.fixture.store`) or by `repo:dir`
(`go-lib:store`). `doc set` takes `repo:dir/` with a trailing slash.
`siblings` takes a config key as `config:KEY` (`config:db.host`).

## Commands

Examples run on the fixture workspace from the [Quickstart](../README.md#quickstart),
inside `go-lib`.

### One call per need

| Need | Command |
|---|---|
| Read a function, a type or a section | `symbol <addr>...` |
| What is in a file or a directory | `outline <path>` |
| Where is this done | `where "<words>"` |
| What does a change to this touch | `impact <symbol>` |
| How does this work | `how <symbol\|path>` |
| What does this dependency offer | `api <package> [name]` |
| Which files exist, what changed lately | `ls` |
| A line range I already know | `lines <path> <range>...` |

The other commands answer one relation each (`callers`, `refs`,
`tests`, ...) and are what the composite ones are built from.

### Orientation and symbols

`--deps` adds the third-party packages to an answer, see
[Dependencies](dependencies.md).

| Command | Flags | Answers |
|---|---|---|
| `overview` | — | repos (branch, commit, dirty, size), README.md and CLAUDE.md headings, entry points, doc coverage, the last 10 commits, the dependencies, the module tree to depth 2 (cut first by the budget). A module's file and line counts include the modules nested under it (an npm package counts its `src/` directories, a Python package its modules and subpackages), not another package below its directory |
| `outline <path\|module>` | `--doc`, `--detail`, `--sort`, `--reverse` (directories), `--deps` | the symbols of a file (members collapsed; `--detail` tags each with kind, fan-in and doc state); the heading tree of a document; the files of a directory or module with symbol and line counts and modification times, ordered by `--sort name\|mtime\|committed\|size\|lines` |
| `ls [path\|glob ...]` | `--long`, `--sort`, `--reverse`, `--since`, `--until`, `--kind`, `--lang`, `--depth N`, `--dirs` | the indexed files, newest first, see [Files](#files) |
| `find <name\|glob>` | `--kind`, `--lang`, `--doc`, `--detail`, `--deps` | symbols by name, qualified name or glob (`--detail` tags each with kind, fan-in and doc state); with `--kind section` or `--kind document`, headings containing the text |
| `symbol <addr>...` | `--no-body`, `--doc-only`, `--context N`, `--deps` | the location and the source of a symbol from its doc comment on, with line numbers. For a section of a document: its text. Several addresses are answered together |
| `lines <path> <A-B\|A+N\|A>...` | — | those line ranges of the file on disk, each under the symbol that encloses it, see [Cross-checking the index](#cross-checking-the-index) |

`--lang` takes a family (`go`, `ts`, `py`, `java`, `doc`) or a precise
language (`typescript`, `tsx`, `javascript`, `python`, `markdown`, `rst`,
`asciidoc`, `text`). `--kind` takes `function`, `method`, `struct`,
`interface`, `class`, `type`, `const`, `var`, `field`, `enum` and so on,
and `section` and `document`.

**`symbol`** prints one line, `name  path:start-end`, then the lines of
the file from the first line of the doc comment to the end of the
symbol. The signature and the doc are those lines, so the header line
does not repeat them.

- `--no-body` prints location, signature and doc on one line;
  `--detail` adds kind, container, module, fan-in, fan-out and doc
  state. `--body` is accepted and does nothing: it is the default.
- `--context N` adds N lines before and after the source.
- A line number from a stack trace, a test failure or a grep hit is an
  address: `symbol path:LINE` prints the innermost symbol or section
  enclosing it.
- Several addresses are answered in one call under one budget. One that
  names nothing is one line, and the call fails only when none
  resolves.

```
$ codedoc symbol store/store.go:28
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

$ codedoc symbol MemStore.Get --detail --no-body
go-lib@0a93839
MemStore.Get  store/store.go:26-32  func (m *MemStore) Get(key string) (string, error)  Get implements Store.  [method] [container:MemStore] [module:github.com/fixture/lib/store] [fan-in:1] [fan-out:2] [doc:ok]

$ codedoc symbol MemStore.Put NewMemStore Nope
go-lib@0a93839
MemStore.Put  store/store.go:35-38
34	// Put implements Store.
35	func (m *MemStore) Put(key, value string) error {
36		m.data[key] = value
37		return nil
38	}
NewMemStore  store/store.go:23
22	// NewMemStore creates an empty MemStore.
23	func NewMemStore() *MemStore { return &MemStore{data: map[string]string{}} }
Nope  not found
```

A source larger than the budget is never dropped. A type with members
answers with the outline of its members, a document with its heading
tree; any other source is cut to the lines that fit and ends with the
command that continues it. Inside `java-lib`:

```
$ codedoc symbol src/main/java/com/fixture/store/MemStore.java#MemStore --budget 600
java-lib@b5cd276
src/main/java/com/fixture/store/MemStore.java
  MemStore  :8-39  public class MemStore implements Store  MemStore keeps values in memory.
  MemStore.data  :9  private final Map<String, String> data
  MemStore.<init>  :12-14  public MemStore()  Creates an empty store.
6 more: codedoc symbol src/main/java/com/fixture/store/MemStore.java#MemStore --budget 600 --offset 3
MemStore is 33 lines, over the budget: its members are listed; codedoc symbol MemStore.<member> prints one, codedoc lines java-lib:src/main/java/com/fixture/store/MemStore.java A-B a range

$ codedoc symbol src/main/java/com/fixture/service/Service.java#Service.lookup --budget 260
java-lib@b5cd276
Service.lookup  src/main/java/com/fixture/service/Service.java:23-29
23	    public String lookup(String key) {
24	        String found = store.get(key);
5 more lines: codedoc lines java-lib:src/main/java/com/fixture/service/Service.java 25-29
```

### Documents

Every `.md`, `.mdx`, `.markdown`, `.rst`, `.adoc` and `.txt` file is a
document: one symbol of kind `document`, and one of kind `section` per
heading, which runs to the next heading of the same or a higher level.
The examples run inside `doc-repo`.

```
$ codedoc outline docs/design.md
doc-repo@c713ac5
== docs/design.md, 63 lines (11)
10-13  ## 1. Goals  4 lines
15-35  ## 2. Model  21 lines
  19-27  ### 2.1 Keys  9 lines
  29-35  ### 2.2 Values  7 lines
    33-35  #### Notes  3 lines
37-53  ## 6. Delivery  17 lines
  42-44  ### 6.1 Ordering  3 lines
  46-53  ### 6.2 Retries  8 lines
    51-53  #### Notes  3 lines
55-58  Open questions  4 lines
60-63  Appendix  4 lines

$ codedoc symbol "docs/design.md#6.2"
doc-repo@c713ac5
6.2 Retries  docs/design.md:46-53
46	### 6.2 Retries
47	
48	A failed delivery is retried three times. Details are in the
49	[retry runbook](runbooks/retries.md#backoff).
50	
51	#### Notes
52	
53	Retries back off exponentially.
```

- **Headings.** Markdown and MDX: ATX (`#`) and setext headings, outside
  fenced code and HTML comments. reStructuredText: underlined (and overlined) titles, levels
  in order of first appearance. AsciiDoc: `=` titles outside delimited
  blocks. Plain text has no sections. YAML front matter is skipped; its
  `title` and `description` become the document's title and summary.
- **Addressing a section.** `path#Heading` matches, in this order: the
  heading or the heading path as written; the same ignoring case, or the
  heading's anchor (`6-delivery`); its numbering alone (`#6`, `#6.2`); a
  prefix of the heading; a substring of the heading, then of the heading
  path (`#values > notes`).
- **Sources.** `symbol` prints the section with its sub-sections. A
  document or section larger than the budget answers with its heading
  tree and the commands that print one heading or a line range.
- **A document outline** prints line ranges only; its title names the
  file. Large files (over 2 MB) are listed by `ls` but not sectioned; `outline`
  of one says so and points at `codedoc lines`.

`find` matches headings with `--kind section` (any heading containing the
text, ignoring case), `search` and `where` cover the text of every
section, and `explain doc.md` prints the document's title, first
paragraph and heading tree:

```
$ codedoc find retries --kind section
doc-repo@c713ac5
== find retries (1)
6.2 Retries  docs/design.md:46-53  ### 6.2 Retries  A failed delivery is retried three times.

$ codedoc explain docs/runbooks/retries.md
doc-repo@c713ac5
== explain doc-repo:docs/runbooks/retries.md (1)
retries.md  docs/runbooks/retries.md:1-12  Retry runbook  What to do when deliveries keep failing. Background is in the [design](../design.md#6-delivery).
== headings (3)
1-12  # Retry runbook  12 lines
  6-8  ## Backoff  3 lines
  10-12  ## Escalation  3 lines
```

A relative Markdown link to another document of the repo is a
`references` edge from the section it is written in to that document, or
to the section its anchor names. `refs` answers "which docs point here"
with the line of the link:

```
$ codedoc refs "docs/design.md#6"
doc-repo@c713ac5
== references to 6. Delivery (1)
Retry runbook  docs/runbooks/retries.md:4  [design](../design.md#6-delivery).
```

Documents and sections are not documentation debt: they never count in
doc coverage, `gaps`, `doc check`, upkeep or autodoc, `doc set` refuses
them, and `overview` and `status` count code symbols only.
`changes --since` lists the sections of changed documents as `added`,
`removed`, `moved` or `text-changed`, without a doc state. A repo drops
documents with `languages:` like any other language (`doc`, or
`markdown`, `rst`, `asciidoc`, `text`); once `languages:` is set, list
`doc` to keep them.

### Files

`ls` replaces `ls -lt`, `find -newer` and `tree`. It lists the indexed
files newest first, the files of a directory together under its name.
Without an argument it lists the working directory recursively, which is
"what changed most recently here". Each line is the path, the
modification time in the local zone and the line count:

```
$ codedoc ls
go-lib@0a93839
== ls (8)
go.mod  2026-01-01 00:00  3 lines
service/
  cache.go  2026-01-01 00:00  18 lines
  gen.go  2026-01-01 00:00  5 lines
  service.go  2026-01-01 00:00  28 lines
store/
  doc.go  2026-01-01 00:00  2 lines
  null.go  2026-01-01 00:00  8 lines
  store.go  2026-01-01 00:00  38 lines
  store_test.go  2026-01-01 00:00  13 lines
```

`--long` (`-l`) adds the size, kind/language, the number of symbols (of
a document: sections) and a title: a document's front-matter title or
first heading, a code file's module doc sentence.

```
$ codedoc ls --long
go-lib@0a93839
== ls (8)
go.mod  2026-01-01 00:00  39B  3 lines  other
service/
  cache.go  2026-01-01 00:00  349B  18 lines  code/go  4 symbols
  gen.go  2026-01-01 00:00  95B  5 lines  code/go  1 symbol
  service.go  2026-01-01 00:00  653B  28 lines  code/go  6 symbols  Package service exposes the fixture's use cases.
store/
  doc.go  2026-01-01 00:00  70B  2 lines  code/go  Package store persists the fixture's key-value data.
  null.go  2026-01-01 00:00  221B  8 lines  code/go  3 symbols
  store.go  2026-01-01 00:00  861B  38 lines  code/go  9 symbols  Package store holds the storage abstractions of the fixture library.
  store_test.go  2026-01-01 00:00  240B  13 lines  test/go  1 symbol

$ codedoc ls --dirs
go-lib@0a93839
== ls (3)
./  8 files  2026-01-01 00:00  115 lines  2.5K
service/  3 files  2026-01-01 00:00  51 lines  1.1K
store/  4 files  2026-01-01 00:00  61 lines  1.4K
```

(The fixture's files all carry one date here; yours carry the time of
your copy.)

- **Commit date.** With `--long`, when the last commit touching a file
  is more than a minute away from its modification time, the line adds
  `committed 2026-10-09 11:20`. A file with uncommitted changes is tagged
  `[modified]`, one no commit holds `[untracked]`; neither shows a commit
  date. Commit dates are read in one `git log` pass per index run, from
  the last indexed head on.

| Flag | Meaning |
|---|---|
| `--long`, `-l` | add size, kind, symbol count, title and commit date to each file |
| `--sort mtime\|committed\|name\|size\|lines` | default `mtime`; times, sizes and line counts descending, names ascending |
| `--reverse` | invert the order |
| `--since D`, `--until D` | by modification time: a duration back from now (`30m`, `3h`, `2d`, `1w`), a date (`2026-10-09`, `2026-10-09T14:30`) or a git ref (the date of its commit) |
| `--kind code\|doc\|config\|test\|other` | repeatable |
| `--lang L` | a language, a family (`go`, `ts`, `doc`) or a config format |
| `--depth N` | how deep below the path argument files are listed (`1`: the directory's own files); with `--dirs`, how many levels of directories |
| `--dirs` | one line per directory: files below it, newest modification time, total lines and size |

Arguments are paths (`repo:path`, or relative to the working directory or
the current repo) or globs (`'src/**/*.ts'`, `repo:**/*.md`).

Files that discovery leaves out are never listed. The note of the answer
counts those under each directory argument by reason, for example:

```
hidden under go-lib:.: 1 ignored by git (1 whole directory), 2 built-in exclusion
```

The reasons are `ignored by git` (a directory ignored as a whole counts
once), `built-in exclusion`, `workspace exclude` and `not indexable
(binary, over 2 MB or language not selected)`. `outline <path>` of such
a file says which reason applies to it, see
[No dead ends](#no-dead-ends).

### Cross-checking the index

Reading a known line range with `sed -n A,Bp` is fine and stays outside
codedoc. `lines` does the same and names what the index places there,
which is what you want the moment an answer looks wrong:

```
$ codedoc lines store/store.go 24-28
go-lib@0a93839
store/store.go:24-28  in MemStore.Get (26-32)
24	
25	// Get implements Store.
26	func (m *MemStore) Get(key string) (string, error) {
27		v, ok := m.data[key]
28		if !ok {

$ codedoc lines store/store.go 7 10-12 26+2
go-lib@0a93839
store/store.go
  :7  in ErrMissing (7)
7	var ErrMissing = errors.New("missing")
  :10-12  in Store (10-15)
10	type Store interface {
11		// Get returns the value for key.
12		Get(key string) (string, error)
  :26-28  in MemStore.Get (26-32)
26	func (m *MemStore) Get(key string) (string, error) {
27		v, ok := m.data[key]
28		if !ok {
```

A range is `A-B`, `A+N` (A and the N lines after it) or `A`. Several
ranges are printed in line order, joined where they overlap or touch,
each under a header naming the innermost symbol or section that
encloses the whole range (`in Store (10-15)`), or, when no single one
does, the ones it starts and ends in (`in NullStore.Get (7) ..
NullStore.Put (10)`). A range larger than the budget is printed up to the
lines that fit and ends with the `codedoc lines` call for the rest. A
file that changed since it was indexed is re-indexed first. If the index
still disagrees with the file (the lines a symbol is recorded on do not
hold its declaration), the note carries `index mismatch: <key> recorded
120-168`. A file of a dependency
(`node_modules/@acme/sdk/dist/server/index.d.ts`) is printed under the
dependency symbols on the range.

`feedback` records such a disagreement instead of letting it pass.
Filing a report prints nothing:

```
$ codedoc feedback "symbol of MemStore.Get ends one line early" \
    --path store/store.go --cmd "codedoc symbol MemStore.Get"

$ codedoc feedback --list
== feedback (1)
2026-10-11T00:46:21Z  fixture/go-lib main@0a93839  symbol of MemStore.Get ends one line early  [path:store/store.go] [cmd:codedoc symbol MemStore.Get] [codedoc e0c6377 extractor 10]
```

| Command | Flags | Does |
|---|---|---|
| `feedback <text>` | `--key K`, `--path P`, `--cmd C`, `--verbose`, `--workspace`, `--repo` | appends one JSON line: time, workspace, repo, head, dirty flag, codedoc build and extractor version, the text and the flags, and for `--key` or `--path` the content hash the index holds for the file and the hash of the file on disk. The index is not touched. `--verbose` prints `feedback recorded in <file>` |
| `feedback --list` | `--since D` (duration or date), `--budget`, `--offset`, `--out`, `--format json` | the reports, oldest first, cut and continued like a query answer but without a header line; a text longer than 400 bytes is cut in its line (`--format json` carries each stored report whole under `extra`); a report whose two hashes differ is tagged `[index behind the file]` |
| `feedback --clear` | `--verbose` | empties the file, whatever it holds; `--verbose` prints `feedback cleared (N reports)` |

The file stays small: a text is cut to 8 KB and every other field to
1 KB (ending ` [truncated]`), and at 4 MB the oldest reports are dropped,
keeping the newest 2 MB. Lines that are no report (garbage, a line cut
short) are skipped by `--list`.

Nothing leaves the machine.

### Graph

| Command | Flags | Answers |
|---|---|---|
| `callers <addr>` | `--depth N`, `--no-tests`, `--group-by file`, `-C/-A/-B`, `--deps` | callers, each once with the line of every call; a tree above depth 1; calls that may go elsewhere in a section of their own |
| `callees <addr>` | `--depth N`, `--no-tests`, `--group-by file`, `-C/-A/-B` | what a symbol calls, with the call lines |
| `refs <addr>` | `--kind type\|instantiate\|field\|link`, `--no-tests`, `--group-by file`, `-C/-A/-B`, `--deps` | non-call references with their lines, one entry per referrer and line; for a document or section, the sections linking to it (`link`) |
| `impls <addr>` | `--no-tests`, `--group-by file`, `-C/-A/-B` | types implementing or extending a type |
| `supertypes <addr>` | `--group-by file`, `-C/-A/-B` | types a type implements or extends |
| `tests <addr\|module>` | `--transitive`, `-C/-A/-B` | tests that reference the symbol, with the line; with `--transitive` also those reaching it through callers up to depth 3 |
| `siblings <addr\|file\|dir\|config:KEY>` | — | other implementations of the same interface or members of the same container, same-named files in sibling directories, files sharing a suffix, sibling directories. A config key, addressed `config:KEY` (or by a bare name no symbol has), gets the other keys under the same parent |
| `deps <module>` | `--depth N`, `--cross-repo` | modules a module imports |
| `dependents <module>` | `--depth N`, `--cross-repo` | modules importing a module |

```
$ codedoc callers Service.Lookup --depth 2
go-lib@0a93839 go-app@f9911b0
== callers of Service.Lookup (2)
run  go-app:main.go:16  func run(svc *service.Service) string { return svc.Lookup("k") }
  main  go-app:main.go:13  fmt.Println(run(svc))

$ codedoc impls store/store.go#Store
go-lib@0a93839
== implementations of Store (2)
NullStore  store/null.go:4  type NullStore struct{}
MemStore  store/store.go:18  type MemStore struct {

$ codedoc tests MemStore.Put
go-lib@0a93839
== tests of MemStore.Put (1)
TestMemStorePutGet  store/store_test.go:7  if err := m.Put("k", "v"); err != nil {  [test]

$ codedoc deps github.com/fixture/app --cross-repo
go-lib@0a93839 go-app@f9911b0
== dependencies of github.com/fixture/app (2)
github.com/fixture/lib/service  go-lib:service  go go-package  [cross-repo]
github.com/fixture/lib/store  go-lib:store  go go-package  [cross-repo]
```

Every edge has one of these confidence tiers. Text output shows the
tier with `--detail`; an `ambiguous` edge is always set apart.

| Tier | Meaning |
|---|---|
| `precise` | from a SCIP index |
| `local` | resolved by language scoping without an import: same file, same package, `this`/`self`/`cls`, the Go method receiver, a typed field (`svc.s` declared `s store.Store`), a local variable or parameter whose declaration names a type of the file or package (`m := NewMemStore()`, `const h = new Here()`), a member inherited from a supertype in the package |
| `import` | through an import, dot import or star import to a workspace module or its descendants, also when the import names the type of a local variable or parameter (`svc *service.Service`, `const repo = new MemoryRepo()`) |
| `unique` | a qualified reference whose receiver can't be typed and is not a variable of unknown type, matched to the only definition with that name and arity. Bare names never get this tier |
| `ambiguous` | several candidates, or the one candidate of a call through a variable of unknown type. The edge is listed under each one |

**Local variables.** Go and TypeScript/JavaScript calls through a local
variable, a parameter or a named result resolve through its type where
the declaration makes it evident:

- Go: `var x T`, `var x *T`, `x := T{...}`, `x := &T{...}`, `x := new(T)`,
  typed parameters and named results, and `x := pkg.NewT(...)` when the
  callee is a function whose signature names its first result.
- TS/JS: `const x = new T(...)`, `const x: T = ...`, typed parameters, a
  class property initialised with `new T(...)` and `this.x = new T(...)`.
  A type alias of a named type stands for that type.

Variables are scoped lexically: a variable of an inner block, a closure
parameter, an `if x, err := f(); ...` variable, a loop or `catch`
variable or a destructured name (`const { x } = h`, `({ x }) => ...`)
hides an outer variable of the same name only inside its scope, and a
later declaration hides an earlier one from that line on. Module-level
variables count too (`const store = new Store()` at the top of a file,
a Go package `var x = T{}` or `var x T` in any file of the package), and
so does a named import under another name (`import { A as B }`). A
variable whose type codedoc cannot tell (an untyped parameter, the
result of a method that declares none) is never guessed at: where the
`unique` tier would have picked the only method with that name, the edge
is kept as `ambiguous` with that one candidate, so `callers` still lists
it but does not present it as certain. A variable of
a builtin type (`error`, `string`, `Map`, an array), or of a type from a
package that is not indexed (`strings.Builder`), calls nothing in the
index: the call stays unresolved instead of landing on a method of the
workspace that happens to share its name. When the type is known but
does not declare the member (an embedded or inherited one), the `unique`
tier decides as before.

**Typed receiver chains.** A receiver is followed through declared
types, hop by hop:

- a field hop is the declared type of a Go struct field, of a TS class
  field, interface property or property of an object type alias, or of a
  Java field; a TS constructor parameter property
  (`constructor(private store: Store)`) types `this.store`;
- a call hop is the declared result of the method
  (`s.pick().Touch()`); `await` unwraps `Promise<T>`;
- hops chain: `options.client.register()`,
  `this.deps.stores.agents.get()`, `s.opts.Store.First.Touch()`;
- a variable assigned from such a chain carries its type: a
  destructured parameter (`({ client }: Options)`), `const x = a.b`,
  `const r = await store.get(id)`, a loop variable over a typed
  collection, the Go forms `x := s.field` and `r, err := s.store.Get(id)`;
- in Go a member a struct does not declare is looked for in the structs
  it embeds, three levels at most;
- a hop into a type of a dependency reads the member from the
  dependency index.

The chain keeps the tier a typed local has: `local` while every type
lies in the file (TS) or package (Go) of the reference, `import`
otherwise, never `precise`. A hop that cannot be read from a
declaration (an unannotated field, `any`, a union, a type parameter)
leaves the reference where it was before the chain was tried. Python
attributes and Java method results are not followed.

### Config keys

| Command | Flags | Answers |
|---|---|---|
| `config <key\|glob>` | `--defines`, `--reads` | definitions per file with format and value preview, config files and templates referring to the key, the code that reads it. A glob first lists the matching keys with counts |

```
$ codedoc config 'lib.*' --defines
java-lib@b5cd276 cfg-repo@c93f4d0
== config keys matching lib.* (2)
lib.mode  defines:4 references:0 reads:1 in java-lib,cfg-repo
lib.timeout  defines:0 references:0 reads:1  [undefined]
== defines lib.mode (4)
java-lib:pom.xml:8  xml  dev
cfg-repo:config/app.properties:2  properties  dev
cfg-repo:config/application.yaml:2  yaml  dev
cfg-repo:pom.xml:6  xml  dev
== defines lib.timeout (0)
```

A key that code reads but nothing in the index defines (typically an
environment variable set outside the repos) still answers: its reads are
tagged `[undefined]`. A glob lists undefined keys when an accessor reads
them; a key-like string literal alone counts only for its exact name.

An accessor can take its key through a constant, as in
`os.Getenv(KeyConst)`. codedoc then reads the key from the constant's
value.

`status` counts accessor reads of undefined keys as `unresolved-config`.

### Understanding

| Command | Flags | Answers |
|---|---|---|
| `where <words>` | `--limit N` (default 5), `-C/-A/-B`, `--deps` | "where is this done": the top locations in code and documents, each with the lines that match. A single strong hit is shown whole, as `symbol` shows it |
| `impact <symbol>` | `--only callers\|refs\|impls\|tests\|mentions\|config`, `-C/-A/-B` | "I am about to change this": callers, references, implementations, tests, mentions, config keys read, exports, in one answer |
| `how <symbol\|path>` | `--deps` | "how does this work": what `explain` says, then the first two levels of the call tree |
| `search <text>` | `--kind` (`module` for modules only), `--lang`, `--limit N` (results per page, default 20; the title counts every hit and `--offset` continues), `-C/-A/-B`, `--deps` | symbols, document sections and modules by keywords and, with a model, meaning, each with the lines that match, see [Search and embeddings](search.md) |
| `explain <addr\|path>` | `--deps` | the doc stack (workspace, repo, module chain, container, symbol), then the top 5 callers and callees. For a module or file: the main symbols. For a document: its title, first paragraph and heading tree |
| `flow <addr>` | `--depth N` (default 3), `--documented-only` | call tree below a symbol, printed in call order with each node as `signature — one-line doc`. The tree is built breadth first: each symbol is expanded once, at its shallowest node, and its other nodes say `[see above]` or `[see below]`. Ambiguous calls list their candidates unexpanded; calls back up the path are marked `cycle`; at most 200 nodes |

**`where`** is the search ranking without modules, answered as
locations. Each hit is the symbol or section, then the lines of its
file that match, with two lines around them.

```
$ codedoc where "persist values by key" --limit 2
go-lib@0a93839 doc-repo@c713ac5
== where "persist values by key" (21)
Store  go-lib:store/store.go:10-15  type Store interface  Store persists string values by key.
9	// Store persists string values by key.
10	type Store interface {
11		// Get returns the value for key.
12		Get(key string) (string, error)
13		// Put stores value under key.
14		Put(key, value string) error
15	}
2. Model  doc-repo:docs/design.md:15-35  ## 2. Model  A record has a key and a value.
15	## 2. Model
16	
17	A record has a key and a value.
18	
19	### 2.1 Keys
20	
21	Keys are strings. The empty key is rejected.
22	
23	```sh
19 more: codedoc where 'persist values by key' --limit 2 --offset 2
text search only: embeddings disabled (CODEDOC_EMBEDDER=off)
```

**`impact`** prints the symbol, then only the sections that have
entries: `callers`, `possibly callers`, `references`, `implementations`
(of a member: `implements or overrides` and `implemented or overridden
by`), `tests` (direct, then via callers), `mentions in documents and doc
comments`, `config keys read`, and the line `exported`. Its default
budget is 3,000 bytes: it is a summary. The budget is shared, so no
section costs another its place, and a section that is cut ends with the
command for the rest of it. `--only` prints one section, which
`--offset` then pages.

```
$ codedoc impact Store.Get
go-lib@0a93839
Store.Get  store/store.go:12  Get(key string) (string, error)  Get returns the value for key.
== callers (1)
Service.Lookup  service/service.go:18  v, err := svc.s.Get(key)
== implemented or overridden by (2)
NullStore.Get  store/null.go:6  func (NullStore) Get(key string) (string, error)
MemStore.Get  store/store.go:26  func (m *MemStore) Get(key string) (string, error)
== mentions in documents and doc comments (1)
MemStore.Get  store/store.go:25  // Get implements Store.
exported  yes (public member of Store)  no other repo of the workspace uses it

$ codedoc impact Mode
go-lib@0a93839
Mode  service/service.go:28  func Mode() string  Mode returns the library mode from the environment.
== config keys read (1)
LIB_MODE  service/service.go:28  func Mode() string { return os.Getenv("LIB_MODE") }
exported  yes  no other repo of the workspace uses it
```

A file with more than ten call sites prints its first three and the
command that lists the others.

**`how`** on a symbol is its `explain` stack and top callers, then
`flow --depth 2`. On a code file it is the `explain` of the file and
the tree of each of its entry functions (the functions nothing calls),
three at most. A directory, a module, a document or a dependency is
answered as `explain` answers it.

```
$ codedoc how go-app:main.go
go-lib@0a93839 go-app@f9911b0
== explain go-app:main.go (3)
fixture  workspace  repos: go-lib; go-app; ts-domain; ts-app; py-lib; java-lib; cfg-repo (Config fixture: one small file per config format.); doc-repo
  go-app  repo
    github.com/fixture/app  go-app:.  module go go-package  Command app is the fixture entry point.
== symbols (2)
go-app:main.go
  main  :11-14  func main()
  run  :16  func run(svc *service.Service) string
== flow from main (5)
main  go-app:main.go:11-14
  New  go-lib:service/service.go:14  func New(s store.Store) *Service — New wires a Service to a Store.
  NewMemStore  go-lib:store/store.go:23  func NewMemStore() *MemStore — NewMemStore creates an empty MemStore.
  run  go-app:main.go:16  func run(svc *service.Service) string
    Service.Lookup  go-lib:service/service.go:17-23  func (svc *Service) Lookup(key string) string — Lookup returns the value for key, or "" when missing.
```

`flow` alone follows the tree deeper:

```
$ codedoc flow main
go-lib@0a93839 go-app@f9911b0
== flow from main (7)
main  go-app:main.go:11-14  func main()
  New  go-lib:service/service.go:14  func New(s store.Store) *Service — New wires a Service to a Store.
  NewMemStore  go-lib:store/store.go:23  func NewMemStore() *MemStore — NewMemStore creates an empty MemStore.
  run  go-app:main.go:16  func run(svc *service.Service) string
    Service.Lookup  go-lib:service/service.go:17-23  func (svc *Service) Lookup(key string) string — Lookup returns the value for key, or "" when missing.
      Store.Get  go-lib:store/store.go:12  Get(key string) (string, error) — Get returns the value for key.
      fallback  go-lib:service/service.go:25  func fallback(key string) string
```

### History

| Command | Flags | Answers |
|---|---|---|
| `changes --since <ref\|duration>` | — | files changed since a ref, or since the newest commit older than a duration (`12h`, `3d`, `2w`), against the working tree (uncommitted and untracked included, renames detected). The symbols in them are tagged `added`, `removed`, `moved`, `body-changed`, `doc-changed`, `doc-added` or `doc-removed`, with their current doc state |
| `history <addr>` | `--limit N` | commits that touched the symbol's lines (`git log -L`) |
| `at <addr> --ref A` | `--diff[=B]` | the symbol as it was at A; with `--diff`, a unified diff of just that symbol from A to B. B defaults to the working tree; give it with an equals sign: `--diff=HEAD` |

```
$ codedoc doc set service/service.go#fallback --text "fallback returns the value used when the store has no entry for key."

$ codedoc changes --since HEAD
go-lib@0a93839 dirty
== changed files since HEAD (1)
go-lib:service/service.go  modified, 1 symbol change
== changed symbols (1)
fallback  service/service.go:26  func fallback(key string) string  fallback returns the value used when the store has no entry for key.  [doc-added] [doc:ok]

$ codedoc at service/service.go#fallback --ref HEAD --diff
go-lib@0a93839 dirty
== diff HEAD..worktree (1)
fallback  service/service.go:26  func fallback(key string) string  fallback returns the value used when the store has no entry for key.
--- HEAD:service/service.go
+++ worktree:service/service.go
@@ -25,1 +25,2 @@
+// fallback returns the value used when the store has no entry for key.
 func fallback(key string) string { return "default:" + key }

$ codedoc history MemStore.Get
go-lib@0a93839
== history of MemStore.Get (1)
0a93839  2026-01-01 fixture  fixture: initial
```

### Documentation

Documentation is written where the knowledge is: in the session that
writes the code, as an ordinary doc comment in the edit. The commands
here are for code that is not being edited, and for looking at the debt.
What is left stale or missing after a commit is filled outside the
session, see [Documentation upkeep](daemon.md#documentation-upkeep).

| Command | Flags | Does |
|---|---|---|
| `doc set <addr\|module>` | `--text T` (default: stdin), `--verbose` | writes the doc into the source in the language's doc form, verifies it by re-extraction (restoring the file on failure) and reindexes the file. Prints nothing when the doc is written. Never commits |
| `doc set --batch` | `--verbose` | writes several docs in one call, from records on stdin |
| `gaps [module]` | `--kind missing\|stale\|module`, `--limit N` | ranked worklist: stale before missing, then by score (see below) |
| `doc check` | — | re-extracts every added, changed and deleted file of the workspace or `--repo` repo (no file limit, like `codedoc index`), then reports per repo the ok/stale/missing counts of symbols and modules. It also lists placement problems: a Go doc that doesn't start with the name (warning), Javadoc detached by a blank line, Python docstrings in single quotes. Always exits 0 |

`doc set` is silent on success; `--verbose` prints the new doc span and
state. A doc that cannot be written is one line on stderr and exit
status 1:

```
$ codedoc doc set service/service.go#Service --text "Service answers lookups from a Store." --verbose
go-lib@0a93839 dirty
== doc set (1)
go-lib:go:github.com/fixture/lib/service:Service  service/service.go:10  doc ok  Service answers lookups from a Store.

$ codedoc doc set Nope --text "x"
codedoc: doc set Nope: not found; look its name up with codedoc find, or its file with codedoc outline
```

**`doc set --batch`** reads one record per doc from stdin: a line
`=== <address>` (a symbol or a module), then the text up to the next
line starting with `=== ` or the end. Blank lines around a text are
dropped. Every record that can be written is written; each one that
cannot is one line on stderr, `<address>: <what>; <what to do>`, and the
exit status is then 1. The files written are reindexed together, once.
A doc with a line that starts with `=== ` cannot go in a batch: set it
alone.

```
$ codedoc doc set --batch <<'DOCS'
=== store/null.go#NullStore.Get
Get always reports ErrMissing.
=== store/null.go#NullStore.Put
Put drops the value and returns nil.
=== store/null.go#Nope
Nothing.
DOCS
store/null.go#Nope: not found; look its name up with codedoc find, or its file with codedoc outline
```

The two docs that could be written are in the file:

```
$ codedoc lines store/null.go 6-10
go-lib@0a93839 dirty
store/null.go:6-10  in NullStore.Get (7) .. NullStore.Put (10)
6	// Get always reports ErrMissing.
7	func (NullStore) Get(key string) (string, error) { return "", ErrMissing }
8	
9	// Put drops the value and returns nil.
10	func (NullStore) Put(key, value string) error { return nil }
```

```
$ codedoc gaps --limit 3
ts-domain@970edb8
== gaps (3)
ts-domain:./  ts npm-package @fixture/domain  module, fan-in 1, changed 283d ago  [#1] [score 12.12] [doc:missing]
src/records.ts
  ts-domain:ts:@fixture/domain:src/records.ts/MemoryRepo.load  :19  load(name: string): Environment | undefined  public, fan-in 2, changed 283d ago  [#2] [score 6.30] [doc:missing]
  ts-domain:ts:@fixture/domain:src/records.ts/isHealthy  :28  export function isHealthy(env: Environment): boolean  public, fan-in 2, changed 283d ago  [#3] [score 6.30] [doc:missing]

$ codedoc doc check --repo go-lib
go-lib@0a93839
== doc states (1)
go-lib  symbols ok:16 stale:0 missing:7  modules ok:2 missing:0
== doc problems (0)
```

**How docs are written.** `doc set` places the doc where each language
expects it:

| Language | Symbol doc | Module doc |
|---|---|---|
| Go | `//` block above the declaration, starting with the name, wrapped at 80 | the package comment: in `doc.go` when it exists (other files carrying one are named in the note), else replaced where it is, else a new `doc.go` |
| TS/JS | `/** */` above (above `export`) | `README.md` in the directory, `## Purpose` section |
| Python | docstring, triple double quotes | `__init__.py` docstring (created if absent); a file module's docstring |
| Java | Javadoc above the declaration and its annotations; `@param`/`@return`/`@throws` kept | `package-info.java` |

It refuses generated files. Setting a doc marks it `ok`, even when the
text is unchanged: you reviewed it against the current body.

**How docs go stale.** A symbol's doc is `stale` when its body changed
after the doc last changed. Only tokens count, so reformatting does not
stale a doc, but changing the signature does, and so does editing the
spaces inside a string literal (`"a b"` to `"ab"`). A container (struct, class,
interface) depends on its own signature and its members' declarations,
not on their bodies. On a fresh index, the first state comes from git
blame (`-M -C`) of the doc lines against the body lines. A member left
undocumented in a documented Go `const ( ... )`/`var`/`type` group is
`ok`.

**Ranking.** Each gap gets this score:

```
score = visibility × (1 + ln(1 + fan-in)) × kind weight × recency
```

| Factor | Values |
|---|---|
| visibility | 3 exported or public, 1.5 package, 1 private |
| kind weight | modules `1 + ln(1 + files)` (at most 4; a module's fan-in counts the modules importing it), interfaces 3, classes and structs 2, functions and methods 1, fields 0.5 |
| recency | 1.5 when changed in the last 30 days or uncommitted, else 1 |

`gaps` leaves these out:

- tests and generated files;
- `testdata/`, `fixtures/`, `examples/`, `vendor/`, `node_modules/` and
  `third_party/`;
- trivial accessors (at most 5 lines: one `return` of a field, or one
  assignment to a field), and symbols under three lines unless exported;
- methods of documented interfaces;
- synthetic symbols (Java record accessors, TS `it:`/`test:`);
- members documented by their group;
- anything `doc set` would refuse. The head of the list is checked by
  planning the write.

### Index and operations

| Command | Flags | Does |
|---|---|---|
| `index` | `--rebuild`, `--verbose`, `--repo`, `--workspace`, `--format` | indexes the repos of the workspace and the packages they depend on, incrementally unless `--rebuild` (which deletes the database first); embeds what changed when a model is configured. Silent on success. A repo that cannot be indexed is one line on stderr; the others are still indexed and the exit status is 1 |
| `status` | `--repo`, `--workspace`, `--format` | per repo: files, symbols, config keys, unresolved config reads, parse errors, `vectors:N/M`, dirty, indexed time, SCIP state; with a running daemon also its queue, next SCIP and autodoc runs, the upkeep state and the last error |
| `scip run` | `--repo`, `--workspace`, `--format` | brings the index up to date, runs the SCIP indexers, merges precise edges |
| `daemon` | `--workspace` (default: all) | foreground daemon, see [Daemon](daemon.md#daemon) |
| `autodoc --repo R` | `--max-items N` (40), `--max-minutes M` (30), `--commit`, `--allow-dirty`, `--workspace` | unattended documentation, see [Autodoc](daemon.md#autodoc) |
| `mcp` | — | MCP server over stdio, see [MCP server](agents.md#mcp-server) |
| `version` | — | prints the build version; `codedoc --version` (or `-v`) prints the same line |

```
$ codedoc status
8 repos (go-lib@0a93839 +7)
== status (8)
go-lib  main@0a93839 files:8 symbols:24 config-keys:0 unresolved-config:0 parse-errors:0 vectors:0/18 dirty:0 indexed:2026-10-11T00:43:49Z scip:never
go-app  main@f9911b0 files:2 symbols:2 config-keys:0 unresolved-config:0 parse-errors:0 vectors:0/2 dirty:0 indexed:2026-10-11T00:43:49Z scip:never
ts-domain  main@970edb8 files:5 symbols:13 config-keys:0 unresolved-config:0 parse-errors:0 vectors:0/12 dirty:0 indexed:2026-10-11T00:43:49Z scip:never
ts-app  main@6a9560e files:2 symbols:1 config-keys:0 unresolved-config:0 parse-errors:0 vectors:0/1 dirty:0 indexed:2026-10-11T00:43:49Z scip:never
py-lib  main@ce7cd4d files:5 symbols:22 config-keys:0 unresolved-config:0 parse-errors:0 vectors:0/17 dirty:0 indexed:2026-10-11T00:43:49Z scip:never
java-lib  main@b5cd276 files:6 symbols:27 config-keys:6 unresolved-config:2 parse-errors:0 vectors:0/18 dirty:0 indexed:2026-10-11T00:43:49Z scip:never
cfg-repo  main@c93f4d0 files:9 symbols:0 config-keys:23 unresolved-config:0 parse-errors:0 vectors:0/2 dirty:0 indexed:2026-10-11T00:43:49Z scip:never
doc-repo  main@c713ac5 files:6 symbols:0 config-keys:2 unresolved-config:0 parse-errors:0 vectors:0/29 dirty:0 indexed:2026-10-11T00:43:49Z scip:never
daemon: not running
```

`symbols` counts code symbols. `vectors:N/M` counts what is embedded (N)
of what can be (M): documented symbols, undocumented ones with a body
worth embedding, sections and modules, each once however many passages
it has.

`status` only reads. After an edit or a commit it shows `fresh:no` and the
repo line ends with the reason and the remedy:
`(index behind: 3 files changed since indexed; the next query or codedoc
index brings it up to date)`. That is the normal state between a change
and the next query, not a fault; with more changed files than
`CODEDOC_FRESH_MAX_FILES` it says to run `codedoc index` instead.

Writers to one workspace database (index runs, freshness updates, the
daemon) take turns through a write lock. A writer waits for it at most 60
seconds (`CODEDOC_LOCK_TIMEOUT`), then fails and names the process
holding it.

## Languages and config formats

| Language | Files | Module identity | Tests |
|---|---|---|---|
| Go | `.go` | package import path from `go.mod` | `_test.go`, `func Test…` |
| TypeScript, JavaScript | `.ts`, `.mts`, `.cts`, `.tsx`, `.js`, `.jsx`, `.mjs`, `.cjs` | npm name from `package.json` (else the directory path) | `*.test.*`, `*.spec.*`, `__tests__/`; each `it`/`test` call is a symbol |
| Python | `.py` | dotted module path below the `pyproject.toml`/`setup.py` directory (or its `src/`) | `test_*.py`, `*_test.py`, `tests/` |
| Java | `.java` | package name; the repo identity is Maven `groupId/artifactId` or Gradle `rootProject.name` | `src/test/`, `@Test` |

**Documents.** `.md`, `.mdx`, `.markdown` (Markdown), `.rst`
(reStructuredText), `.adoc` (AsciiDoc) and `.txt` (text) files are
indexed as documents and sections, see [Documents](#documents). They
belong to no module.

**Cross-repo resolution.** An import that matches a module identity
another repo provides (`go.mod`, `package.json`, `pyproject.toml`,
`setup.py`, `pom.xml`, `settings.gradle`) resolves into that repo.

**Config formats.** These files are indexed as config keys:

| Format | What becomes a key |
|---|---|
| YAML, JSON | dotted path of mapping keys; list items by index |
| XML | element path, `path@attr`; Maven `<properties>` children also by bare name |
| `.properties`, `.env` | each key |
| HCL/Terraform | `var.x`, `local.x`, resource attributes |
| shell (`.sh`, `.bash`, extension-less files with a sh/bash shebang) | `X=` assignments; `$X` uses |
| templates (`.jsp`, `.tpl`, `.tmpl`, `.j2`, `.template`) | only `${X}` references |

Value previews are one line of at most 80 bytes. Keys that name a
secret (password, token, secret, API or private key and the like) are
masked as `***`.

**Reads from code.** Code reads of config keys are found without knowing
which keys exist. Two sources count as reads:

- accessors: `os.Getenv`, `os.LookupEnv`, `viper.Get*`, `process.env.X`,
  `os.environ[...]`, `os.getenv`, `System.getenv`, `System.getProperty`,
  Spring `Environment.getProperty`, `@Value("${x}")`;
- string-literal arguments that look like a key (`FOO_BAR`, `a.b.c`).

Each read is matched by name against the keys defined anywhere in the
workspace. It is matched again as soon as a config file defines the key.
