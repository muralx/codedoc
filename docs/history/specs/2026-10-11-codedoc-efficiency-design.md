# codedoc: efficiency pass (addendum)

Status: approved 2026-10-11. Extends the v1 design and the two
2026-10-10 addenda; where they disagree on output shape, documentation
upkeep or skills, this file wins.

## 1. Evidence

A study of a real build session on the trial repository, a TypeScript
monorepo (`usage.py`, kept in the session scratchpad, to be committed under `scripts/usage/`) measured:

- 466 codedoc calls, 442 of them documentation upkeep (`doc set`,
  `changes`, `gaps`), 20 lookups; 384 shell reads beside them, 40
  paging-for-a-symbol episodes; the lookup skill never loaded.
- The session iterated on its own code many times after documenting it
  (190 paged reads and searches of repo files), so the docs it wrote had
  readers; they were reached through `grep` and `sed` because nothing
  steered those reads to the index. The cost of writing docs is not the
  problem to solve; the unused return is.
- One model turn cost about 227k cache-read tokens; a paged read
  returned about 364. Turns dominate bytes.
- A result stays in context for about 31 later turns.
- Stable keys are 12–20% of result bytes, the repeated path prefix
  9–14%, doc text 22–34%. `doc set` echoed the doc back. A class body
  over the budget returned no body. `ls` returned 33 times `ls`.
- 3 of 4 dead ends (not found, empty `find`) were followed by shell.

## 2. Principles

1. The agent asks for what it needs and gets exactly that. It does not
   need to know how the tool works, which commands compose, or what a
   stable key is.
2. Every answer finishes the need in one turn: no answer is a dead end,
   no answer requires a follow-up to become useful.
3. Default output is the minimum that answers the question; everything
   else is opt-in.
4. Success of a write is its exit status.
5. Upkeep the tool can do alone is done outside the session.
6. A question whose answer must be extracted from much data is delegated,
   and only the answer enters the session's context.

## 3. Output shape

**Header.** One line, only what changes decisions: `repo@commit` when
the workspace has several repos; `fresh:no` or `dirty` only when true.
A fresh single-repo answer has no header line at all.

**Entries.** `name  path:start-end  signature`, then the fragment that
answers the question (see Fragments). Within one answer a path is
printed in full once per file group and entries of the same file are
listed under it. Names are short qualified names
(`AgentStore.register`), never stable keys. A stable key is printed only
with `--keys`, in JSON, or when two entries would otherwise be
indistinguishable (then the shortest distinguishing prefix: repo, or
package, or version).

**Fragments.** An agent that greps and then reads lines around the hit
wants the relevant text now, with a location to expand from. So an entry
carries the text itself, never a clipped teaser:

- Every fragment is real lines of the file with their line numbers and
  the `path:start-end` they came from, so the agent can widen it.
- What the fragment is depends on the question:
  - `search` / `where`: the lines that matched. For a vector hit, the
    best-matching passage of the embedded text (the doc paragraph, the
    section paragraph or the body lines whose own embedding or term
    overlap is closest to the query), not the first lines. For a keyword
    hit, the matching lines. Several hits in one symbol or section are
    merged into one fragment when they are within the context distance,
    else shown as separate fragments under the same entry.
  - `callers` / `refs` / `impact`: the call or reference line itself.
  - `find` / `outline`: signature and the full doc summary sentence
    (the first sentence, whole, however long; the rest of the doc on
    `--doc`).
  - `symbol`: doc and body in full (see below).
- `-C N` / `--context N` (also `-A N`, `-B N`, as in grep) sets how many
  lines are shown around each fragment; the default is 2 for search-like
  answers and 0 for call sites. `--context symbol` widens every fragment
  to its enclosing symbol or section. Fragments never cross the
  enclosing symbol or section unless `--context file` is given.
- The budget is spent on fragments in rank order: a fragment is shown
  whole or not at all, and the line `N more: <exact command>` says how
  to get the rest. Doc text is never cut mid-sentence and never capped
  at a fixed byte count.
- To expand, the agent reruns with a larger `-C`, or reads the range
  with `codedoc lines path A-B` or `sed`; both work from the printed
  location.

To make passages addressable, embeddings are stored per passage for
long texts: a doc, section or body longer than about 1,200 bytes is
split at paragraph or statement boundaries into overlapping passages
(about 800 bytes, 15% overlap), each with its line range; shorter texts
stay one vector. Search ranks passages and reports the symbol or
section once with its best passages. Bodies of undocumented symbols are
embedded too (signature plus body passages), since undocumented code is
exactly what agents grep for.

*Passages as built.*

- **Splitting** (`internal/passage`, pure: lines in, line ranges out).
  A text of at most 1,200 bytes is one passage. A longer one is cut into
  passages of at most 800 bytes of whole lines; a line is never divided
  (one longer than 800 bytes is a passage of its own). A cut is chosen
  among the places that leave the passage between 400 and 800 bytes:
  next to a blank line (a paragraph, a group of statements) when there
  is one; for code otherwise before the least indented line that does
  not close a block, which is where a statement of the outermost level
  starts; else at the last line that fits. The next passage repeats the
  last lines of the one before, at most 120 bytes of them; what is left
  when it fits in 1,200 bytes is the last passage. Blank lines at the
  ends belong to no passage. One text has at most 40 passages (about
  28 KB): what follows is not embedded.
- **What is embedded**, per kind of owner, always from real lines of the
  file as indexed (a file changed on disk since is skipped until it is
  indexed again):
  - *document, section*: its own lines, heading to the line before its
    first sub-section, each passage under the path and the heading path.
    One passage covers heading and text; several cover text lines only.
  - *documented symbol, doc of at most 1,200 bytes*: one vector over
    path, container, signature, doc and the first 10 body lines, as
    before; its range is the doc comment's lines, or the symbol's when
    the doc's lines are not known.
  - *documented symbol, longer doc*: one vector per passage of the doc
    comment's lines as they are in the file, each under path, container
    and signature. The body is not embedded: the doc describes it.
  - *undocumented symbol*: one vector per passage of its declaration's
    lines, under path and container (and under the signature when the
    passage does not start with it). Left out: symbols of generated
    files, test cases, and the trivial ones, a field, a constant or any
    declaration of one line (an accessor, an enum member, a property).
    They are found by name, crowded the results of prose queries (four of
    the first five hits for "spawn the hub detached" were option fields)
    and doubled what there was to embed. Files over the size limit are
    not indexed at all.
  - *undocumented container* (class, struct, interface, trait, enum):
    one vector over path, container, signature and the names of its
    members; its range is the signature's lines. Its body is not
    embedded again: its members are embedded one by one, and a
    container's body hash covers its members' declarations only, so it
    could not tell when the body changed.
  - *module*: unchanged, one vector over identity, path and doc.
- **Storage** (schema 20). `symbol_vec(symbol_id, ord, from_doc,
  start_line, end_line, text_hash, vec)`, primary key `(symbol_id,
  ord)`. Line ranges are stored relative to the first line of the
  symbol's doc (`from_doc` 1) or of the symbol, and made absolute when
  read: lines added above a symbol move it without changing its hashes,
  and its vectors must follow it without being embedded again. The text
  hash is `p1:<doc hash>:<body hash>`; the prefix names the passage
  scheme, so changing how texts are split re-embeds everything by
  bumping it. The migration keeps every old vector as passage 0: still
  current where the embedded text is the same under the new scheme (a
  documented symbol or a section of at most 1,200 bytes), stale but
  still searched otherwise, so nothing is lost until the next
  `codedoc index` or daemon pass replaces it and embeds the undocumented
  bodies. Nobody has to run anything.
