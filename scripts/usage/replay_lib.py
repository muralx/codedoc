"""Measuring machinery of replay.py: running a binary, index facts, planning.

Nothing here prints. `Side` runs one codedoc binary in an isolated
environment and measures bytes and exit status. `Facts` holds what the index
knows (is this name a symbol, is this path indexed), probed once with a
reference binary and saved with a baseline so that a later run decides
servability exactly the same way. `plan_reads` turns the shell reads of the
transcripts into jobs (the codedoc commands a profile maps them to) and
`recorded_lookups` lists the codedoc lookups to rerun.

`usage.configure()` must have been called before anything here is used.
"""
import collections
import json
import os
import re
import shlex
import subprocess

import usage

# A file with an extension, then :line; or an entry of a file group, which
# carries only "  :line" under the line naming its file.
LOC_RX = re.compile(r'[\w./@-]*\w\.\w+:\d+|^\s+\S.*  :\d+', re.M)
# The output budget cut the answer: the line that says so, and the command it offers to go on.
CUT_RX = re.compile(r'^\.\.\. \d+ more |\[body omitted|^\d+ more(?: lines)?: ', re.M)
# Both wordings of a cut body: the 448b108 build ("continue with codedoc
# lines ... or use --out FILE") and the efficiency pass ("N more lines:
# codedoc lines ...").
CONTINUE_RX = re.compile(r'(?:continue with `?|^\d+ more lines: )codedoc ([^`\n]+?)`?(?: or use --out FILE)?\.?\s*$', re.M)
MAX_HOPS = 60
ERR_RX = re.compile(r'^codedoc: ', re.M)
DROP_FLAGS = ('--out', '--format', '--budget')  # replays use text output and the default budget
MCP_POSITIONAL = ('target', 'path', 'range', 'pattern', 'symbol', 'query', 'key', 'paths')
READ_KINDS = ('whole', 'paged', 'search', 'listing')
DEAD_KINDS = ('not found', 'empty result', 'ambiguous', 'error')


def nbytes(text):
    """Size in bytes of a decoded string."""
    return len(text.encode('utf-8', 'replace'))


class Side:
    """One binary under measurement, with its own isolated index."""

    def __init__(self, label, binary):
        self.label, self.binary = label, os.path.abspath(binary)
        self.cache = {}
        self.env = None

    def run(self, argv, timeout=60):
        """Run `codedoc argv` in the repo; return {argv, bytes, exit, flag, out}. Never raises."""
        key = tuple(argv)
        if key in self.cache:
            return self.cache[key]
        if self.env is None:
            self.env = usage.cd_env(self.label)
        raw, rc, flag = b'', 0, ''
        try:
            p = subprocess.run([self.binary] + list(argv), cwd=usage.REPO, env=self.env, capture_output=True,
                               timeout=timeout, stdin=subprocess.DEVNULL)
            raw, rc = p.stdout + p.stderr, p.returncode
        except subprocess.TimeoutExpired as e:
            raw, rc, flag = (e.stdout or b'') + (e.stderr or b''), -1, 'timeout %ds' % timeout
        except OSError as e:
            rc, flag = 127, 'cannot run: %s' % e.strerror
        out = raw.decode('utf-8', 'replace')
        if not flag and rc != 0:
            flag = 'exit %d' % rc
        elif not flag and ERR_RX.search(out):
            flag = 'error line'
        self.cache[key] = {'argv': list(argv), 'bytes': len(raw), 'exit': rc, 'flag': flag, 'out': out,
                           'cut': bool(CUT_RX.search(out)), 'cont': False}
        return self.cache[key]

    def run_all(self, argv):
        """Run a command and every `continue with codedoc ...` its answers print, until the answer is whole.

        A shell read returned all its lines, so the codedoc equivalent is the
        full chain; each continuation is a command of its own (cont=True).
        """
        runs, seen = [self.run(argv)], {tuple(argv)}
        while len(runs) < MAX_HOPS:
            m = CONTINUE_RX.search(runs[-1]['out']) if runs[-1]['exit'] == 0 else None
            try:
                nxt = shlex.split(m.group(1)) if m else None
            except ValueError:
                nxt = None
            if not nxt or tuple(nxt) in seen:
                break
            seen.add(tuple(nxt))
            runs.append(dict(self.run(nxt), cont=True))
        return runs

    def index(self):
        """Build the isolated index once (up to 10 minutes)."""
        return self.run(['index'], timeout=600)

    def tools_list(self):
        """Size of the MCP `tools/list` answer: {tools, bytes} or None when the server did not answer."""
        if not usage.have(self.label):
            return None
        tl = usage.mcp_tools(self.label)
        return {'tools': len(tl), 'bytes': nbytes(json.dumps(tl))} if tl else None


