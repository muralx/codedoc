---
name: codedoc-lookup
description: Use when about to read or search this repository's code with grep, rg, sed, cat, head, find, ls or Read - finding where something is, reading a function, checking who calls it, what a change breaks, how a feature works, how a dependency's API looks, which files changed.
---

# codedoc lookup

The repository is indexed. One `codedoc` call answers what several grep
and sed calls would, with the lines and their locations.

| Need | Run |
|---|---|
| Read a function, a type or a doc section | `codedoc symbol Store.Get` (several: `codedoc symbol A B C`; a section: `"docs/spec.md#6.2"`) |
| The function around a line (stack trace, grep hit) | `codedoc symbol path/file.go:160` |
| What is in a file or directory | `codedoc outline path/file.go` |
| Before changing a symbol: callers, tests, mentions | `codedoc impact Store.Get` |
| Where is this done | `codedoc where "retry a failed delivery"` |
| How does this symbol or file work | `codedoc how Service.Lookup` |
| A dependency's API, in place of node_modules or web docs | `codedoc api @scope/pkg`, `codedoc api @scope/pkg Server` |
| Which files exist, what changed lately | `codedoc ls`, `codedoc ls src --since 2d` |
| Line ranges you already know | `codedoc lines path/file.go 120-160 300-320` |

Every answer says how to get more of itself; run the line it ends with.

## Delegate

Hand the question to the `codedoc-explorer` agent, in plain words, when:

- it will take more than two lookups;
- it spans several files or packages;
- it is about how a dependency is used;
- it asks where, how or why.

It returns the answer with locations, not the files. Do not delegate one
symbol, one file outline or a known line range: run those yourself.

## When an answer looks wrong

Compare with the file, `codedoc lines path/file.go 150-170`, report it,
`codedoc feedback "asked X, got Y, the file says Z" --path path/file.go`,
and go on with what the file says.
