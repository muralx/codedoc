# Design history

These are dated records written while codedoc was built: the original
design spec, its three addenda, and the five implementation plans that
delivered the first version. They explain why things are the way they
are. They are not maintained, and parts of them are superseded by later
addenda or by the code. The current truth is the code and the reference
pages in [`docs/`](../README.md); [architecture.md](../architecture.md)
describes the design as it stands today.

## Specs

In order; each later document says which parts of the earlier ones it
replaces.

| Date | File | Covers |
|---|---|---|
| 2026-10-09 | [specs/2026-10-09-codedoc-design.md](specs/2026-10-09-codedoc-design.md) | The v1 design: concepts and stable keys, the indexing pipeline (discovery, extraction, resolution tiers, SCIP, doc staleness, embeddings, freshness), the SQLite data model, the query API, doc write-back, gaps and autodoc, the daemon, CLI, MCP server and plugin, configuration. Kept in sync with the code longer than the others. |
| 2026-10-10 | [specs/2026-10-10-codedoc-deps-design.md](specs/2026-10-10-codedoc-deps-design.md) | Addendum: indexing third-party packages (npm and Go) into a shared `deps.db`, package identity, discovery from manifests, edges from workspace code into packages, dependency queries, pruning. |
| 2026-10-10 | [specs/2026-10-10-codedoc-documents-design.md](specs/2026-10-10-codedoc-documents-design.md) | Addendum: documents (Markdown, reStructuredText, AsciiDoc, text) as symbols with sections, file metadata and `codedoc ls`, `codedoc lines`, `codedoc feedback`. |
| 2026-10-11 | [specs/2026-10-11-codedoc-efficiency-design.md](specs/2026-10-11-codedoc-efficiency-design.md) | Addendum: the efficiency pass driven by measured sessions: output shape and budgets, passage embeddings, no dead ends, composite questions (`where`, `impact`, `how`, `api`), silent writes, documentation upkeep in the daemon, the explorer and documenter subagents, smaller skills and MCP schemas. |

## Plans

The implementation plans of the v1 design, written on 2026-10-09 and
executed in order. They record the task breakdown at the time; file
names and details changed afterwards.

| Date | File | Covers |
|---|---|---|
| 2026-10-09 | [plans/2026-10-09-codedoc-plan-1-core-index.md](plans/2026-10-09-codedoc-plan-1-core-index.md) | Plan 1: the skeleton, config, store, Go and TypeScript extraction, stable keys, resolution, the output frame and the first structural queries. |
| 2026-10-09 | [plans/2026-10-09-codedoc-plan-2-languages-config.md](plans/2026-10-09-codedoc-plan-2-languages-config.md) | Plan 2: Python and Java extractors, config key extraction across formats, and `codedoc config`. |
| 2026-10-09 | [plans/2026-10-09-codedoc-plan-3-docs-history.md](plans/2026-10-09-codedoc-plan-3-docs-history.md) | Plan 3: the documentation model (staleness, module docs), doc write-back per language, gap ranking, `changes`, `history`, `at` and `overview`. |
| 2026-10-09 | [plans/2026-10-09-codedoc-plan-4-search-mcp-plugin.md](plans/2026-10-09-codedoc-plan-4-search-mcp-plugin.md) | Plan 4: the ONNX embedder, hybrid search, `explain` and `flow`, the MCP server, the Claude Code plugin and its skills. |
| 2026-10-09 | [plans/2026-10-09-codedoc-plan-5-daemon-scip-autodoc-docker.md](plans/2026-10-09-codedoc-plan-5-daemon-scip-autodoc-docker.md) | Plan 5: the daemon, SCIP import, the unattended autodoc runner, and the Docker test environment. |