class Facts:
    """What the index knows, independent of output format and of the profile.

    Probes run on `side` (the reference binary) with JSON output and are
    cached by command; a baseline file carries the cache so both runs agree.
    """

    def __init__(self, side, saved=None):
        self.side = side
        self.saved = dict(saved or {})

    def _entries(self, argv):
        """Entry keys of a JSON answer, cached under the joined argv."""
        k = ' '.join(argv)
        if k not in self.saved:
            keys = []
            if self.side is not None:
                r = self.side.run(list(argv) + ['--format', 'json', '--budget', '0'])
                try:
                    doc = json.loads(r['out']) if r['exit'] == 0 else {}
                    for s in doc.get('sections') or []:
                        keys += [e.get('key') or e.get('loc') or '' for e in s.get('entries') or []]
                except (ValueError, AttributeError):
                    keys = []
            self.saved[k] = keys
        return self.saved[k]

    def symbol_keys(self, name, deps=False):
        """Stable keys of the indexed symbols called exactly `name` (dependencies included with deps)."""
        keys = self._entries(['find', name] + (['--deps'] if deps else []))
        return [k for k in keys if re.split(r'[/.#]', k)[-1] == name]

    def symbol_key(self, name):
        """First stable key of a workspace symbol, or None."""
        keys = self.symbol_keys(name)
        return keys[0] if keys else None

    def has(self, argv):
        """True when `outline <path>` / `ls <path>` lists at least one entry."""
        return bool(self._entries(argv))


def strip_flags(args):
    """Drop --out/--format/--budget (with their value) and heredoc markers from a recorded argv."""
    out, skip = [], False
    for w in args:
        if skip:
            skip = False
        elif w in DROP_FLAGS:
            skip = True
        elif w.split('=')[0] in DROP_FLAGS or w.startswith(('HEREDOC', '<<')):
            continue
        else:
            out.append(w)
    return out


def mcp_argv(call):
    """CLI argv equivalent to a recorded codedoc MCP tool call."""
    inp = dict(call.input or {})
    sub = (call.name or '').split('codedoc_')[-1]
    if sub == 'graph':
        sub = str(inp.pop('relation', 'graph'))
    pos, flags = [], []
    for k in MCP_POSITIONAL:
        v = inp.pop(k, None)
        if v:
            pos += [str(x) for x in v] if isinstance(v, list) else [str(v)]
    for k, v in inp.items():
        f = '--' + k.replace('_', '-')
        if v is True:
            flags.append(f)
        elif v not in (False, None, ''):
            flags += [f, ','.join(map(str, v)) if isinstance(v, list) else str(v)]
    return [sub] + pos + strip_flags(flags)


def lookup_argv(u):
    """argv to rerun a recorded lookup without the agent's filters, or None when it cannot be rebuilt."""
    if u.via == 'mcp':
        return mcp_argv(u.call)
    args = None
    if not u.wrapped and '$' not in u.raw.split(' | ')[0]:
        ws = usage.clean_words(usage.words_of(u.raw.split(' | ')[0]))
        if ws and ws[0] == 'codedoc':
            args = ws[1:]
    if args is None:  # shell function or for-loop expansion: rebuild from the parsed pieces
        args = u.sub.split() + list(u.files)
        for k, v in u.flags.items():
            args += [k] if v is True else [k, str(v)]
    args = strip_flags(args)
    return None if any('@SUBST' in a or '$' in a for a in args) else args


def is_lookup(u):
    """True for a recorded codedoc lookup (CLI or MCP), not the read-back of an --out file."""
    if u.kind != 'codedoc' or u.via == 'outfile':
        return False
    if u.via == 'mcp' and u.sub == 'graph':
        return str((u.call.input or {}).get('relation')) in usage.LOOKUP
    return u.sub in usage.LOOKUP


def uid(u):
    """Stable id of a unit: tool call id plus its position in the call."""
    return '%s#%d' % (u.call.id, u.call.units.index(u))


def recorded_lookups(units, dead):
    """Part A work list: [{id, ts, cmd, argv, recorded, dead}] for every recorded lookup in scope."""
    why = {id(d['unit']): d['why'] for d in dead if d['why'] in DEAD_KINDS}
    out = []
    for u in units:
        if is_lookup(u):
            argv = lookup_argv(u)
            out.append({'id': uid(u), 'ts': u.call.ts or '', 'argv': argv, 'recorded': int(round(u.chars)),
                        'cmd': 'codedoc ' + ' '.join(argv) if argv else u.raw[:100], 'dead': why.get(id(u), '')})
    return out


def answers(run):
    """True when a rerun answers: exit 0, at least one path:line location, no `codedoc: ` line."""
    return run['exit'] == 0 and bool(LOC_RX.search(run['out'])) and not ERR_RX.search(run['out'])


