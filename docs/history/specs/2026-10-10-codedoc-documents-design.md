# codedoc: documents and file listing (addendum to the v1 design)

Status: approved 2026-10-10, implemented (extractor version 7, schema
migration 13; `codedoc_lines` added with the merge into the dependency
index). Extends `2026-10-09-codedoc-design.md`. Section 6 records
what the implementation decided where this text was open or wrong.

## 1. Problem

The trial on a TypeScript monorepo showed three habits codedoc does not serve:

- reading one section of a long design document with
  `sed -n '/^## 6\./,/^## 7\./p' spec.md` (codedoc indexes no headings, so
  `outline spec.md` is empty);
- paging through a source file with `sed -n 1,105p file`, then
  `sed -n 150,200p file`, to find a symbol (reading a range whose line
  numbers are already known is fine);
- orienting with `ls -lt`: agents pick files by name and date, then read
  them.

Any document codedoc does not index is a document an agent reads whole.

## 2. Documents

**Files.** Every `.md`, `.mdx`, `.markdown`, `.rst`, `.adoc`, `.txt` file
that discovery lists (git-tracked or untracked-and-not-ignored, built-in
exclusions apply) is a document. Family `doc`; a workspace can drop it
with `languages:` like any other (`doc`, or one of `markdown`, `rst`,
`asciidoc`, `text`). Files over 2 MB are listed but not sectioned: they
have a file row and no symbols, and `outline` of one says so (`docs/x.md
is too large to section (2.4M, over 2.0M): read it with codedoc lines
repo:docs/x.md 1-200`).

**Sections.** Each heading starts a section that runs to the next heading
of the same or a higher level; nested headings are its members.

- Markdown/MDX: ATX (`#`) and setext headings, ignoring fenced code and
  HTML comments (`<!-- ... -->`, over one line or several).
- reStructuredText: underline/overline titles, levels in order of first
  appearance. AsciiDoc: `=` titles. Plain text: no sections.
- A document is a symbol of kind `document` (qualified name: empty, the
  path addresses it); a section is a symbol of kind `section` whose
  qualified name is the heading path joined with ` > `
  (`6. Delivery > 6.2 Retries`). Duplicate paths get `~N` as elsewhere,
  and the sections below a duplicate carry its suffixed path
  (`Notes~2 > Details`).
- Stable keys use the language `doc` and the file path in place of a
  module: `repo:doc:docs/spec.md:6. Delivery > 6.2 Retries`, and
  `repo:doc:docs/spec.md:` for the document. They never carry a hash
  suffix, so a heading may end like one (`Issue #1234`).
- Signature: the heading line. Doc: the section's own text up to its
  first sub-heading, trimmed to 600 bytes at a sentence end, used for
  one-line summaries and snippets. Body: the whole section, sub-sections
  included.
- YAML front matter is skipped as content; its `title` and `description`
  feed the document's signature and doc. A block between `---` lines that
  is not valid YAML is skipped all the same, its two fields read line by
  line. Without a front-matter title the signature is the first heading,
  else the file name; without a description the doc is the first
  paragraph.

**Addressing.** `path#Heading text`. The steps, the first that matches
any section wins: the heading or the heading path as written; the same
ignoring case, or the heading's anchor slug (`62-retries`); the numbering
the heading starts with, alone (`spec.md#6`, `spec.md#6.2`, with or
without the closing dot); a prefix of the heading; a substring of the
heading; a substring of the heading path. Several matches at one step are
ambiguous and list the candidates as usual. `path` alone, or `path#`, is
the document.

**Queries.**

- `outline doc.md`: the heading tree with line ranges and sizes. Entries
  carry the line range only (`46-53`), under the title line naming the
  file: a long path repeated on every heading would use up the budget.
- `symbol "doc.md#6. Delivery"`: that section only (the source is the
  default since the efficiency addendum; `--body` is accepted and does
  nothing). A section larger than the budget is cut to the lines that
  fit and ends with the command that continues it (`4 more lines:
  codedoc lines doc-repo:docs/design.md 24-27`); one with sub-sections,
  and a document, answers with its heading tree.
