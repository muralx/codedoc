package autodoc

import (
	"fmt"
	"strconv"
	"strings"
)

// promptRules follows the key list. It is self-sufficient: the repo may
// not have the codedoc plugin or its skills installed, so it says what a
// good doc contains and gives the exact commands to read and write.
const promptRules = `
How to work:

1. For each key, gather context, only through these codedoc commands (and
   the Read tool):
     codedoc explain <key>             # repo, module chain, container, callers, callees
     codedoc symbol <key> --body       # signature, current doc and body
     codedoc callers <key> --no-tests  # what callers rely on
     codedoc tests <key>               # tests state the contract
     codedoc siblings <key>            # documented neighbours: match their style
   For a module key (repo:dir/): codedoc explain <key> and codedoc outline <key>.
   Do not explore the repository any other way.

2. Write the docs of all the keys in ONE call, only with
   codedoc doc set --batch and the records on stdin: a line "=== <key>",
   then the doc text without comment markers (codedoc adds them for the
   language and places the doc), up to the next "=== " line:
     codedoc doc set --batch <<'DOCS'
     === <key>
     First sentence: what it does or returns, standing alone.

     Then what a caller cannot see from the signature.
     === <next key>
     First sentence of the next doc.
     DOCS
   doc set prints nothing when the docs are written: no output and exit
   status 0 are the success, so do not check it with another command. A
   doc it could not write is one line, "<key>: <what>; <what to do>", and
   the other docs were written all the same. If the line reports a
   verification failure the file was restored: fix that text and set that
   key again (a --batch with the failed keys only). No line of a doc text
   may start with "=== ".
   Never edit source files directly and never change code.

What a good doc says:
- Lead with one sentence that stands alone; it is what listings show.
- Function or method: the contract (what it returns or guarantees), the
  meaning and constraints of inputs, errors and when they occur, side
  effects (I/O, mutation, locks), concurrency. Skip restating the name or
  types and line-by-line narration.
- Type, class or interface: its role, the invariants it keeps, its
  lifecycle and ownership, thread safety. Do not list every member.
- Module or package: its purpose, main types and entry points, how its
  dependents use it, what it must not do.
- Describe behavior, not history. Be specific: name the error, the unit,
  the default, the limit. Go docs start with the symbol name.
- Do not invent behavior: leave out what the body, callers and tests do
  not settle. Three true sentences beat ten that drift.

A stale key already has a doc that no longer matches its code: rewrite it
from what the code does now. A missing key has none yet.

If a key cannot be resolved (codedoc symbol or explain reports it not found
or ambiguous), or doc set refuses it, skip it: do not guess another symbol
and do not work around the refusal. Say in your final message which keys
you skipped and why.

Document exactly the keys listed above, each once. When they are all
written, stop: do not document other symbols, run other commands, or
commit anything.
`

// Prompt returns the instructions given to `claude -p` for documenting
// gaps, some of one module's gaps in rank order: each line names the
// stable key, its doc state and where it is.
func Prompt(module string, gaps []Gap) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Document the following %d symbols of module %s in this repository.\n", len(gaps), module)
	b.WriteString("Their codedoc stable keys, one per line, with the doc state and file:line:\n\n")
	for _, g := range gaps {
		loc := g.Path
		if g.Line > 0 {
			loc += ":" + strconv.Itoa(g.Line)
		}
		fmt.Fprintf(&b, "- %s (%s, %s)\n", g.Key, orUnknown(g.State), loc)
	}
	b.WriteString(promptRules)
	return b.String()
}