def relpath(f):
    """Repo-relative form of an absolute path ('.' for the repo itself)."""
    return '.' if f == usage.REPO else usage.rel(f)


def shell_now(cmd):
    """Bytes a read-only shell command prints today, run in the repo."""
    return nbytes(usage.sh(cmd))


def line_count(path):
    """Number of lines of a file (0 when unreadable)."""
    lines = usage.file_lines(path)
    return 0 if not lines else len(lines) - (1 if lines[-1] == '' else 0)


def _plan_dependency(u, prof, facts):
    """node_modules and downloaded SDK sources: only declaration greps have an equivalent."""
    outside = u.tclass == 'outside-source'
    if u.kind == 'search' and (not outside or re.search(r'\.[cm]?[jt]sx?$', (u.files or [''])[0] or '')):
        names = [n for n in usage.pattern_idents(u.pattern)[0][:5] if facts.symbol_keys(n, deps=True)]
        if names:
            return {'cmds': prof.dep_search(names)}
    if outside:
        return 'third-party source downloaded outside the repo (not indexed)'
    return ('node_modules grep for text that is not a dependency symbol' if u.kind == 'search'
            else 'node_modules file read (no --deps equivalent for a file range)')


def _plan_listing(u, prof, facts):
    """`ls`/`find`/`tree`/Glob: `ls` of the first four targets that hold indexed files."""
    paths = [relpath(f) for f in u.files[:4] if os.path.exists(f) and facts.has(['ls', relpath(f)])]
    return {'cmds': prof.listing(paths)} if paths else 'listing of a path with no indexed files'


def _plan_paged(u, f0, rp):
    """A ranged read of one file: the range resolved against the file as it is today."""
    if len(u.files) != 1 or not os.path.isfile(f0):
        return 'paged read of several files'
    rg = usage.resolve_range(u, f0)
    if not rg:
        return 'pattern range not resolvable'
    return {'paged': (rp, rg), 'shell': shell_now('sed -n %d,%dp %s' % (rg[0], rg[1], usage.q(f0)))}


def _plan_markdown(u, prof, facts, f0, rp):
    """Documents: heading greps, whole reads and ranged reads of indexed markdown."""
    if os.path.isdir(f0):
        if facts.has(['ls', rp]):
            return {'cmds': prof.listing([rp])}
        return 'recursive grep over documents for free text'
    if not facts.has(['outline', rp]):
        return 'markdown file not indexed'
    if u.kind == 'search':
        if '#' in (u.pattern or ''):
            return {'cmds': prof.heading_search(rp)}
        return 'free-text grep inside a document (search covers names and docs, not prose bodies)'
    if u.kind == 'whole':
        text, n = usage.sh('cat ' + usage.q(f0)), line_count(f0)
        if len(text) > 30000:  # Bash output is truncated near 30k: the reader saw only the lines before the cut
            text = text[:30000]
            n = text.count('\n')
        return {'cmds': prof.whole_strict(rp, n), 'outline': prof.whole_outline(rp), 'shell': nbytes(text)}
    return _plan_paged(u, f0, rp)


def _plan_source(u, prof, facts, f0, rp):
    """Source and test files: whole reads, ranged reads and greps naming indexed symbols."""
    if u.kind == 'whole':
        files = [f for f in u.files if os.path.isfile(f) and facts.has(['outline', usage.rel(f)])]
        if not files:
            return 'file has no indexed symbols'
        strict = [c for f in files for c in prof.whole_strict(usage.rel(f), line_count(f))]
        outline = [c for f in files for c in prof.whole_outline(usage.rel(f))]
        return {'cmds': strict, 'outline': outline, 'shell': sum(shell_now('cat ' + usage.q(f)) for f in files)}
    if u.kind == 'paged':
        return _plan_paged(u, f0, rp)
    ids, _, decl = usage.pattern_idents(u.pattern)
    names = [i for i in ids if facts.symbol_keys(i)][:5]
    if not names:
        return 'free-text grep (string, regex or a name that is not an indexed symbol)'
    single = len(u.files) == 1 and os.path.isfile(f0)
    want = 'body' if (single or decl) and u.ctx >= 3 else 'decl' if (single or decl) else 'usage'
    return {'cmds': prof.search_names(names, rp if single else None, want)}