- **Incremental.** Unchanged: staleness is decided in SQL by text hash
  and vector size, vectors are computed outside any transaction and
  written per batch (whole owners, at least 32 texts) in short
  transactions that need no write lock. A limited pass (a freshness
  check, a search: at most 64 stale owners) also gives up above 128
  texts, since one long body has many passages.
- **Counts.** `vectors:N/M` in `status`, and `+N symbols` in `index`,
  count owners (symbols, sections, modules), never passages; M now
  includes the undocumented symbols that are embedded. JSON of `index` adds the number of
  passage vectors written.
- **Search.** A vector scan scores every passage and keeps, per owner,
  its best score and its three best passages. An owner is ranked once,
  by its best passage. Each hit exposes the passages within 0.03 cosine
  of its best one, best first, at most three: inside `internal/query`
  as `rankedHit.passages []Passage{StartLine, EndLine, Score}`
  (absolute 1-based file lines), and in JSON as `extra.passages`
  `[{start, end, score}]`. A hit only the text indexes found has none.
- **Ranking.** Passages alone did not fix what the real model showed
  on the trial repository: sections and module docs above the code asked for, and
  totals over a hundred for a prose query. Ranking now merges text hits
  by how many query terms they hold and vector hits by cosine with a
  floor, fuses the two, weighs test code down and keeps the sections of
  one document apart (v1 design, 7.7). A word used only inside a body
  is found through the names the body references, which costs no
  storage: a text index over bodies was not added (it would grow the
  database by roughly a third of the indexed source and re-extract
  every file).
- **Cost.** The model, not the batching, bounds embedding: about 16
  texts a second on a 28-thread desktop whatever the thread count above
  12, so sessions default to at most 12 threads (`CODEDOC_EMBED_THREADS`)
  and stop burning the rest. Embedding bodies about doubles the texts of
  a repo (the trial repository: 458 to 1,002; this repo: 3,780 to 7,334) and its
  first embedding time; later runs embed only what changed.
- **Dependencies.** `deps.db` has the same table and migration.
  Dependency symbols stay embedded from signature and doc only (their
  files are not read and may be gone): a long doc is split by the lines
  of its stored doc text, which are the comment's lines when there are
  as many of them; otherwise each passage reports the whole comment.
  Undocumented dependency symbols are still not embedded.

**Tags.** Only tags that change what the reader does: `[ambiguous]`,
`[test]`, `[dep name@version]` (once per group), `[stale]`. Fan-in,
fan-out, container, module, doc state, confidence tier of confident
edges: with `--detail` only.

**`symbol`.** Default is location, signature, doc and body: reading a
symbol is the common need, so `--body` becomes the default and
`--no-body` (or `--doc-only`) opts out. A body over the budget is never
dropped: for a container, the member outline with line ranges is
returned and the note says how to get one member; for a function, the
first lines that fit plus the exact continue command.

**Paging and continue hints** stay exact and must work when pasted.

**JSON** keeps every field (keys included); only text output changes.

**MCP** tools return the same compact text.

**`ls`.** Default is path, date and line count only; `--long` adds
size, kind, symbol count and title. **`lines`.** One header line with
the innermost enclosing symbol; the mismatch note only on mismatch.
**`find --deps`** lists names and locations grouped by package, docs on
`--detail`.

A golden test fixes the byte size of a representative set of answers on
the fixture workspace so growth is a visible diff
(`cmd/codedoc/testdata/sizes.txt`, rewritten with `-update`).

### 3.1 As built

What the implementation decided more precisely than the text above.

- **Names.** The name is the qualified name without the file path a
  TypeScript one starts with; a test is `test:<title>`, a section its
  heading, a document its file name. Section titles name their symbol
  (`callers of AgentStore.register`); JSON keeps the stable key in `key`
  and in the title, and adds `name`. Two lines of one section that would
  read the same get `repo:name`, else `module:name`, else the stable key.
  `gaps` and the `--verbose` answer of `doc set` print stable keys: they
  are what `doc set` consumes. The candidates of an ambiguous heading are
  named by their heading path, without their first paragraph.
- **File groups.** A run of two or more consecutive entries of one file
  prints the path on a line of its own and `:lines` per entry, indented;
  an answer about one file (`outline <file>`) prints only `:lines`. A
  run never spans two packages, whose paths are relative to each.
- **Dependency tags.** `[dep name@version]` is printed once per run of
  entries of one package when the dependency entries end the list
  (`find --deps`, candidates, outlines); in a list that mixes them with
  workspace entries (`search --deps`) every dependency entry keeps it. A
  callee that lies in a dependency is tagged `[dep]`; in a call tree
  (`flow`, `how`) it is the dependency symbol itself, by name, with
  `[dep name@version]`.
- **Tags always printed**: `test`, `stale`, `dep ...`, `+N members`,
  `cycle`, `see above`, `see below`, and the candidate count of an
  ambiguous edge. The answers whose tags are their point print them all:
  `gaps`, `changes`, `doc check`, `config`, `deps`, `dependents`,
  `feedback --list`, the entry points of `overview`, the uses of
  `explain <package>`, and `ls --long` (`modified`, `untracked`).
- **Ambiguous edges are never read as certain.** In `callers`,
  `callees`, `refs`, `impls`, `supertypes` and `tests` they follow the
  certain ones in a section of their own, `possibly callers of X (N)`,
  each line starting with `?` and ending with `[1 of K]`, K being the
  candidates of the call (`? createEdgeServer
  packages/edge/src/mcp-server.ts:155  const registration = await
  options.client.register({  [1 of 2]`). An ambiguous callee names its
  candidates: `? Store.get | MemStore.get  path:12  <line>`. A tree
  (`--depth` above 1, `flow`) marks them in place. JSON keeps the
  `ambiguous` tag and `confidence`.
- **Call sites.** `name  path:line  <the line, without its indentation>`.
  `callers` and `callees` list a symbol once with the line of every call
  between the two (several lines are numbered lines under the entry);
  `refs`, `impls`, `tests` keep one entry per referrer and line: two
  references on one line (a type named and instantiated) are one entry,
  both kinds tags under `--detail` and `ref_kinds` in JSON. `supertypes` shows
  the declaration line of the supertype. With `-C/-A/-B` above 0, or
  `--context symbol`, the lines are numbered lines under the entry and
  entries of one symbol whose fragments overlap or touch become one.
  A file that changed since it was indexed gives the signature and
  `[stale]`. `tests --transitive` keeps signature and `via ...`: the path
  it takes is the point. `deps` and `dependents` list modules: an import
  edge has no line.
- **Search fragments.** A `search` or `where` hit is `name  path:lines
  signature  doc sentence` and, under it, the numbered lines of its file
  that matched: the passages of its embedded text closest to the query
  (section 3, passages), then the runs of consecutive lines (at most 8)
  of its doc comment and body that hold the most query terms, three in
  all. The signature lines are never a match of their own: the entry
  line prints them. So a hit whose only passage is its doc comment also
  shows the lines of the body that do what was asked (`search "spawn the
  hub detached"` shows the `spawn(` call under `spawnHubProcess`). Each
  is widened by the context, 2 lines by default (`-C`, `-A`, `-B`;
  `--context symbol` is the whole symbol with its doc comment), kept
  inside the symbol unless `--context file`, and joined with the next
  where they overlap or touch. A file changed since it was indexed
  gives the entry line alone and `[stale]`. Dependency hits and modules
  have no fragments: their files are not read.
- **`where`.** `codedoc where "<words>"` is the search ranking without
  modules, 5 locations by default (`--limit`), with the same fragments.
  When there is one hit, or the first one's fused score is 1.5 times the
  second's (the top of both the text and the vector list, followed by
  hits at the top of neither), the first hit is shown whole as `symbol`
  shows it, under the budget rules of `symbol`; the title still counts
  every location and the line that continues the answer lists the
  others. `--deps` adds dependencies. MCP: `codedoc_where` (query,
  limit, context, deps). A document and the section that is its whole
  text (its one top-level heading) are one hit of `search` and `where`:
  the section, at the better rank of the two.
