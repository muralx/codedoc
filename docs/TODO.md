# Known issues and potential improvements

## Known issues

- **Upkeep counts untracked files as a dirty tree.** A single untracked
  file or directory in the working tree stops every upkeep run, and
  `codedoc status` only says `upkeep:on (uncommitted changes)`.
- **Git worktrees are not indexed.** A linked worktree of a configured
  repository is outside every workspace: queries from it are refused,
  and queries with `--workspace` answer from the main checkout.

## Potential improvements

Ideas held back until real use shows the need. Nothing here is scheduled.

### Cache extraction and vectors by file content

The index holds one state per workspace: the working tree as it is now.
On a branch switch only the files whose content hash differs are
re-extracted, but their previous symbols, edges and vectors are replaced,
so switching back extracts and embeds them again. Embedding is the slow
part.

Keep what was extracted and embedded keyed by content hash (file hash for
symbols, text hash for vectors), with a size or age bound, so content
that comes back is restored instead of recomputed. Worth doing when a
large repo switches between long-lived branches that differ in thousands
of files; on a small repo the cost is negligible.

### Document classes for ranking and filtering

Classify documents at index time (code, code docs, reference docs,
working records such as plans, specs and changelogs) from path
conventions, content signals and config globs. Rank code and reference
docs first by default, demote and collapse working records into one line
that names the command for them, label each document hit with its class
and date, and offer one filter (`--in code|docs|history`). The agent
should not have to choose a category to get a good answer.

### A `why` command

Questions about reasons are answered by decisions, specs and commit
messages, not by code. A command that searches those first.

### Upkeep in a private worktree

An upkeep run leaves its doc edits uncommitted in the user's working tree
for the minutes it takes; a session that commits everything in that
window sweeps them into its own commit. Run in a private git worktree at
the branch's commit and land the docs commit only if the branch has not
moved and the tree is still clean. This one is a safety issue, not an
optimization.
