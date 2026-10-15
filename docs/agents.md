# Agents: MCP server and Claude Code plugin

codedoc serves coding agents two ways: an MCP server any MCP client can
start, and a Claude Code plugin that registers that server and adds
skills, subagents and a session-start hook.

## MCP server

`codedoc mcp` serves nineteen tools over stdio, with logs on stderr. The
working directory selects the workspace, as it does for the CLI. Tools
return the same compact text as the CLI, except that a line naming the
command for what the budget left out names the tool call instead:
`8 more: codedoc_find {"pattern":"*Store*","budget":300,"workspace":"fx","offset":3}`,
`8 more lines: codedoc_lines {"path":"go-lib:service/service.go","range":"16-23","workspace":"fx"}`.

| Tool | Covers | Inputs (`*` required) |
|---|---|---|
| `codedoc_overview` | overview | — |
| `codedoc_outline` | outline | `target*`, `detail` |
| `codedoc_ls` | ls | `paths`, `since`, `sort`, `long` |
| `codedoc_lines` | lines | `path*`, `range*` |
| `codedoc_find` | find | `pattern*`, `kind`, `deps`, `detail` |
| `codedoc_symbol` | symbol | `symbol`, `names` (several addresses), `no_body`, `deps`, `detail` |
| `codedoc_impact` | impact | `symbol*`, `only`, `context`, `detail` |
| `codedoc_how` | how | `target*` |
| `codedoc_api` | api | `package*`, `name`, `detail` |
| `codedoc_where` | where | `query*`, `limit`, `context`, `deps` |
| `codedoc_search` | search | `query*`, `kind`, `deps`, `context`, `detail` |
| `codedoc_graph` | callers, callees, refs, impls, supertypes, tests, siblings, deps, dependents | `relation*`, `target*`, `depth`, `no_tests`, `context`, `detail` |
| `codedoc_config` | config | `key*` |
| `codedoc_explain` | explain | `target*` |
| `codedoc_flow` | flow | `symbol*`, `depth` |
| `codedoc_history` | changes, history, at | `mode*`, `since`, `symbol`, `ref`, `diff` |
| `codedoc_doc` | gaps, doc set, doc check | `action*`, `target`, `text`, `docs`, `kind` |
| `codedoc_deps` | deps (list), deps index, deps prune | `action` (`list`, `index` or `prune`) |
| `codedoc_feedback` | feedback | `text`, `path`, `list` |

Every tool also takes `workspace`, `repo`, `budget` and `offset`:

- **`budget`** defaults to 6000 bytes (`codedoc_impact`: 3000) and is
  capped at 60000.
- **Descriptions** are one sentence each. Inputs that are rarely used
  stay on the CLI (`--out`, `--format`, `--sort` of an outline,
  `--lang`).
- **Writes** answer `ok`: `codedoc_doc` with action `set`,
  `codedoc_feedback` filing a report, `codedoc_deps` with action
  `index` or `prune`. `codedoc_doc` action `set` takes `target` and
  `text`, or `docs`, a list of `{address, text}` written as one
  `doc set --batch`; a batch in which records failed is a tool error
  whose text is the failure lines.
- **Misses.** The nearest candidates, the best of several candidates
  and a `not found` line come back as normal text results.

While it runs, the server holds the session lock of every repo in its
workspace, so scheduled autodoc runs skip them. It takes none when
started under `CODEDOC_HEADLESS`.

## Claude Code plugin

`plugin/` is a Claude Code plugin, and the repository root is a local
marketplace named `codedoc`:

```sh
claude plugin marketplace add /path/to/codedoc
claude plugin install codedoc@codedoc
```

The plugin registers the MCP server (`codedoc mcp`, so `codedoc` must be
on `PATH`), three skills and two subagents.

| Skill | Loaded when | Says |
|---|---|---|
| `codedoc-lookup` | the session is about to read or search the repository's code with grep, sed, cat or Read | a table of need to command (`symbol`, `outline`, `impact`, `where`, `how`, `api`, `ls`, `lines`), when to delegate to the explorer, and the cross-check (`lines`, `feedback`) |
| `codedoc-document` | the session writes or changes code, or is asked to document | always write the doc comment in the edit that writes the code: what only this session knows. What a good doc contains per level. `doc set` and the documenter are for code that is not being edited |
| `codedoc-maintain` | asked about coverage, upkeep, autodoc, dependencies, the daemon, SCIP or the config | `gaps`, `doc check`, upkeep, autodoc, workspace config, dependencies, SCIP, the daemon |

The first two are what a working session carries. Together they are
3,457 characters, about 860 tokens. They say nothing about addresses,
keys, budgets or freshness: answers explain themselves.

| Subagent | Model, tools | Used for |
|---|---|---|
| `codedoc-explorer` | `haiku`; Bash and Read | a question that takes more than two lookups, spans several files or packages, is about how a dependency is used, or asks where, how or why. It runs the lookups in its own context and returns the answer in about 300 words, every claim with its `path:line`, a `checked:` line and a `not found:` line. It never guesses |
| `codedoc-documenter` | `haiku`; Bash and Read | documentation the user asks for now: it is given addresses, reads the symbols, writes every doc with one `codedoc doc set --batch` and returns a count and the failures |

Only the answer of a subagent enters the session's context, not the
files it read. One symbol, one file outline or a known line range is not
delegated: the session runs those itself. The `tools` field of a
subagent takes tool names only, so the files tell the agent to run only
`codedoc` commands with Bash; a permission rule in your settings can
enforce it.

**Session-start hook.** The plugin also ships
`plugin/hooks/session-start.sh`, which prints the first 40 lines of
`codedoc overview --budget 3000` for the current directory's workspace.
It prints nothing when codedoc is missing or the directory is in no
workspace. It is off by default. To turn it on, add it to
`~/.claude/settings.json` (or a project's `.claude/settings.json`):

```json
{
  "hooks": {
    "SessionStart": [
      { "hooks": [ { "type": "command", "command": "/path/to/codedoc/plugin/hooks/session-start.sh" } ] }
    ]
  }
}
```