- `find` matches headings with `--kind section|document`: any heading
  (or document name or title) containing the text, ignoring case; a glob
  matches as for code. Without `--kind`, an exact heading matches too.
- `search` covers sections: FTS over heading plus section text, and
  vectors. A section is embedded from heading path plus its own text
  (all of it, one vector per passage when it is longer than 1,200 bytes:
  efficiency addendum, section 3), so every section is "documented" for
  embedding purposes. Results from documents are tagged `[doc]`, in `find` as well.
  The own text of each section lives in a text index of its own
  (`section_fts`), ranked as a third list next to symbols and modules.
- `explain doc.md` prints the document's front matter title, first
  paragraph and heading tree.
- Sections and documents never count in doc coverage, `gaps`,
  `doc check` or autodoc; `doc set` refuses them. `overview` and `status`
  count code symbols only. `changes --since` lists changed sections (kind
  `section`, tagged `added`, `removed`, `moved` or `text-changed`) without
  doc state.
- Links: a relative Markdown link (inline, image or reference
  definition) to a document of the same repo is a `references` edge
  (reference kind `link`, confidence `path`) from the section it is
  written in to that document, or to the section whose anchor slug the
  fragment names, so `refs doc.md` and `refs "doc.md#6"` answer "which
  docs point here". A link to a file that is no document makes no edge:
  only documents have a symbol to point at. Links live in a table of
  their own (`doc_link`) and the edges of a repo are derived again
  whenever one of its documents is written or deleted, since any of them
  may be the target. Mentions of code symbols are not resolved.

## 3. File metadata and `codedoc ls`

The `file` row already held `mtime`, `size`, `lines` and, when the repo
is a git checkout, `last_commit_at` and `last_commit` of the last commit
touching the file (v1 section 7.6: filled in bulk from one
`git log --name-only` pass per index run, incrementally from the last
indexed head); `ls` exposes them. Migration 13 adds `title`. Untracked
or modified files show `mtime` with a `modified` or `untracked` tag and
no commit date.

`codedoc ls [path|glob ...]` replaces `ls -lt`, `find -newer` and
`tree`:

| Flag | Meaning |
|---|---|
| `--sort mtime\|committed\|name\|size\|lines` | default `mtime`, newest first |
| `--reverse` | invert the order |
| `--since D` / `--until D` | by mtime; a duration back from now (`30m`, `3h`, `2d`, `1w`), a date (`2026-10-09`, optionally `T14:30`), or a git ref (the date of its commit) |
| `--kind code\|doc\|config\|test\|other` | repeatable |
| `--lang L` | language, family or config format |
| `--depth N` | limit directory depth below the path argument (`1`: the directory's own files; with `--dirs`, one level of directories) |
| `--dirs` | one line per directory, the argument included: count of the files below it, newest mtime, total lines and size |
| `--long`, `-l` | add size, kind/language, symbol count, title, the commit date and the `modified`/`untracked` tags to each file |
| `--repo`, `--workspace`, `--budget`, `--offset`, `--out`, `--format` | as every query |

One line per file: name, mtime (local, minute precision) and lines. The
files of a directory are listed together under its path, the
directories in the order of their first file by `--sort`, so the answer
costs about what `ls -R` with dates does (efficiency addendum, section
3). `--long` adds the commit date when it differs by more than a minute,
size, kind/language, symbol count, and a title: a document's first
heading or front-matter title, a code file's module or file doc first
sentence when it has one.
For a document the symbol count is its number of sections. Files
discovery excludes are not listed; `ls` says how many were hidden under
each directory argument and why (`ignored by git`, `built-in exclusion`,
`workspace exclude`, and `not indexable (binary, over 2 MB or language
not selected)`) in one summary line, never per file. A directory git
ignores as a whole counts once: its files are not walked. The line is
computed in git repositories only.

Without arguments in a workspace directory `ls` lists that directory
recursively, which with the default sort is "what changed most
recently"; outside every repo it lists the repos in scope. An argument
is a path or a glob (`'src/**/*.ts'`, `repo:**/*.md`); a path without an
indexed file is not found (exit 1), or says in one line why the path
is outside the index (efficiency addendum, section 4.1).

`outline <dir>` entries gain the mtime column, last on the line, so
directory outlines read the same way; `--sort` and `--reverse` are
accepted there too, with `name` as the default.

MCP: a `codedoc_ls` tool with the same inputs; `codedoc_outline` takes
`sort` and `reverse`.

## 4. Paging through files to find a symbol

Reading a known line range with `sed -n A,Bp` is fine and stays outside
codedoc. The habit to remove is paging: `sed -n 1,105p agents.ts`, then
`sed -n 150,200p agents.ts`, reading chunk after chunk to locate a
symbol or to learn what a file holds. No new command is needed; the
existing ones must be the obvious move:

- The lookup skill is a table of need to command (efficiency addendum,
  section 9): reading a function is `symbol`, what a file contains is
  `outline`, the files by name and date are `ls`, and a range whose line
  numbers are already known is `lines` (or `sed`).
- Every entry that names a symbol or section already carries
  `path:start-end` (a document outline: `start-end` under the path in
  its title); `outline`, `find` and `ls` keep doing so, so a raw range
  read never needs a search first.
- `symbol` accepts `path:LINE`: the innermost symbol or section
  enclosing that line. An agent holding a line number from a stack
  trace, a test failure or a grep hit gets the whole enclosing function
  in one call instead of guessing a range around it. A line no symbol
  encloses is answered with the symbols of the file and the note `no
  symbol encloses line N of F (M lines)` (efficiency addendum, section
  4.1); with `--strict` it is not found, with a note pointing at
  `outline`.

### 4.1 `codedoc lines`

`codedoc lines <path> <A>-<B>...` (or `<A>+<N>`: A and the N lines after
it, as sed's `A,+N`; or `<A>` alone; several ranges are printed in line
order, joined where they overlap or touch) prints that range of the file
on disk with line numbers under one header line, `path:A-B  in <symbols>`,
and nothing else, under the budget: a range larger than the budget
is printed up to the lines that fit and ends with the `codedoc lines`
call for the rest, so following those calls reads the whole range, each
line once (never `[body omitted]` unless not one line fits). It exists
as the cross-check for the index, not as a replacement for `sed`:

- the header names the symbols or sections the index places on those
  lines, innermost only and at most six (`in EdgeSession.register
  (120-168)`); there is no note unless the index and the file disagree;
- the freshness check runs as for any query; when the content hash of
  the file on disk still differs from the indexed one (the check was over
  its file limit), the note says so and codedoc re-indexes the file
  before answering; if the index still disagrees with the disk after that
  (the lines a symbol's signature is recorded on hold neither its name
  nor its signature, or its range ends past the file), the note carries
  `index mismatch: <key> recorded 120-168; report it with codedoc
  feedback`;
- a file on disk that is not indexed is printed too, with a note that
  says why it is not (efficiency addendum, section 4.1); a
  file of a dependency is printed under the symbols `deps.db` holds for
  it (deps addendum, section 7);
- commands an answer suggests are written to work when pasted from any
  directory: files as `repo:path`, and arguments a shell would interpret
  in single quotes.

### 4.2 `codedoc feedback`

An agent that sees codedoc disagree with the file (a body that is not
the function, a missing symbol, a wrong caller, a stale section) reports
it instead of silently falling back:

`codedoc feedback "<what was asked, what came back, what the file says>"
[--key K] [--path P] [--cmd "codedoc symbol ..."]`

appends one JSON line to `$XDG_STATE_HOME/codedoc/feedback.jsonl` and
prints nothing (efficiency addendum, section 6: the exit status is the
result; `--verbose` prints `feedback recorded in <file>`). The line holds:
time, workspace, repo, head, dirty flag, codedoc build and extractor
version, the text and the optional fields, plus the file's indexed hash
and current hash when `--path` or `--key` is given. Nothing leaves the
machine. `codedoc feedback --list [--since D]` prints the entries, oldest
first, as one section `== feedback (N)` without a header line (the list
describes no index): one line each, the text on one line and cut to its
first 400 bytes (tagged `[text cut: N bytes, whole with --format json
--out FILE]`); an entry whose two hashes differ is tagged `[index behind
the file]`. The list takes `--budget`, `--offset` and `--out` and is cut
and continued like a query answer (section 9 of the design); with
`--format json` it is the usual response object without `header`, each
entry carrying the stored report whole under `extra`. `--clear` empties
the file whatever it holds, silently; with `--verbose` it prints how
many reports could be read.

The file is bounded and read defensively. A report's text is cut to
8 KB and every other field to 1 KB, at a character boundary, ending
` [truncated]`. A report that would take the file past 4 MB first
replaces it by its newest whole lines, about 2 MB of them (written
beside the file and renamed over it; a report another process appends
in that instant is lost). A report always starts on a line of its own.
Lines that are no report (not JSON, cut short, or longer than 1 MB,
which are skipped without being held in memory) cost only themselves:
the reports around them are listed.

Filing a report never re-indexes: it describes the index that
gave the answer. Outside every workspace the text is recorded with the
build alone. The lookup skill tells agents to check a suspicious answer
with `codedoc lines` or `sed`, and to file feedback when they differ.
MCP: `codedoc_feedback`, which answers `ok` for a report filed and takes
the common inputs like every tool
(`budget`, `offset` and `out` page its `list`, so a long list cannot
flood the agent's context); and `codedoc_lines` (`path`, `range`) for the
cross-check.

## 5. Testing

Fixtures (`testdata/ws/doc-repo`, a repo of its own in the fixture
workspace): a spec-like markdown file with numbered nested headings, a
duplicate heading, fenced code containing `#` lines, setext headings and
front matter; one `.rst`, one `.adoc`, one `.txt`. `ls` tests set mtimes
with `os.Chtimes` and build a small git history with fixed commit dates.
Every fixture copy carries the date of its one commit (2026-01-01) as
the mtime of its files, and tests run with UTC as the local zone, so
answers that print times are reproducible. An integration test
(`make docker-integration`) asks a built `codedoc mcp` over stdio for
the outline of a Markdown file, a `file.md#section` address, `codedoc_ls`
and `codedoc_lines`.

## 6. Decisions made during implementation

- **READMEs are documents.** v1 indexed `.md` files as rows without a
  language, which `languages:` never dropped. They are documents now:
  a repo that sets `languages:` without `doc` no longer lists them.
  Module docs read from `README.md` and the readme headings of
  `overview` are unaffected: both read the file, and both find headings
  with the document parser (`doc.MarkdownSections`), which replaced the
  ATX-only splitter of v1.
- **Symbol counts.** `overview`, `status` and `RepoStats` count code
  symbols; `ls` shows sections per document.
- **Embeddings.** `vectors:N/M` counts every section, with or without
  text of its own.
- **Markdown edge cases.** A text line directly above `---` or `===` is
  a setext heading unless it is a list item, a quote, a table row or an
  HTML line; lines indented four spaces are code. A line that starts
  inside an HTML comment is no heading: the comment runs from `<!--` to
  the first `-->` (to the end of the file when it never closes), and a
  `<!--` inside a fenced block, an indented code line or an inline code
  span opens none. reStructuredText needs
  an underline at least as long as the title; an indented title needs an
  overline. AsciiDoc skips `----`, `....`, `++++`, `////` and fenced
  blocks, and accepts Markdown-style `#` titles.
- **Section spans** end at the last line with text before the next
  heading of the same or a higher level: trailing blank lines are left
  out.
- **`codedoc_lines` MCP tool.** The addendum named MCP tools for `ls`
  and `feedback` only; `codedoc_lines` (`path`, `range`, and the common
  inputs) was added afterwards, so the cross-check the lookup skill asks
  for exists on both surfaces. The server has fifteen tools (nineteen
  since the efficiency addendum, section 9.1).
- **Store.** Migration 13 (12 is the dependency index's
  `edge.dst_dep_key`): `file.title`, the `section_fts` text index
  (with a trigger that drops a deleted symbol's text) and `doc_link`.
  A full reference resolution keeps link edges (`ref_kind = 'link'`),
  which no reference derives.
