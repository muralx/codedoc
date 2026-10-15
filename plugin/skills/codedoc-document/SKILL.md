---
name: codedoc-document
description: Use when writing or changing code in this repository, and when asked to document existing code - what the doc comment of a function, type or module must say and how to write it.
---

# codedoc document

While writing or changing code, always write the doc comment. It holds
what only this session knows: the intent, the invariants, the side
effects, the error and concurrency behaviour, the reason for a choice.
That knowledge is lost when the session ends; no later run can recover
it from the body.

- Write the comment in the source, in the same edit that writes the
  code. No extra command.
- Summary sentence first; it stands alone. Do not restate the signature.
- Describe behaviour, not history. Name the error, the unit, the limit.

## What a good doc contains

- **Function**: what it returns or guarantees, the meaning and limits
  of its inputs, errors and when they occur, side effects, concurrency.
- **Type**: its role, the invariants it keeps, its lifecycle and owner,
  thread safety.
- **Module**: its purpose, main types and entry points, what it must
  not depend on.

## Code you are not editing

To add or replace a doc there, read the code first (see the
codedoc-lookup skill), then run
`codedoc doc set path/file.go#Name --text "..."`; it prints nothing
when the doc is written. For many, give the addresses to the
`codedoc-documenter` agent.

Never look for stale or missing docs yourself. Upkeep finds and fills
them after the commit.