- **`symbol`.** One line, `name  path:start-end`, then the numbered lines
  of the file from the first line of the doc comment (when it ends at
  most two lines above the declaration) to the end of the symbol: the
  signature and the doc are those lines, so neither is repeated on the
  header line. A doc written elsewhere stays on the header line. The
  answer is within 2% of `sed -n A,Bp` of the same lines with `N<TAB>`
  numbers, so lines stay numbered and there is no flag to drop them.
  `--body` is accepted and does nothing; `--no-body` prints location,
  signature and doc on one line; `--detail` adds kind, container, module,
  fan-in, fan-out and doc state. `find` and the outline of a file tag
  each symbol with kind, fan-in (one query for the list) and doc state
  under `--detail`; an outline line prints the signature as it is
  written, never the kind as a word before it. Over the budget, a symbol with members
  in its file answers with `name  :start-end  signature  first sentence`
  per member under the path, and a document with its heading tree; the
  note names `codedoc symbol X.<member>` and `codedoc lines <file> A-B`.
  Any other body is cut to the lines that fit and ends with `N more
  lines: codedoc lines <file> A-B`.
- **`lines`** has a default budget of 30,000 bytes, not 8,000: the lines
  asked for are the answer, and a range cut at 8,000 bytes cost a second
  call for 26 of the 97 ranges of the study. 30,000 is what a shell tool
  returns of one command before it cuts it.
- **`lines`** takes several ranges; they are printed in line order,
  joined where they overlap or touch, each under `path:A-B  in <symbol>
  (start-end)`: the innermost symbol or section that encloses the whole
  range, its doc comment counted (`in Store (10-15)` for lines 10-12,
  not the member on line 12); when no single one does, the ones the
  range starts and ends in (`in A (26-32) .. B (35-38)`). Several
  ranges of one file share the path line.
- **`ls`** lists the files of a directory together under its name, the
  directories in the order of their first file by `--sort` (so the
  directory of the newest file leads, with its other files); `--long`
  (`-l`) adds size, kind, symbol count, title, the commit date and the
  `modified`/`untracked` tags.
- **Notes.** A note that does not fit the budget is never cut inside a
  word or a command: its sentences are kept whole while they fit, an
  `also:` line drops candidates from its end and counts them (`+N
  more`), and any other sentence that does not fit is left out with
  what follows it.
- **Omissions.** `N more: <the command that ran> --offset K`; a body:
  `N more lines: codedoc lines <file> A-B`, also when not one line of it
  fits (the entry line, then the command for the whole range). No line
  suggests `--out FILE` or a larger budget without a command: when not
  one entry fits, and for sections that are only counted, it is the
  command that ran with the budget that prints the answer whole (`6
  more: codedoc symbol X --budget 1100`, `... 2 more sections (tests,
  exported): codedoc impact X --budget 900`), or the command of the
  section when it has one. A note longer than the budget yields to the
  entries. JSON sections carry the same command in `continue`. Details
  in the v1 design, section 9.
- **`index --verbose`.** The line counting the vectors written is a
  section of its own without a title, after the repos: `== index (N)`
  counts repos only.
- **Header.** No line for a fresh, clean answer of a one-repo workspace;
  `repo@commit` (or the repos an answer draws on) in a workspace of
  several; `dirty` and `fresh:no` without counts. JSON keeps branch,
  commit, the dirty count and `fresh`.

## 4. No dead ends

- `symbol`/`outline`/`callers`… with an address that matches nothing:
  the answer is the nearest candidates (same file's symbols when a path
  was given; otherwise names by edit distance, case-insensitive and
  substring, then full-text hits), exit status 0 with the note
  `no exact match; nearest:`. When exactly one candidate is plausible
  (unique case-insensitive or unique suffix match), it is answered
  directly with the note `answered for <name>`.
- `find` with no match falls through to `search`; multi-word patterns
  go to `search` directly.
- An ambiguous address answers the best-ranked candidate (same file as
  cwd, then non-test, then most referenced) and lists the others in one
  line, instead of failing with exit 3. `--strict` restores the error.
- A path outside the index (ignored, generated, too large, outside the
  repo, an uninstalled dependency) says which, in one line.
- Exit status: 0 answered, 1 nothing at all could be offered, 2 usage,
  4 internal. Exit 3 only under `--strict`.

### 4.1 As built

What the implementation decided more precisely than the text above.

**Where it happens.** One resolver serves every command that takes a
symbol address (`symbol`, `callers`, `callees`, `refs`, `impls`,
`supertypes`, `tests`, `explain`, `flow`, `siblings`, `history`, `at`):
an address that names nothing, or several symbols, is answered there,
so a new command gets the behaviour by resolving its argument the same
way. `outline`, `lines` and `ls` do the same for their path argument,
`deps`, `dependents`, `gaps <module>` and `tests <module>` for a module.

**The answer of a miss** is one section titled `no exact match;
nearest:` (the count follows as for any section), entries as `find`
prints them. It is an answer: exit status 0, nothing on stderr, in JSON
a response without an error object.

```
$ codedoc symbol store/store.go#Nope
== no exact match; nearest: (6)
store/store.go
  ErrMissing  :7  var ErrMissing = errors.New("missing")  ErrMissing is returned when a key has no value.
  Store  :10-15  type Store interface  Store persists string values by key.
  ...
```

**An address with a file part** (`path#Name`, `path:LINE`, a bare
address holding a slash):

- the file is indexed: its symbols. For `path#Name` the near names lead,
  members included: equal ignoring case, then a name that holds the one
  asked for or is held by it (`runMain` for `main`), then a name sharing
  a word of four letters or more (`callTool` for `registerTool`), then
  one a typo away; after them the other symbols the outline of the file
  lists, in line order. For `path:LINE` on a line no symbol encloses it
  is the outline, and the note says `no symbol encloses line N of F (M
  lines)`. A document lists its top-level sections.
- the index has no such path: the indexed files it may mean stand for
  it, by the first of these that finds any: the paths ending with it
  after a slash (`src/bin.ts`), the files with its base name ignoring
  case (`bin.ts`), then, for a path form only, the paths at most two
  edits away (one for a path under eight bytes). Up to five such files
  are searched as above; more, and paths found by edit distance, are
  listed as paths (`packages/edge/src/tools.ts  357 lines`). Of several
  files, the only one below the working directory is the one meant.
- no path is near: the name is looked up alone, first exactly (workspace,
  then dependencies), then as a name that matched nothing (below).
- the file is one of a dependency: the same, from `deps.db`.

**A name** (also the name of a stable key that names nothing) is
compared, ignoring case, with the distinct symbol names of the
workspace, in Go (one query for the names, then one per name kept).
Candidates rank: the whole address (`memstore.get` for `MemStore.Get`);
a qualified name ending with it (`Store.get` for `MemStore.get`); the
name in another container (`Nope.runMain` for `runMain`); a name ending
with it (`RequestHandler` for `setRequestHandler`); a name holding it or
held by it; a name within `max(1, min(3, len/4))` edits. A name under
four bytes is near only what equals it or is a typo away. Production
code precedes tests within a rank, ten candidates at most. When the
workspace has none and the command answers for dependencies, the
packages it uses are compared the same way. Within a rank the
candidates of the repo the query runs in come first. Full-text hits
(`search` without embeddings: no model is loaded for a mistyped name)
are offered only when no name is near, five at most, and only those
that hold every word of the address as a whole word: after candidates
found by name they were unrelated document sections.