def plan_read(u, prof, facts):
    """Map one shell read to codedoc commands.

    Returns a reason string when the read is not servable, else a dict with
    `cmds` (argv lists) or `paged` ((path, (A, B)), mapped later so a profile
    can merge ranges), optional `outline` (the second whole-file variant) and
    optional `shell` (bytes of the same read rerun today; absent = keep the
    historical size).
    """
    if u.tclass == 'outside':
        return 'outside the repo (not indexed)'
    if u.tclass == 'unresolved':
        return 'path is a shell variable (not resolvable)'
    if u.tclass in ('outside-source', 'dependency'):
        return _plan_dependency(u, prof, facts)
    if u.tclass == 'repo-other':
        return 'config/JSON/other non-source file (no symbols)'
    f0 = u.files[0] if u.files else None
    if not f0 or not os.path.exists(f0):
        return 'file or directory no longer exists'
    if u.kind == 'listing':
        return _plan_listing(u, prof, facts)
    if u.tclass == 'markdown':
        return _plan_markdown(u, prof, facts, f0, usage.rel(f0))
    if u.tclass in ('source', 'test'):
        return _plan_source(u, prof, facts, f0, usage.rel(f0))
    return 'unclassified'


def plan_reads(units, prof, facts):
    """Part B work list for one profile: (jobs, unserved).

    A job is one or more shell reads answered together: {ids, kind, tclass,
    target, reads, shell_bytes, shell_cmds, cmds, outline}. `outline` is None
    except for whole-file reads. unserved: [{id, kind, tclass, why, bytes}].
    """
    jobs, unserved, groups = [], [], collections.OrderedDict()
    for u in units:
        if u.kind not in READ_KINDS:
            continue
        p = plan_read(u, prof, facts)
        if isinstance(p, dict) and not (p.get('cmds') or p.get('paged')):
            p = 'the profile maps this read to no command'
        if not isinstance(p, dict):
            unserved.append({'id': uid(u), 'kind': u.kind, 'tclass': u.tclass, 'why': p, 'bytes': int(round(u.chars))})
            continue
        shell = p.get('shell', int(round(u.chars)))
        if 'paged' in p:
            path, rg = p['paged']
            key = (u.call.id, path) if prof.merge_paged else uid(u)
            g = groups.get(key)
            if g is None:
                g = groups[key] = {'ids': [], 'kind': u.kind, 'tclass': u.tclass, 'target': path, 'reads': 0,
                                   'shell_bytes': 0, 'shell_cmds': 0, 'ranges': [], 'outline': None}
                jobs.append(g)
            g['ids'].append(uid(u)); g['ranges'].append(rg)
            g['reads'] += 1; g['shell_bytes'] += shell; g['shell_cmds'] += 1
            continue
        jobs.append({'ids': [uid(u)], 'kind': u.kind, 'tclass': u.tclass, 'reads': 1, 'shell_bytes': shell,
                     'shell_cmds': 1, 'cmds': p['cmds'], 'outline': p.get('outline'),
                     'target': relpath(u.files[0]) if u.kind != 'search' else 'grep ' + (u.pattern or '')[:60]})
    for g in groups.values():
        g['cmds'] = prof.paged(g['target'], g.pop('ranges'))
    return jobs, unserved


def run_side(side, prof, lookups, jobs, unserved):
    """Run every planned command on one binary; return the side's result (JSON-serialisable)."""
    def slim(r):
        return {k: r[k] for k in ('argv', 'bytes', 'exit', 'flag', 'cut', 'cont')}

    def chain(j, argvs):
        full = j['kind'] in ('paged', 'whole')  # the shell printed every line: follow continuations
        return [slim(r) for a in argvs for r in (side.run_all(a) if full else [side.run(a)])]
    res = {'label': side.label, 'binary': side.binary, 'profile': prof.name, 'index': slim(side.index()),
           'lookups': [], 'jobs': [], 'unserved': unserved, 'tools_list': side.tools_list()}
    for lk in lookups:
        e = {k: lk[k] for k in ('id', 'ts', 'cmd', 'recorded', 'dead')}
        if lk['argv'] is None:
            e.update(argv=None, bytes=0, exit=None, flag='not replayable (shell substitution)', answers=False)
        else:
            runs = [side.run(a) for a in prof.lookup(lk['argv'])]
            e.update(argv=[r['argv'] for r in runs], bytes=sum(r['bytes'] for r in runs),
                     exit=max((r['exit'] for r in runs), key=abs, default=0),
                     flag='; '.join(sorted({r['flag'] for r in runs if r['flag']}
                                           | {'cut by the output budget' for r in runs if r['cut']})),
                     answers=bool(runs) and all(answers(r) for r in runs))
        res['lookups'].append(e)
    for j in jobs:
        e = {k: j[k] for k in ('ids', 'kind', 'tclass', 'target', 'reads', 'shell_bytes', 'shell_cmds')}
        e['cmds'] = chain(j, j['cmds'])
        e['outline'] = [slim(side.run(a)) for a in j['outline']] if j['outline'] is not None else None
        res['jobs'].append(e)
    return res
