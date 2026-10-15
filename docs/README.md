# codedoc documentation

The [README](../README.md) covers what codedoc is, how to install it
and a first run. The pages below are the reference.

## Reference

| Page | Covers |
|---|---|
| [CLI reference](cli.md) | output conventions, no dead ends, addresses, every command and its flags, languages and config formats |
| [Configuration](configuration.md) | the config file and every key, environment variables, the files codedoc reads and writes |
| [Search and embeddings](search.md) | how `search` and `where` rank, what is embedded, enabling the local model |
| [Dependencies](dependencies.md) | the index of third-party packages, `api`, `deps`, package states |
| [Agents](agents.md) | the MCP server and its tools, the Claude Code plugin: skills, subagents, session-start hook |
| [Daemon and background work](daemon.md) | the daemon, documentation upkeep, SCIP precise edges, autodoc |
| [Development](development.md) | tests, the Docker images, measuring usage over Claude Code transcripts |
| [Architecture](architecture.md) | the packages and how they fit together |

The reference pages and the code are the current truth.

## Other documents

- [`history/`](history/): the dated design records (`specs/`) and
  implementation plans (`plans/`) written during development. They are
  kept for the rationale behind the design and are not maintained: where
  they disagree with the reference pages or the code, the reference
  pages and the code are right.
- [`TODO.md`](TODO.md): known issues and potential improvements.