**A name the working repo spells otherwise.** A bare name that only
other repos have in the exact case does not win over the repo the query
runs in: its symbols whose qualified name is the address ignoring case
join the candidates and rank first (inside go-lib, `symbol lookup` is
`Service.Lookup`, with py-lib's and java-lib's `Service.lookup` in the
`also:` line). The exact case wins alone where the working repo has no
such name, outside every repo, and under `--strict`.

**One plausible candidate** is answered as if it had been asked for,
and the note ends the answer: `answered for AgentStore.register`. That
is: the only symbol of the best rank that has any, when the rank is one
of the first four above (equal ignoring case, or a unique name ending
with the address); in a file, the only symbol whose name equals the
one asked for, else the only one holding it or held by it; the symbol
enclosing the line in the only file a path stands for; the only path a
path argument may mean, unless it is a typo away (`answered for
packages/edge/src/tools.ts`). Typos and parts of names are always
listed, never chosen.

**Nothing to offer** is one line on stderr and exit status 1, with no
header and nothing on stdout: `codedoc: symbol Zzyzxq: not found`,
`codedoc: outline dist/x.js: not found: no such file or directory in
web-app`. The line says what to do where there is something: `ls
nope: not found: no indexed file under it; codedoc ls --repo go-lib
lists the indexed files`, `package nope: not found: no manifest of the
workspace declares it; codedoc deps lists the packages` (or `nearest:
lint-tool`), `Zzz in @acme/sdk: not found: it exports no name like
that; codedoc api @acme/sdk lists what it exports`, `config key k: not
found: ...; codedoc config '*' lists the keys`, `symbol X at REV: not
found: the file or the symbol may have moved; codedoc history <key>
lists its commits`. `codedoc deps index --embed` without an embedder
(`CODEDOC_EMBEDDER=off`, no model configured, a model that does not
load) is such a line too: `deps index --embed: nothing was embedded:
<why>; <what to do>`. For a name, the
line (or the note of the nearest candidates) names what was not
searched: `not searched: @eslint/js, eslint, prettier, typescript,
typescript-eslint (dependencies no source file imports; deps.include in
the config indexes them)`, the packages no repo that declares them
imports, eight at most and a count of the rest.

**`find`.** A pattern that matches no workspace symbol is looked up in
the dependencies (section 5). When that finds nothing either it is
answered by the search. One word gets the answer of
`codedoc search <pattern>` under the `--kind` and `--lang` search knows,
titled `no symbol named so; search results:`. Several words without a
filter ask where something is done and get the answer of
`codedoc where "<pattern>"`: the top five locations with the lines that
matched, a quarter of the bytes of the twenty hits of a search; with
`--kind` or `--lang` they get the filtered search. The name lookup still runs first for several words, since
a test is named by its title. Paging continues with `codedoc find ...
--offset N`. A glob that matches nothing stays an empty answer, and so
does `--kind section|document`, which already matches headings by
substring. No hit at all is `(0)` and exit status 0, as for `search`.

**An ambiguous address** is answered for the best candidate and the
note names the others in one line, the first three and a count: `also:
HubClient.register (packages/edge/src/hub-client.ts:190),
TestEdge.register (packages/edge/src/testkit.ts:19), +1 more`
(`repo:path:line` in a workspace of several repos; a dependency
candidate as `name (path:line dep pkg@version)`). The order: a file in
the working directory, then one below it (at the root of a repo every
file of that repo is below it: the root says which repo is meant, not
which file); production code before tests; the
symbol whose whole qualified name is the address (a top-level `Find`
before `Store.Find`); the most referenced (fan-in, ambiguous edges
counted); the shortest path; path and line. Workspace symbols precede
dependency symbols. Because every candidate is ranked, a top-level
symbol in a test no longer hides the members with that name: `symbol
register` is `AgentStore.register`, not the helper of a test file. A
module address several repos provide answers the module of the current
repo, else the shortest path.

**`--strict`**, a flag of every query command, restores the errors: an
address that matches nothing is `codedoc: symbol X: not found` and exit
status 1 (the header and the note of the attempt are printed as before
this section, the nearest candidates are not); one that matches several
prints `== candidates (N)` and exits 3; `find` does not search. The
whole qualified name wins before ambiguity is declared, as it did.

**Writes are strict without the flag.** `doc set` (and each record of
`--batch`) never writes to a nearest or a best candidate: the failure is
the one line of section 6, exit status 1 for both not found and
ambiguous; `--strict` on `doc set` makes the ambiguous one exit 3.
`feedback` resolves no address.

**A path outside the index** is one line, `<path> is not indexed:
<why>`, exit status 1, for `outline`, `ls`, and the path of a symbol
address; `lines` prints the lines of the file and gives the reason as
its note (it reads the disk, so it has an answer). The reason is the
first that applies, taken from the code that decides it (`index.Skipped`
mirrors discovery; `exclude.Matcher.Why` is what `Matcher.Path` now
tests):

| Reason | Text |
|---|---|
| a dependency no source file imports | `no source file imports the dependency typescript; name it under deps.include in the config to index it` |
| a dependency waiting, failed or not installed | `the dependency X waits to be indexed; run codedoc deps index` / `indexing the dependency X failed: <error>` / `the dependency X is not installed` |
| a file of an indexed dependency outside its entry points | `of the dependency X only the declarations its entry points export are indexed` |
| `node_modules` of no declared package | `it belongs to a package no manifest of the workspace declares (dependencies of dependencies are not indexed)` |
| a lock file | `dependency lock files are never indexed` |
| `node_modules`, `vendor`, `testdata`, `.git`, build output | `the directory dist/ is excluded (dependencies, fixtures and build output are)` |
| an exclude glob of the workspace | `it matches the exclude glob **/*.gen.ts` |
| `.gitignore`, `.git/info/exclude`, the user's excludes | `git ignores it (.gitignore)` |
| not a regular file | `it is not a regular file` |
| over the size limit (documents excepted) | `it is larger than the limit of 2 MB` |
| a NUL byte in the first 8 KiB | `it is a binary file` |
| a language `languages:` leaves out | `the languages of the repo in the config leave out python` |
| a directory without indexed files | `no file below it is indexed` |
| none of these | `the index has not read it yet; run codedoc index` |

A generated file is indexed (its symbols are tagged, `doc set` refuses
it), so it has no line here. A path that leaves the workspace keeps its
line (`<path>: path is outside the repos of the workspace and the
directories of its dependencies`), now also for `symbol path:LINE` and
`explain <path>`, which used to report not found.

**Exit status**, for the whole CLI:

| Status | Meaning |
|---|---|
| 0 | answered: also the nearest candidates, the best of several, a `find` or `search` without hits |
| 1 | nothing at all could be offered (not found, a path outside the index or the workspace), a failed write, failures a command listed itself, and any failure that is none of the following |
| 2 | usage: an unknown command or flag, a wrong argument count, a flag value that does not parse or is not accepted (`--depth 0`, `--sort color`, a line range that is none) |
| 3 | only with `--strict`: an ambiguous address, after its candidates |
| 4 | internal: a database error, a file system error other than a missing file, a panic (reported as `codedoc: internal error: <value>` with the stack) |

Every error cobra returns before the command runs is a usage error; of
the errors a command returns, those marked as such, and those whose
message starts with the flag they are about (`--depth must be at least
1`, `unknown --sort "x"; ...`), which is how every such check in the
tree is worded. The JSON error object carries the same number in
`code`.

**MCP.** An answered miss and the best of several candidates are
ordinary tool results (the command succeeded). The one-line answers
that end with exit status 1 (`not found`, `is not indexed`) and the
candidates of `--strict` are tool results too, not tool errors: they
say what the index has. A path outside the workspace, a usage error and
an internal error are tool errors.

