"""Shell read -> codedoc command mappings used by replay.py.

A profile answers one question: "which codedoc command(s) would have served
this shell read?". It has one method per kind of read, each returning a list
of argv lists (the words after `codedoc`), so a mapping is one short method
and changing the CLI under test means editing only this file.

`v1` is the mapping for the 448b108 build, the same one the usage study calls
"new binary, expected". `v2` is the mapping for the efficiency pass: it starts
as v1 and overrides only what the new CLI does in fewer calls. To follow a CLI
change, edit `ProfileV2` (or add a subclass and register it in `PROFILES`).

What replay.py decides before calling a profile, and a profile cannot change:
which reads are servable at all (the target is indexed, the grep names
indexed symbols, the range resolves), and what the shell returned for them.
"""


def union_ranges(ranges):
    """Sort line ranges and merge the ones that overlap or touch: [(1,5),(6,9),(20,30)] -> [(1,9),(20,30)]."""
    out = []
    for a, b in sorted(ranges):
        if out and a <= out[-1][1] + 1:
            out[-1] = (out[-1][0], max(out[-1][1], b))
        else:
            out.append((a, b))
    return out


class ProfileV1:
    """Commands of the 448b108 build ("previous build").

    `symbol_key(name)` is supplied by replay.py and returns the stable key of
    an indexed workspace symbol; this build needs it for `symbol --body`.
    """

    name = 'v1'
    # False: every paged read is planned alone. True: the paged reads of one
    # file inside one tool call reach `paged` together.
    merge_paged = False

    def __init__(self, symbol_key):
        self.symbol_key = symbol_key

    def lookup(self, argv):
        """A recorded codedoc lookup, rerun: the recorded argv unchanged."""
        return [list(argv)]

    def paged(self, path, ranges):
        """`sed -n A,Bp path`, `head`, `tail`, Read with offset: one `lines` per range."""
        return [['lines', path, '%d-%d' % (a, b)] for a, b in ranges]

    def whole_strict(self, path, nlines):
        """`cat path` answered with the same information: every line of the file."""
        return [['lines', path, '1-%d' % max(1, nlines)]]

    def whole_outline(self, path):
        """`cat path` answered with the file's outline (less information, the cheap variant)."""
        return [['outline', path]]

    def search_names(self, names, scope_file, usage):
        """A grep whose pattern names indexed workspace symbols.

        names: the indexed symbol names in the pattern (at most 5).
        scope_file: repo-relative file when the grep targeted one file, else None.
        usage: 'body' (grep with -A/-B/-C context on a file or a declaration:
        the reader wanted the code), 'decl' (where is it declared) or
        'usage' (recursive grep: who uses it).
        This build takes one name per command.
        """
        out = []
        for n in names:
            if usage == 'body':
                out.append(['symbol', self.symbol_key(n) or n, '--body'])
            elif usage == 'decl':
                out.append(['find', n])
            else:
                out += [['callers', n], ['refs', n]]
        return out

    def heading_search(self, path):
        """A grep for `#` headings in an indexed markdown file."""
        return [['outline', path]]

    def listing(self, paths):
        """`ls` / `find` / `tree` / Glob over indexed paths."""
        return [['ls', p] for p in paths]

    def dep_search(self, names):
        """A grep in node_modules or downloaded SDK sources naming dependency symbols."""
        return [['find', n, '--deps'] for n in names]


class ProfileV2(ProfileV1):
    """Commands of the efficiency pass.

    Differences from v1, and nothing else:
    - `paged`: all paged reads of one file inside one tool call become ONE
      `lines <path> A-B C-D ...`, overlapping and adjacent ranges unioned.
      The shell bytes compared against stay the sum of the original reads.
    - `search_names`: a grep naming several indexed symbols becomes ONE
      `symbol A B C` call when the reader wanted the code ('body') or the
      declarations ('decl', with `--no-body`); a recursive usage grep is one
      `impact <name>` per name in place of `callers` plus `refs`.
    - `dep_search`: one `symbol --no-body A B C`; dependency names need no
      `--deps` any more.
    Every other kind is inherited: override `whole_strict`, `whole_outline`,
    `heading_search`, `listing` or `lookup` the same way when the CLI changes.
    """

    name = 'v2'
    merge_paged = True

    def paged(self, path, ranges):
        """One `lines` call carrying every range read from the file in this tool call."""
        return [['lines', path] + ['%d-%d' % r for r in union_ranges(ranges)]]

    def search_names(self, names, scope_file, usage):
        """One command for all the names of a grep, or one `impact` per name for a usage grep."""
        if usage == 'body':
            return [['symbol'] + list(names)]
        if usage == 'decl':
            return [['symbol', '--no-body'] + list(names)]
        return [['impact', n] for n in names]

    def dep_search(self, names):
        """Declarations of dependency symbols, all names in one call."""
        return [['symbol', '--no-body'] + list(names)]


PROFILES = {'v1': ProfileV1, 'v2': ProfileV2}
