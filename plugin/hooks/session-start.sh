#!/usr/bin/env bash
# SessionStart hook for codedoc: prints the first 40 lines of the workspace
# overview so the session starts oriented. Prints nothing and exits 0 when
# codedoc is not installed or the current directory is in no workspace.
# Opt-in: register it in settings.json (see docs/agents.md, "Session-start hook").
set -u

command -v codedoc >/dev/null 2>&1 || exit 0

out=$(codedoc overview --budget 3000 2>/dev/null) || exit 0
[ -n "$out" ] || exit 0

printf 'codedoc workspace overview (use the codedoc-lookup skill for queries):\n'
printf '%s\n' "$out" | head -n 40
exit 0