## 5. Composite questions

One call each, one compact answer, because each is one need:

- `codedoc impact <symbol>`: "I am about to change this." Signature and
  location; callers grouped by file (non-test first, counts when over
  ten per file); implementations/overrides; tests that reach it;
  documents and doc comments that mention it; config keys it reads;
  whether it is exported from its package and used by other repos.
- `codedoc where "<words>"`: "where is this done?" Search across code,
  docs and sections (keywords and embeddings), answered as the top
  locations, each with its enclosing symbol and the matching fragment
  with context (`-C`); a single strong hit is shown with its whole
  symbol.
- `codedoc how <symbol-or-path>`: the existing `explain` plus the first
  two levels of `flow`, under one budget. `explain` and `flow` remain.
- `codedoc api <package>[ <name>]`: a dependency's surface for a need:
  the package's entry modules, or the named type with its members
  (inherited ones folded in), without `--deps`.
- `symbol A B C`: several symbols in one call, bodies shared under one
  budget.

`callers`, `outline`, `find`, `symbol` take dependency names without
`--deps` when no workspace symbol matches (dependencies are searched
second); `--deps` forces both.

As built: every command that answers for a dependency symbol (`symbol`,
`explain`, `callers`, `refs`, `tests`, `impact`, `outline <type>`, and
`find`) looks a name up in the dependencies when no workspace symbol has
it; with
`--deps` both are searched, and a name both have is answered for the
workspace symbol with the dependency's in the `also:` line (section
4.1). `search` keeps the flag: it ranks, it does not resolve a name.
The commands that work on workspace symbols only (`callees`, `impls`,
`supertypes`, `flow`, `siblings`, `history`, `at`, `doc set`)
do not look there, and what they could not search is not named in
their not-found line. `tests <dep symbol>` lists the tests of the
workspace that call or refer to it, with `--transitive` also those
reaching it through the workspace code that does (`via app.run`);
`impact <dep symbol>` is the symbol with its package, then `callers`,
`references` and `tests` (transitive) of the workspace, each continued
by `codedoc callers|refs|tests <addr>`. A dependency symbol has no
implementations, mentions, config keys or exported line there.

### 5.1 As built

- **One budget, shared.** An answer of several questions gives each
  section a share of the budget (`output.Section.Room`): a section that
  fits an equal share of what is left is whole, and the ones that do not
  split the rest equally; what a section leaves of its share goes to the
  sections after it. A section cut by its share does not cost the later
  ones their place, and its omission line names the command of that
  section alone (`output.Section.Command`), not the command that ran.
  JSON keeps every field and spends the budget in order.
- **`impact <symbol>`.** Default budget 3,000 bytes (a summary; the CLI
  flag and the MCP tool share that default). First line: `name
  path:start-end  signature  first doc sentence`. Then, each only when
  it has entries and titled with their number: `callers`, `possibly
  callers`, `references` (the non-call references: without them a type
  has no impact), `possibly references`, `implementations` (a type) or
  `implements or overrides` / `implemented or overridden by` (a member:
  the member of that name its supertypes and subtypes declare), `tests`
  (the list of `tests --transitive`: direct first, then `via ...`; names
  and locations), `mentions in documents and doc comments`, `config keys
  read` (tagged `[undefined]` when no config file defines the key), and
  the line `exported  yes|no (...)  used by <repos>` (repos only in a
  workspace of several; not printed for a document or section).
  - Callers are the entries of `codedoc callers` (one per caller with
    the lines of its calls, non-test first, a file's callers together).
    A file with more than ten call sites prints its first three and
    `N more call sites in this file: codedoc callers X --offset K`,
    K being the rank of the first caller not printed whole; tests and
    references follow the same rule per file (more than ten).
  - Mentions: the document sections (their own text, up to the first
    subsection) and the doc comments of other symbols found by FTS on
    the name, then checked on the lines of the file: the qualified name
    (`Type.member`, `Type#member`, `Type::member`) always counts; the
    bare name of a member only in a text about its container (it names
    the container, or is the container or one of its members); a
    top-level name that is an everyday word (lower-case letters only)
    only written as code (`` `name` `` or `name(`). The entry is the
    section or symbol at the first matching line, with that line. A
    document or section is also mentioned by the sections linking to it.
  - Commands of the omission lines: `codedoc callers X`, `codedoc refs
    X`, `codedoc impls X`, `codedoc tests X --transitive`, and for the
    sections no other command lists, `codedoc impact X --only
    mentions|config|impls`. `--only <section>` prints one section alone
    and `--offset` then pages it; `--offset` without `--only` is a usage
    error (exit 2). Follow-up commands carry `--workspace`/`--repo`
    when the call did, and name the symbol by its stable key when the
    address was answered for a nearest or a best candidate (section 4),
    so that every command asks about the same symbol. `-C/-A/-B` widen the call lines; a crowded file then keeps whole
    callers.
- **`how <symbol|path>`.** A symbol: the `explain` stack and its top
  callers, then `flow --depth 2` whose first line is the symbol without
  the signature and doc the stack already printed; explain's top callees
  are left out (the tree lists them in call order). A code file: the
  `explain` of the file, then the tree of each of its entry functions
  (top-level functions, or methods named `main`, that call something and
  that no indexed symbol calls), at most three, the others named in the
  note; a test file has none. A directory, a module, a document or a
  dependency is answered as `explain` answers it, and so is an address
  nothing has (the nearest candidates, section 4). A code file named
  without its directory (`bin.ts`) is answered when exactly one indexed
  file can be meant, with the note `answered for repo:path`. Omission lines name
  `codedoc explain <target>` and `codedoc flow <symbol> --depth 2`.
- **`api <package> [name]`.** The package is a name a manifest declares,
  `name@version`, its id, or an import specifier below it (one module).
  - Package: `name@version  <install dir>  <first README sentence>`;
    the exported top-level names of the main module, then of up to six
    modules the workspace uses most (by edges from workspace code), at
    most 40 names each, names the workspace uses first, then types,
    functions, the rest: `name  kind`, a function with its signature
    from the name on; then `modules of <pkg>`, every module with its
    number of names in the order of `codedoc outline <pkg>`, which
    continues it (a directory of more than ten modules lists three).
    `codedoc api <specifier>` lists one module whole and continues a
    cut list; `--offset` on the package continues the main module.
  - Name (also `Type.member`, inherited ones included): location on
    disk, signature, whole doc, `[dep name@version] [from <module>]`;
    for a type, `members of T`: its non-private members, then the
    public inherited ones named `Super.member` under the file that
    declares them (at most 30), each with the modifiers before its name
    and its signature from the name on. Doc sentences of names and
    members with `--doc` or `--detail`. Several symbols of one name: the
    one the workspace uses most, the others in the note.
  - A name the package does not export: `nearest names in <pkg>` (same
    ignoring case, ending with it, containing or contained, then a typo
    away: the ranking of section 4), each with its module; exit 0. None:
    not found, exit 1, one line (section 4.1).
  - A declared package that is not indexed answers one line and exit 0:
    unused (`...no source file imports it, so it is not indexed; name it
    under deps.include in the codedoc config and run codedoc deps
    index`), missing (`...not installed; install the dependencies and
    run codedoc deps index`), failed, pending, or a workspace package. A
    name no manifest declares is not found (exit 1), one line with the
    nearest declared names.
- **`symbol A B C`.** Each address is resolved on its own, as section 4
  resolves it: one answered for its only plausible or its best
  candidate prints its source like any other and the note says so after
  the address (`errmissing: answered for ErrMissing`, `Get: also:
  ...`); one that names nothing is one line, `Nope  not found; nearest:
  A, B, C` (names, at most five) or plain `Nope  not found`; under
  `--strict` an ambiguous one is `Get  ambiguous (4 candidates);
  codedoc symbol Get --strict lists them`. The query fails (exit 1, or 3
  for a strict ambiguity) only when no address was answered or had
  candidates. Sizes are measured whole; a
  symbol larger than its share is answered by its member outline when
  that fits the share, else cut to the lines that fit and continued
  with `codedoc lines`. `--no-body`, `--doc-only`, `--context` apply to
  all.
- **Display.** One symbol listed at several places of a call tree
  prints the same line each time (no stable key: it would not tell the
  lines apart).
- **Inherited members.** A dependency member asked through a type that
  inherits it says so in the note of `symbol`, `callers`, `refs`,
  `tests` and `impact` (`Server.notification is declared on Protocol`);
  `[declared on Protocol]` stays a tag of `--detail`.

## 6. Writes are silent

`doc set`, `feedback` (filing a report and `--clear`; `--list` is a
question), `index`, `deps index`, `deps prune`: no output on success
(exit 0, nothing on stdout or stderr), one line on stderr on failure
saying what and what to do. `--verbose` prints what was done: the answer
these commands printed before this pass, byte for byte. `deps prune` has
no `--yes`: it deletes as it always did and that form is the silent
write; `deps prune --dry-run` is a question and always prints its list.

**JSON.** `--format json` still prints the result object of every one
of these commands (a machine asked for it, and JSON keeps every field);
only text is silent. `--out FILE` still writes the result to the file;
its summary on stdout is silent like the answer.

**Failures.** A failed `doc set` prints `codedoc: doc set <address>:
<what>; <what to do>` and no candidates list (the candidates of an
ambiguous address are named in the line: the first three keys and how
many more); `--verbose` prints the candidates as before. The exit status
of a single `doc set` is unchanged by this section (section 4 redefines
exit statuses for every command).

**Warnings.** A run that succeeded but left something to act on says so
in one line on stderr starting `warning:`: an identity several repos
provide, an embedding model that cannot load, and, because the summary
that used to show them is no longer printed, package versions that
failed to index or manifests that could not be read (`warning: <the
note of the summary>; see them with codedoc deps`). A clean run prints
nothing at all.

**`index` with a failed repo.** `index` no longer stops at the first
repo it cannot index: it prints `<repo>: <what>; fix that and run
codedoc index --workspace W --repo <repo>` for each, indexes the others
(dependencies, resolution and embedding cover the repos that were
indexed) and exits 1. `--verbose` and `--format json` print the result
of the repos indexed; in JSON the last line of stdout is the usual
`{"error": "N of M repos failed: ...", "code": 1}`. `scip run`, which
needs every target repo indexed, still stops with one error.

**`doc set --batch`.** `codedoc doc set --batch` writes several docs in
one call. Stdin carries one record per doc: a line `=== <address>`
(a symbol or a module, as `doc set` takes them), then the doc text up to
the next line starting with `=== ` or the end; the blank lines around a
text are dropped, the ones inside it kept. A doc with a line that starts
with `=== ` cannot be written in a batch (set it alone); a line that is
only `===` is text. `--batch` excludes a positional address and
`--text`.

- It writes every record it can and prints one line per record it could
  not, on stderr: `<address>: <what>; <what to do>` (no `codedoc:`
  prefix, no summary line). Parts of stdin that are no record are
  failures too: `line N: text before the first "=== <address>" line;
  ...`, `line N: a record without an address; ...`, `<address>: no doc
  text; ...`. Exit 0 when every record was written, 1 when any failed.
  Stdin without any record is one error and exit 1.
- The addresses are resolved against the index as it was before the
  first write (one freshness check per repo), the docs are written file
  by file with the same verification as a single `doc set`, and the
  files written are re-indexed together, each repo once, at the end.
  Two records for one symbol: the later text wins.
- Every write goes through the same writer as a single `doc set`, so
  under `CODEDOC_RUN_ID` every file is journaled before its first change
  and an autodoc run that used a batch is reverted like any other.
- `--verbose` prints one `== doc set (N)` section with an entry per doc
  written; `--format json` the same as JSON, then, when records failed,
  `{"error": "N of M docs failed: ...", "code": 1}`.

**MCP.** A write tool call that succeeds answers `ok`: a tool result
must have content, and the CLI printed none. This holds for
`codedoc_doc` with action `set`, `codedoc_feedback` filing a report and
`codedoc_deps` with action `index` or `prune` (the tools no longer take
`format` or `dry_run`, see 9.1). `codedoc_doc` action `set`
takes either `target` and `text` or `docs`, a list of `{address, text}`
that runs as one `doc set --batch`; the tool refuses a text with a line
starting with `=== ` and an address that is empty or spans lines. A
batch in which records failed is a tool error whose text is the failure
lines alone.

**Autodoc.** The prompt tells the headless model to gather context for
every key of the group and then write all the docs in one `codedoc doc
set --batch <<'DOCS'` call, that no output and exit 0 are the success
(nothing to check with another command), and that a line `<key>:
<what>; <what to do>` is a doc that was not written while the others
were; a failed key is set again with a batch of the failed keys only.

## 7. Documentation upkeep runs outside the session

The session never checks doc states and is never told to. `changes`
keeps existing for humans; the skills stop mentioning it.

**Trigger.** The daemon already watches every repo. On every tick
(30 s) a repo is ready for upkeep when all hold, checked in this order:

1. `upkeep.enabled` in its workspace;
2. the workspace spent less than `daily_cost_usd` today;
3. no file event for `quiet` (default 5 minutes). A batch of watcher
   changes counts unless it holds only files the running upkeep wrote
   itself (the files of its journals and the temporary files they are
   renamed from). The daemon's start, the end of a run and a failed
   check count as events too, so nothing is retried sooner than `quiet`
   later;
4. HEAD is on a branch (not detached, not unborn);
5. the working tree is clean: everything committed, and no untracked
   file that is not ignored. What a run that died left behind is
   restored from its journal first;
6. there are candidates: symbols changed since the last upkeep on this
   branch whose doc is stale or missing and that the gaps ranking (v1
   11.1) does not leave out.

A session lock does not hold upkeep back; clean and quiet is the
condition. The older scheduled job (`autodoc.every`) keeps its lock
rule: it takes the repo's top gaps wherever they are and puts back
files changed outside its journal, so it stays away from a repo a
session works in. A manual `codedoc autodoc` is unchanged.

**State per branch.** The workspace database keeps, per repo and
branch, the last documented commit and when a run last finished there
(`upkeep_branch`, schema 19; it survives daemon restarts, not
`index --rebuild`, after which the first-run rule applies again). A
branch switch, rebase or checkout changes nothing by itself: each
branch has its own row, read when that branch is checked out, clean and
quiet.

**Candidates.** The commits in scope are the branch's own: reachable
from HEAD, not from the default branch (the branch `origin/HEAD` names,
else `main`, else `master`; both the local ref and the one on origin are
left out; on the default branch itself, or in a repository without one,
nothing is left out), merge commits aside, and not reachable from the
last documented commit. Without a last documented commit, or when it is
no longer an ancestor of HEAD (a rebase, a reset), it is the first-run
rule: the newest `first_run_commits` (default 20) of the branch's own
commits; the commit they started from is then recorded as the last
documented one, so a retry or the next check looks at the same commits
plus what came after. A symbol is a candidate when it was added or
moved or its body or doc differs between the parent of the oldest
commit in scope (the empty tree for a root commit) and HEAD, the way
`changes` tells it, it lives in a file those commits touched, and it is
in the gaps ranking; a module is a candidate when such a file lies
directly in its directory and the module is in the gaps ranking. A
clean branch without candidates is recorded as documented up to HEAD.

**Action.** The autodoc runner (headless `claude -p`, journal, guard,
cost cap; groups of `autodoc.group_size` keys) documents the candidates
in rank order, at most `max_items` per run, as one unit: no group is
committed on its own and every group's journal is kept, its marker
locked, until the run ends. Then

- when `commit: true` (the default for upkeep), the files the run wrote
  are committed as one commit on the same branch with the repository's
  configured identity, subject `docs: document N changed symbol(s)`,
  the documented keys in the body, one per line. Upkeep never pushes;
- when `commit: false`, the docs stay in the working tree, which is
  then not clean: upkeep waits until they are committed.

The branch's last documented commit advances when no candidate is
left: to the docs commit when one was made, else to the HEAD the
candidates were computed from. Either way the next check finds nothing
to compare (with `commit: false`, once the docs are committed, the
range holds only symbols whose doc is now `ok`). While candidates are
left (more than `max_items`, or the budget ran out) the commit stays
and only the time of the run is recorded; the next run takes the rest.
A group that fails on its own (a key not `ok` afterwards, a claude
error) is restored at once as under `autodoc`, and its keys are no
candidates again until the branch's state advances or the daemon
restarts; `codedoc autodoc` can take them.

**Interruption.** If the tree stopped being clean or quiet while the
run was going, the run is undone as a whole and retried after the next
quiet time; nothing is left half-applied and the last documented commit
does not move. That is: a watcher batch that is not the run's own (the
daemon cancels the run, which kills claude), or, found by the guard
after a group, a path changed outside the run's journal or a moved
HEAD. Under upkeep such a change is taken for someone else's work, not
the agent's: it is left exactly as it is (the scheduled `autodoc` puts
such paths back to HEAD; upkeep never does). Every journal is restored,
newest group first. A journaled file edited since the run wrote it is
kept and logged, as before; one that git now reports unchanged was
committed by someone meanwhile and is left alone, since putting its
original back would leave an uncommitted change nobody made.

**Failure.** A run that ends with an error (a hook refuses the docs
commit, git fails) is reverted the same way, the paths it staged are
unstaged, and no other run starts on that repo until HEAD is another
commit or the daemon restarts, so a failure that repeats is paid for
once. A run that finished but documented nothing although groups
failed says so as the upkeep error. A repo whose directory is missing
is not checked.

**Cost.** One run may spend `max_cost_usd`: each claude run gets what
is left of it (at most `autodoc.max_cost_usd`), and no group starts
with less than a cent left. What a run spent is added to the
workspace's spend of the day, kept in the database (`upkeep_spend`),
the day being the local date, so the cap starts over at local midnight.
A claude run that ended without reporting its cost (killed on an
interruption, a timeout, a crash) counts at the budget it was given,
the most it can have spent. Once the day's spend reaches
`daily_cost_usd`, upkeep stops for the day and `status` says so.

**Without the daemon** nothing runs; `codedoc autodoc` remains the
manual way. `codedoc status` shows per repo, after the daemon's other
fields:

```
... queue:0 upkeep:on pending:3 last-upkeep:2026-10-11 09:12:44
... queue:0 upkeep:on (uncommitted changes) pending:3
... queue:0 upkeep:on (daily cap reached) pending:3
... queue:0 upkeep:running pending:3
... queue:0 upkeep:off
... queue:0 upkeep:on pending:1 upkeep-error: run undone: a.go changed during the run; it will be retried
```

`upkeep:on` or `off`; in parentheses what holds it back (`daily cap
reached`, `waiting for quiet`, `HEAD is not on a branch`, `no commit
yet`, `uncommitted changes`, `not a git repository`, `not indexed`), or
`upkeep:running`; `pending:N`, the candidates the last check counted;
`last-upkeep:`, when a run last finished; `upkeep-error:`, why the last
check or run failed or was undone, cleared by a run that finishes. JSON
carries the same per repo entry as `extra.upkeep`: `enabled`,
`running`, `pending`, `waiting`, `daily_cap_reached`,
`spent_today_usd`, `last_run`, `last_error`. Without the daemon a
workspace with upkeep enabled gets `upkeep:needs-daemon` on every repo
(JSON `{"enabled": true, "needs_daemon": true}`) and the note says
that nothing is documented without it.

**Config.**

```yaml
workspaces:
  web-app:
    upkeep:
      enabled: true        # default false: opt in per workspace
      quiet: 5m            # absent or 0: 5m
      max_items: 20        # absent or 0: 20
      commit: true         # default true
      first_run_commits: 20  # absent or 0: 20
      max_cost_usd: 1.00   # per run; absent or 0: 1.00
      daily_cost_usd: 5.00 # stop for the day when reached; absent or 0: 5.00
      model: sonnet        # model of the claude runs; absent: sonnet
```

Config load fails on a negative `max_items`, `first_run_commits`,
`max_cost_usd` or `daily_cost_usd`, on a `quiet` that is no duration,
and on an unknown key. The `autodoc` section and `codedoc autodoc` are
unchanged.

**Skill.** Documentation is a practice the tool supports, not a cost it
tries to avoid. `codedoc-document` says:

- While writing or changing code, always write the doc comment for what
  only this session knows: intent, invariants, side effects, error and
  concurrency behaviour, and the reason for a non-obvious choice. That
  knowledge is lost when the session ends; no later run can recover it
  from the body.
- Do not restate the signature. Summary sentence first.
- Write the comment in the source with the edit that writes the code
  (the normal edit, no extra command); `doc set` is for adding or
  replacing a doc on code that is not being edited.
- Never run doc checks or `changes` to look for stale docs; upkeep finds
  and fills what is missing or stale after the commit.

It keeps the short "what a good doc contains" list per level (function,
type, module).

## 8. Delegated questions: the explorer subagent

The plugin ships `plugin/agents/codedoc-explorer.md`, a Claude Code
subagent on a small model (`model: haiku`), tools limited to Bash
restricted to `codedoc` and Read. The main session delegates a question
("how does registration work and who calls it", "what breaks if I change
the message schema", "find where retries are configured"); the explorer
runs as many codedoc calls as it needs in its own context and returns:

- the answer in at most about 300 words;
- every claim followed by `path:line` (or `path:start-end`);
- a final `checked:` line listing what it looked at and a `not found:`
  line for anything it could not establish. It never guesses.

The lookup skill says when to delegate: more than two lookups expected,
a question across several files or packages, anything about a
dependency's usage, "where/how/why" questions; and when not to: one
symbol, one file outline, a known line range.

A second agent, `codedoc-documenter`, does what `doc set` in session
did, for when the user explicitly asks for documentation now: it is
given addresses, writes docs with `doc set --batch`, returns only a
count and failures.

### 8.1 As built

- **Files.** `plugin/agents/codedoc-explorer.md` (48 lines) and
  `plugin/agents/codedoc-documenter.md` (55 lines). Claude Code names
  them `codedoc:codedoc-explorer` and `codedoc:codedoc-documenter`.
- **Front matter**: `name` (the file name), `description` (one
  paragraph: it is what the main session delegates on), `tools: Bash,
  Read`, `model: haiku`. These are among the fields Claude Code reads
  for a plugin's agents; `hooks`, `mcpServers` and `permissionMode` are
  ignored there.
- **Bash is not restricted by the front matter.** The `tools` field
  takes tool names; the documentation of Claude Code 2.1 describes no
  `Bash(codedoc:*)` form for it (the only parenthesised form is
  `Agent(type)`, for a main-thread agent), and `claude plugin validate`
  accepts any value, so it proves nothing either way. The restriction
  is therefore stated in the body of each agent ("With Bash run only
  `codedoc` commands: no other shell command, no edits") and can be
  enforced by a permission rule in the user's settings. Without Edit
  and Write the agents cannot change files except through `codedoc doc
  set`.
- **Explorer body**: the rules (only codedoc, never guess, follow an
  answer's last line only when the rest matters), the table of need to
  command (`where`, `symbol` with several names, `symbol path:LINE`,
  `impact`, `how`, `api`, `outline`, `lines`, `ls`,
  `callers`/`refs`/`tests`, `search`, `config`) and what it returns.
- **Documenter body**: read with `symbol A B C` (and `impact`,
  `outline` for context), write every doc in one `doc set --batch`
  heredoc, treat no output and exit 0 as success, set failed records
  again once, return the count and the failures left. It carries the
  list of what a good doc contains per level.
- **Test** (`cmd/codedoc/plugin_test.go`): two agent files; front
  matter parses with the four fields; the name is the file's; the model
  is an alias (`haiku`, `sonnet`, `opus`, `fable`, `inherit`); tools
  only from `Bash`, `Read`; the body confines Bash to codedoc; at most
  60 lines; every `codedoc <command>` in a skill or an agent is a
  command of the binary.

## 9. Skills and fixed cost

- `codedoc-lookup`: at most 60 lines. A table of need → command
  (`symbol`, `outline`, `impact`, `where`, `how`, `api`, `ls`, `lines`), the
  delegate rule, and the cross-check line (`lines` + `feedback`).
  Nothing about addresses, keys, budgets, tiers or freshness: answers
  explain themselves.
- `codedoc-document`: at most 40 lines (see §7).
- `codedoc-maintain`: unchanged in scope, loaded only when asked.
- A SessionStart hook is still not registered by default.
- MCP: tool descriptions one sentence; rarely used inputs dropped from
  schemas (they remain on the CLI). New tools for `impact`, `where`,
  `how`, `api`; `codedoc_graph`, `codedoc_explain`, `codedoc_flow`
  stay.

### 9.0 Skills as built

| Skill | Lines | Characters | Est. tokens (chars / 4) |
|---|---|---|---|
| `codedoc-lookup` | 41 | 1,956 | 489 |
| `codedoc-document` | 37 | 1,501 | 375 |
| both | 78 | 3,457 | 864 |

- **Lookup.** Its description is the trigger the study found missing:
  "Use when about to read or search this repository's code with grep,
  rg, sed, cat, head, find, ls or Read", followed by the needs (where
  something is, reading a function, who calls it, what a change breaks,
  how a feature works, a dependency's API, which files changed). The
  body is the table (nine rows), one sentence saying that every answer
  ends with the way to get more of itself, the delegate rule and the
  cross-check.
- **Document.** Its description triggers on writing or changing code,
  not on doc states. The body is section 7's four points, the list per
  level, and `doc set` or the `codedoc-documenter` agent for code that
  is not being edited.
- **Maintain** covers silent writes and `--verbose`, exit statuses and
  `--strict`, `gaps`, `doc check`, `doc set --batch`, upkeep (config
  block, conditions, status fields), autodoc, the workspace config,
  dependency states and `deps index --embed`, SCIP and the daemon.
- **Test.** The plugin test fails when lookup passes 60 lines, document
  40, or the two together reach 4,800 characters (1,200 tokens); when
  the lookup description loses a trigger or its body names stable keys,
  budgets, freshness or tiers; when the document skill names `changes`,
  `doc check` or `gaps`.
- The hook `plugin/hooks/session-start.sh` is unchanged and still
  registered by nobody.

### 9.1 MCP as built

Nineteen tools, each described in one sentence of at most 130 bytes;
`tools/list` went from 24,163 bytes for fifteen tools to 17,664 for
nineteen. Every tool takes `workspace`, `repo`, `budget` (default
6000, `codedoc_impact` 3000; capped at 60000) and `offset`; `format`
and `out` are gone from every schema, and with `out` the files under
`$TMPDIR/codedoc/<pid>/` and their cleanup. An input the schema does
not have is refused by the client's validation.

**Omission lines name tool calls.** A tool result is the text of the
CLI, except the lines that name the command for what a budget left
out: an MCP caller has tools, not a shell, so the server rewrites the
command as the tool and its inputs, `8 more: codedoc_find
{"pattern":"*Store*","budget":400,"offset":3}`, `8 more lines:
codedoc_lines {"path":"go-lib:service/service.go","range":"16-23"}`,
`... 2 more sections (callers, tests): codedoc_impact
{"symbol":"Store.Get","budget":900}` (`internal/mcpserver/hints.go`:
one table from command word to tool, positional inputs and the fixed
`relation`, `mode` or `action`; flags are the inputs of the same name,
typed as the tool declares them). The `workspace` input of the call
goes with every such call. `codedoc tests X --transitive`, which
`codedoc_graph` cannot ask, becomes `codedoc_impact` with `only:
tests`, the same list. A command with a flag its tool has no input for
is left as the CLI prints it. Neither `--out FILE` nor a flag appears
in a rewritten line. The rewriting happens after the CLI rendered under
the budget, so a result may pass the budget by the bytes the JSON form
of such a line adds; the entries and source lines never do.

| Tool | Inputs besides the common four | Dropped (CLI only) |
|---|---|---|
| `codedoc_overview` | — | |
| `codedoc_outline` | `target`, `detail` | `sort`, `reverse`, `deps` |
| `codedoc_ls` | `paths`, `since`, `sort`, `long` | `reverse`, `until`, `kinds`, `lang`, `depth`, `dirs` |
| `codedoc_lines` | `path`, `range` | |
| `codedoc_find` | `pattern`, `kind`, `deps`, `detail` | `lang` |
| `codedoc_symbol` | `symbol`, `names` (several addresses), `no_body`, `deps`, `detail` | `body` (the default now), `doc_only`, `context` |
| `codedoc_impact` | `symbol`, `only`, `context`, `detail` | |
| `codedoc_how` | `target` | `deps` |
| `codedoc_api` | `package`, `name`, `detail` | `doc` (`detail` prints the doc sentences) |
| `codedoc_graph` | `relation`, `target`, `depth`, `no_tests`, `context`, `detail` | `group_by`, `kind`, `transitive`, `cross_repo` |
| `codedoc_config` | `key` | `defines`, `reads` |
| `codedoc_search` | `query`, `kind`, `deps`, `context`, `detail` | `lang`, `limit` |
| `codedoc_where` | `query`, `limit`, `context`, `deps` | |
| `codedoc_explain` | `target` | `deps` |
| `codedoc_flow` | `symbol`, `depth` | `documented_only` |
| `codedoc_history` | `mode`, `since`, `symbol`, `ref`, `diff` | `limit` |
| `codedoc_doc` | `action`, `target`, `text`, `docs`, `kind` | `limit` |
| `codedoc_deps` | `action` (list, index, prune) | `all`, `dry_run` |
| `codedoc_feedback` | `text`, `path`, `list` | `key`, `cmd`, `since` |

A tool is one line in `registerTools` (name, sentence, input type) and
an input type with a `build` method; the tests name the tools and their
inputs in one table each.

## 10. Measuring

`scripts/usage/usage.py` (the study's script, committed) reports calls,
result sizes, token-turns, paging episodes and dead ends for a project's
transcripts. `scripts/usage/replay.py` replays every recorded codedoc
lookup and every servable shell read of a transcript against two
binaries and prints bytes and calls for each. Acceptance for this pass,
on the trial repository's transcripts recorded on 2026-10-10:

- replayed codedoc lookups: at least 30% fewer bytes than the previous
  build, with the same information the agent used;
- servable shell reads replayed through the new commands: fewer bytes
  in total than the shell commands returned, and fewer calls;
- `doc set`: 0 bytes on success;
- every recorded dead end now answers in one call;
- skills: lookup + document together under 1,200 tokens.
