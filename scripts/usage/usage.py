#!/usr/bin/env python3
"""codedoc usage and token-cost measurement over Claude Code transcripts.

Run:
  python3 -I scripts/usage/usage.py --repo DIR [--project DIR] [--workspace NAME]
      [--old BIN --new BIN] [--env-dir DIR] [--versions FILE] [--plugin DIR]
      [--since ISO] [--until ISO] [--no-measure] [--window N]

  --repo DIR        the repository the sessions worked on (required).
  --project DIR     the transcript directory. Default: the directory Claude
                    Code keeps for --repo, ~/.claude/projects/<repo path with
                    every non-alphanumeric character replaced by '-'>.
  --workspace NAME  workspace name written to the generated codedoc config
                    (default: basename of --repo).
  --old/--new BIN   the two codedoc binaries compared in section 3; the section
                    is skipped when either is missing or with --no-measure.
  --env-dir DIR     where the isolated codedoc environments (index, config,
                    state) are created, one subdirectory per binary. Default: a
                    fresh temporary directory. The user's real index is never
                    touched.
  --versions FILE   optional `old=...` / `new=...` lines naming the builds.
  --plugin DIR      optional plugin source tree whose skills/*/SKILL.md sizes
                    are listed beside the installed plugin's.
  --since/--until   ISO UTC bounds on the tool calls counted (since inclusive,
                    until exclusive), e.g. 2026-10-10T18:00:00Z.

Reads the JSONL transcripts (never writes to them), classifies every codedoc
call and every competing shell/file read, measures tool-result sizes, and
(unless --no-measure) runs read-only shell commands and the two isolated
codedoc binaries to measure counterfactuals. Standard library only. Output:
markdown tables on stdout.

Importing this module parses nothing: call `configure(argv)` first, then use
the loaders and classifiers (replay.py does exactly that).
"""
import sys, os, re, json, glob, shlex, subprocess, collections, statistics, math, argparse, tempfile

HOME = os.path.expanduser('~')
ARGS = None        # argparse namespace, set by configure()
REPO = None        # realpath of --repo
PROJECT = None     # transcript directory
WORKSPACE = None   # workspace name in the generated config
SINCE = None       # ISO lower bound (inclusive) or None
UNTIL = None       # ISO upper bound (exclusive) or None
W_EP = 6           # tool-call window for paging episodes
ENV_DIR = None     # parent of the isolated codedoc environments (created lazily)
BIN = {}           # label ('old', 'new') -> binary path
CPT = 4.0  # chars per token (estimate, mixed code/English)


def project_dir_for(repo):
    """Transcript directory Claude Code uses for a repo path."""
    return os.path.join(HOME, '.claude', 'projects', re.sub(r'[^A-Za-z0-9]', '-', os.path.realpath(repo)))


def configure(argv):
    """Parse argv and set the module globals every other function reads."""
    global ARGS, REPO, PROJECT, WORKSPACE, SINCE, UNTIL, W_EP, ENV_DIR, BIN
    ap = argparse.ArgumentParser(description=__doc__.split('\n')[0])
    ap.add_argument('--repo', required=True, help='repository the sessions worked on')
    ap.add_argument('--project', default=None, help='transcript directory (default: derived from --repo)')
    ap.add_argument('--workspace', default=None, help='workspace name in the generated config (default: repo basename)')
    ap.add_argument('--old', default=None, metavar='BIN', help='previous codedoc binary')
    ap.add_argument('--new', default=None, metavar='BIN', help='new codedoc binary')
    ap.add_argument('--env-dir', default=None, help='where isolated codedoc environments are created (default: temp dir)')
    ap.add_argument('--versions', default=None, metavar='FILE', help='file with old=/new= build descriptions')
    ap.add_argument('--plugin', default=None, metavar='DIR', help='plugin source tree whose skills are sized')
    ap.add_argument('--since', default=None, help='ISO time (UTC), e.g. 2026-10-10T18:00:00Z')
    ap.add_argument('--until', default=None, help='ISO time (UTC), exclusive upper bound')
    ap.add_argument('--no-measure', action='store_true', help='skip counterfactual measurements')
    ap.add_argument('--window', type=int, default=6, help='tool-call window for paging episodes')
    ARGS = ap.parse_args(argv)
    REPO = os.path.realpath(ARGS.repo)
    PROJECT = ARGS.project or project_dir_for(REPO)
    WORKSPACE = ARGS.workspace or os.path.basename(REPO)
    SINCE, UNTIL, W_EP = ARGS.since, ARGS.until, ARGS.window
    ENV_DIR = os.path.abspath(ARGS.env_dir) if ARGS.env_dir else None
    BIN = {v: os.path.abspath(b) for v, b in (('old', ARGS.old), ('new', ARGS.new)) if b}
    return ARGS


def in_scope(k):
    """True when a call's timestamp lies inside [--since, --until)."""
    ts = k.ts or ''
    return (SINCE is None or ts >= SINCE) and (UNTIL is None or ts < UNTIL)


LOOKUP = {'outline', 'symbol', 'find', 'callers', 'callees', 'refs', 'explain', 'search', 'overview',
          'flow', 'impls', 'supertypes', 'siblings', 'tests', 'deps', 'dependents', 'config', 'history',
          'at', 'status', 'ls', 'lines'}
MAINT = {'changes', 'gaps', 'doc check'}
WRITE = {'doc set'}


def tok(chars):
    return int(round(chars / CPT))


def fmt(n):
    return f'{int(round(n)):,}'


def table(headers, rows, out=None):
    out = out if out is not None else sys.stdout
    out.write('| ' + ' | '.join(headers) + ' |\n')
    out.write('|' + '|'.join('---' for _ in headers) + '|\n')
    for r in rows:
        out.write('| ' + ' | '.join(str(c).replace('|', '\\|').replace('\n', ' ') for c in r) + ' |\n')
    out.write('\n')


# ---------------------------------------------------------------- shell parsing

HEREDOC_RE = re.compile(r"<<-?\s*(['\"]?)([A-Za-z_][A-Za-z0-9_]*)\1")


def strip_heredocs(cmd):
    """Remove heredoc bodies; return (text, [bodies])."""
    lines = cmd.split('\n')
    out, bodies, i = [], [], 0
    while i < len(lines):
        ln = lines[i]
        m = HEREDOC_RE.search(ln)
        if m:
            word = m.group(2)
            body = []
            i += 1
            while i < len(lines) and lines[i].strip() != word:
                body.append(lines[i])
                i += 1
            bodies.append('\n'.join(body))
            out.append(HEREDOC_RE.sub(f'<<HEREDOC{len(bodies) - 1}', ln, count=1))
        else:
            out.append(ln)
        i += 1
    return '\n'.join(out), bodies


def split_shell(text):
    """Split into pipelines: list of (connector_before, [segment strings])."""
    text = text.replace('\\\n', ' ')
    segs = []  # (connector, text)
    buf, conn = [], ''
    i, n = 0, len(text)
    st = []
    while i < n:
        c = text[i]
        top = st[-1] if st else None
        nxt = text[i + 1] if i + 1 < n else ''
        if top == 'sq':
            buf.append(c)
            if c == "'":
                st.pop()
        elif top == 'dq':
            buf.append(c)
            if c == '\\' and nxt:
                buf.append(nxt); i += 1
            elif c == '"':
                st.pop()
            elif c == '$' and nxt == '(':
                buf.append(nxt); i += 1; st.append('paren')
            elif c == '`':
                st.append('bt')
        elif top == 'bt':
            buf.append(c)
            if c == '`':
                st.pop()
        elif c == "'":
            st.append('sq'); buf.append(c)
        elif c == '"':
            st.append('dq'); buf.append(c)
        elif c == '`':
            st.append('bt'); buf.append(c)
        elif c == '\\' and nxt:
            buf.append(c); buf.append(nxt); i += 1
        elif c == '(':
            st.append('paren'); buf.append(c)
        elif c == ')':
            if top == 'paren':
                st.pop()
            buf.append(c)
        elif st:
            buf.append(c)
        elif c in ';\n':
            segs.append((conn, ''.join(buf))); buf = []; conn = ';'
        elif c == '&' and nxt == '&':
            segs.append((conn, ''.join(buf))); buf = []; conn = '&&'; i += 1
        elif c == '|' and nxt == '|':
            segs.append((conn, ''.join(buf))); buf = []; conn = '||'; i += 1
        elif c == '|':
            segs.append((conn, ''.join(buf))); buf = []; conn = '|'
            if nxt == '&':
                i += 1
        elif c == '&' and not (buf and buf[-1] == '>') and nxt != '>':
            segs.append((conn, ''.join(buf))); buf = []; conn = ';'
        else:
            buf.append(c)
        i += 1
    segs.append((conn, ''.join(buf)))
    pipes = []
    for conn, s in segs:
        s = s.strip()
        if not s:
            continue
        if conn == '|' and pipes:
            pipes[-1][1].append(s)
        else:
            pipes.append((conn, [s]))
    return pipes


SUBSTS = []


def hide_substs(seg):
    """Replace $(...) with @SUBSTn@ so shlex can split the rest."""
    out, i, n = [], 0, len(seg)
    while i < n:
        if seg.startswith('$(', i):
            d, j = 1, i + 2
            while j < n and d:
                if seg[j] == '(':
                    d += 1
                elif seg[j] == ')':
                    d -= 1
                j += 1
            SUBSTS.append(seg[i + 2:j - 1])
            out.append('@SUBST%d@' % (len(SUBSTS) - 1))
            i = j
        else:
            out.append(seg[i]); i += 1
    return ''.join(out)


def words_of(seg):
    seg = hide_substs(seg)
    try:
        return shlex.split(seg, posix=True)
    except ValueError:
        return seg.split()


REDIR_RE = re.compile(r'(?:^|[^0-9&>])(>>?)\s*([^\s;&|>]+)')
KEYWORDS = {'then', 'do', 'else', '{', '(', '!', 'time', 'command', 'if', 'while', 'elif', 'exec'}


def clean_words(ws):
    """Drop redirections, leading keywords, env assignments, timeout prefixes."""
    out = []
    skip = False
    for w in ws:
        if skip:
            skip = False
            continue
        if re.fullmatch(r'\d*(>>?|<)', w):
            skip = True
            continue
        if re.match(r'^\d*(>>?|<)[^<]', w) or re.match(r'^\d*>&\d$', w) or w.startswith('&>') or w.startswith('<<'):
            continue
        out.append(w)
    while out:
        w = out[0]
        if w in KEYWORDS:
            out = out[1:]
        elif w.startswith('(') and len(w) > 1:
            out[0] = w[1:]
        elif re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]*=.*', w) and len(out) > 1:
            out = out[1:]
        elif w == 'timeout' and len(out) > 2:
            out = out[2:]
        else:
            break
    return out


def stdout_redirect(seg):
    m = None
    for m in REDIR_RE.finditer(' ' + seg):
        pass
    return m.group(2) if m else None


VAL_FLAGS_GREP_LONG = {'--include', '--exclude', '--exclude-dir', '--max-count', '--context', '--after-context',
                       '--before-context', '--regexp', '--file', '--glob', '--type', '-g', '-t', '--max-depth'}


def parse_grep(ws):
    """ws excludes the command name. Returns dict(pattern, files, ctx, flags)."""
    pat, files, ctx, flags = None, [], 0, set()
    i = 0
    explicit = []
    while i < len(ws):
        w = ws[i]
        if w == '--':
            files += ws[i + 1:] if pat is not None else ws[i + 2:]
            if pat is None and i + 1 < len(ws):
                pat = ws[i + 1]
            break
        if w.startswith('--'):
            name = w.split('=')[0]
            flags.add(name)
            if name in VAL_FLAGS_GREP_LONG and '=' not in w:
                i += 1
            if name in ('--context', '--after-context', '--before-context'):
                ctx = max(ctx, 3)
        elif w.startswith('-') and len(w) > 1 and not re.fullmatch(r'-\d+', w):
            body = w[1:]
            m = re.match(r'^([a-zA-Z]*?)([ABCemfgt])(\S*)$', body)
            took = False
            for ch in body:
                if ch.isalpha():
                    flags.add('-' + ch)
            m2 = re.search(r'([ABCemfgt])(\d*|.*)$', body)
            if m2 and m2.group(1) in 'ABC':
                val = m2.group(2)
                if val == '' and i + 1 < len(ws):
                    val = ws[i + 1]; i += 1
                try:
                    ctx = max(ctx, int(val))
                except ValueError:
                    pass
            elif m2 and m2.group(1) in 'emfgt' and body.endswith(m2.group(1)):
                if i + 1 < len(ws):
                    if m2.group(1) == 'e':
                        explicit.append(ws[i + 1])
                    i += 1
        elif re.fullmatch(r'-\d+', w):
            ctx = max(ctx, int(w[1:]))
        else:
            if pat is None and not explicit:
                pat = w
            else:
                files.append(w)
        i += 1
    if explicit:
        pat = '\\|'.join(explicit)
    return {'pattern': pat or '', 'files': files, 'ctx': ctx, 'flags': flags}


def parse_sed(ws):
    """Returns (is_inplace, quiet, script, files)."""
    inplace = quiet = False
    script, files = None, []
    i = 0
    while i < len(ws):
        w = ws[i]
        if w.startswith('-') and len(w) > 1 and script is None:
            if 'i' in w and not w.startswith('--'):
                inplace = True
            if w == '--in-place':
                inplace = True
            if 'n' in w and not w.startswith('--'):
                quiet = True
            if w.endswith('e') and i + 1 < len(ws):
                script = ws[i + 1]; i += 1
        elif script is None:
            script = w
        else:
            files.append(w)
        i += 1
    return inplace, quiet, script or '', files


def parse_headtail(ws):
    n, files = 10, []
    i = 0
    while i < len(ws):
        w = ws[i]
        if re.fullmatch(r'-\d+', w):
            n = int(w[1:])
        elif w in ('-n', '-c', '--lines') and i + 1 < len(ws):
            try:
                n = int(ws[i + 1].lstrip('+-'))
            except ValueError:
                pass
            i += 1
        elif re.fullmatch(r'-n\+?\d+', w):
            n = int(w[2:].lstrip('+'))
        elif w.startswith('-'):
            pass
        else:
            files.append(w)
        i += 1
    return n, files


RANGE_RE = re.compile(r"^(\d+),(\+?)(\d+|\$)p$")


def sed_range(script):
    m = RANGE_RE.match(script.strip())
    if m:
        a = int(m.group(1))
        if m.group(3) == '$':
            return (a, None)
        b = int(m.group(3))
        return (a, a + b) if m.group(2) else (a, b)
    m = re.match(r'^(\d+)p$', script.strip())
    if m:
        return (int(m.group(1)), int(m.group(1)))
    m = re.match(r'^@SUBST(\d+)@,\+(\d+)p$', script.strip())
    if m:
        return ('subst', SUBSTS[int(m.group(1))], int(m.group(2)))
    return None


CODEDOC_VAL_FLAGS = {'--text', '--since', '--format', '--budget', '--offset', '--out', '--workspace', '--repo',
                     '--kind', '--lang', '--sort', '--depth', '--limit', '--until', '--ref', '--from', '--to',
                     '--file', '--top', '-n'}


def parse_codedoc(ws):
    """ws excludes 'codedoc'. Returns (sub, positionals, flags dict)."""
    pos, flags = [], {}
    i = 0
    while i < len(ws):
        w = ws[i]
        if w.startswith('-') and len(w) > 1:
            name = w.split('=')[0]
            if '=' in w:
                flags[name] = w.split('=', 1)[1]
            elif name in CODEDOC_VAL_FLAGS and i + 1 < len(ws):
                flags[name] = ws[i + 1]; i += 1
            else:
                flags[name] = True
        else:
            pos.append(w)
        i += 1
    if not pos:
        return ('help' if ('--help' in flags or '-h' in flags) else '(none)'), [], flags
    sub = pos[0]
    rest = pos[1:]
    if sub == 'doc' and rest:
        sub = 'doc ' + rest[0]
        rest = rest[1:]
    if '--help' in flags or '-h' in flags or sub == 'help':
        sub = 'help'
    return sub, rest, flags


def classify_target(path, cwd):
    """Return (class, abspath)."""
    if path is None:
        return 'unresolved', None
    p = path
    if p.startswith('~'):
        p = HOME + p[1:]
    if '$' in p or '`' in p or '@SUBST' in p:
        return 'unresolved', None
    ap_ = os.path.normpath(os.path.join(cwd or REPO, p))
    base = re.split(r'[*?\[]', ap_)[0]
    if not (base == REPO or base.startswith(REPO + '/')):
        if '/node_modules/' in ap_ + '/':
            return 'dependency', ap_
        if ap_ in OUT_FILES:
            return 'codedoc-out', ap_
        if re.search(r'\.(d\.ts|[cm]?[jt]sx?|py|go|rs)$', ap_) and not ap_.startswith(('/proc', '/sys')):
            return 'outside-source', ap_
        return 'outside', ap_
    rel = ap_[len(REPO) + 1:]
    if 'node_modules' in rel.split('/'):
        return 'dependency', ap_
    low = rel.lower()
    if rel.startswith('.git/') or rel == '.git':
        return 'repo-other', ap_
    if low.endswith(('.md', '.mdx')):
        return 'markdown', ap_
    if re.search(r'\.(test|spec)\.[cm]?[jt]sx?$', low) or '/__tests__/' in low or '/test/' in '/' + low:
        return 'test', ap_
    if re.search(r'\.[cm]?[jt]sx?$', low):
        return 'source', ap_
    if os.path.isdir(ap_) or '.' not in os.path.basename(rel):
        if rel.startswith(('docs', 'plugin/skills')):
            return 'markdown', ap_
        if rel.startswith(('packages', 'scripts', 'src')) or rel == '':
            return 'source', ap_
        return 'repo-other', ap_
    return 'repo-other', ap_


OUT_FILES = {}


class Unit:
    """One output-producing pipeline of a Bash call (or one non-Bash tool call)."""
    __slots__ = ('call', 'raw', 'kind', 'sub', 'addr', 'flags', 'files', 'tclass', 'rng', 'pattern', 'ctx',
                 'grepflags', 'filters', 'chars', 'exact', 'weight', 'wrapped', 'text_in', 'chunk', 'cap',
                 'recursive', 'via', 'sepline', 'silent', 'head_cmd')

    def __init__(self, call, raw, kind):
        self.call, self.raw, self.kind = call, raw, kind
        self.sub = self.addr = self.pattern = self.rng = self.chunk = self.sepline = None
        self.flags, self.files, self.filters, self.grepflags = {}, [], [], set()
        self.tclass = None
        self.ctx = 0
        self.chars = 0.0
        self.exact = False
        self.weight = 1.0
        self.wrapped = False
        self.text_in = 0
        self.cap = None
        self.recursive = False
        self.via = 'cli'
        self.silent = False
        self.head_cmd = ''


class Call:
    __slots__ = ('ts', 'name', 'input', 'id', 'cwd', 'file', 'session', 'ordinal', 'result', 'is_error',
                 'req_at', 'epoch', 'units', 'after_turns', 'agent', 'mid')

    def __init__(self):
        self.result = None
        self.is_error = False
        self.units = []
        self.after_turns = 0


def filter_info(segs):
    """Describe the downstream filters of a pipeline: names, line cap, grep presence."""
    names, cap, rng = [], None, None
    for s in segs:
        ws = clean_words(words_of(s))
        if not ws:
            continue
        names.append(ws[0])
        if ws[0] in ('head', 'tail'):
            n, _ = parse_headtail(ws[1:])
            cap = n if cap is None else min(cap, n)
            if rng is None:
                rng = (1, n) if ws[0] == 'head' else ('tail', n)
        elif ws[0] == 'sed':
            _, _, script, _ = parse_sed(ws[1:])
            r = sed_range(script)
            if r:
                if rng is None:
                    rng = r
                if r[1]:
                    cap = (r[1] - r[0] + 1) if cap is None else min(cap, r[1] - r[0] + 1)
        elif ws[0] == 'wc' or (ws[0] in ('grep', 'rg') and any(re.fullmatch(r'-[a-zA-Z]*c[a-zA-Z]*', w) for w in ws[1:])):
            cap = 1
    return names, cap, rng


def parse_bash(call):
    """Turn a Bash tool call into units."""
    cmd = call.input.get('command') or ''
    text, bodies = strip_heredocs(cmd)
    pipes = split_shell(text)
    units = []
    cwd = call.cwd or REPO
    funcs = {}
    in_def = None
    loop = None  # (var, [words])
    vars_ = {}
    for conn, segs in pipes:
        head = segs[0]
        m = re.match(r'^([A-Za-z_][A-Za-z0-9_]*)\s*\(\)\s*\{?\s*(.*)$', head, re.S)
        if m and in_def is None:
            in_def = m.group(1)
            head = m.group(2).strip()
            segs = [head] + segs[1:]
            funcs.setdefault(in_def, None)
        if in_def is not None:
            ws = clean_words(words_of(head)) if head else []
            if ws and ws[0] == 'codedoc':
                funcs[in_def] = (ws[1:], filter_info(segs[1:]))
            if any(s.strip().endswith('}') for s in segs) or head.strip() == '}':
                in_def = None
            continue
        ws = clean_words(words_of(head))
        if not ws:
            continue
        if ws[0] == 'for' and len(ws) >= 3 and 'in' in ws:
            k = ws.index('in')
            loop = (ws[1], ws[k + 1:])
            continue
        if ws[0] == 'done' or ws[0] == 'fi' or ws[0] == '}':
            if ws[0] == 'done':
                loop = None
            continue
        raw_head = head
        if re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]*=.*', ws[0]) and len(ws) == 1:
            mm = re.fullmatch(r'([A-Za-z_][A-Za-z0-9_]*)=([^$`(]*)', ws[0])
            if mm:
                vars_[mm.group(1)] = mm.group(2)
            continue
        if ws[0] == 'cd' and len(ws) >= 2:
            cl, apath = classify_target(ws[1], cwd)
            if apath:
                cwd = apath
            continue
        expansions = [None]
        if loop and re.search(r'\$\{?' + re.escape(loop[0]) + r'\b', head) and all('$' not in w and '*' not in w for w in loop[1]):
            expansions = loop[1]
        for exp in expansions:
            w2 = ws
            if exp is not None:
                w2 = [re.sub(r'\$\{?' + re.escape(loop[0]) + r'\}?', exp, w) for w in ws]
            w2 = [re.sub(r'\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?', lambda mo: vars_.get(mo.group(1), mo.group(0)), w) for w in w2]
            u = build_unit(call, w2, segs, raw_head if len(segs) == 1 else ' | '.join(segs), cwd, funcs, bodies)
            if u:
                units.append(u)
    return units


def build_unit(call, ws, segs, raw, cwd, funcs, bodies):
    cmd = ws[0]
    fnames, cap, frng = filter_info(segs[1:])
    redir = stdout_redirect(segs[-1])
    u = Unit(call, raw, 'other')
    u.filters, u.cap = fnames, cap
    u.head_cmd = cmd
    if redir and redir not in ('&1', '&2') and not redir.startswith('&'):
        u.silent = True
    hd = re.search(r'<<HEREDOC(\d+)', segs[0])
    if cmd in funcs and funcs[cmd] is not None:
        tmpl, (fn2, cap2, _) = funcs[cmd]
        args = ws[1:]

        def sub_arg(w):
            return re.sub(r'\$(\d)', lambda mo: args[int(mo.group(1)) - 1] if int(mo.group(1)) - 1 < len(args) else '', w)
        ws = ['codedoc'] + [sub_arg(w) for w in tmpl]
        cmd = 'codedoc'
        u.wrapped = True
        u.filters = fn2 + fnames
        u.cap = cap2 if cap is None else cap
    if cmd == 'codedoc':
        u.kind = 'codedoc'
        u.sub, pos, u.flags = parse_codedoc(ws[1:])
        u.addr = pos[0] if pos else None
        u.files = pos
        if u.sub == 'doc set':
            t = u.flags.get('--text')
            u.text_in = len(t) if isinstance(t, str) else (len(bodies[int(hd.group(1))]) if hd else 0)
        if '--out' in u.flags:
            u.silent = True
            if isinstance(u.flags['--out'], str):
                OUT_FILES[os.path.normpath(os.path.join(cwd, u.flags['--out']))] = u.sub
        if u.sub == 'doc set' and any(f in ('grep', 'rg') for f in u.filters):
            u.cap = 0.8  # prints only on failure
        return u
    if cmd == 'echo' or cmd == 'printf':
        u.kind = 'echo'
        u.weight = 0.0
        if cmd == 'echo' and not u.silent and len(segs) == 1:
            t = ' '.join(ws[1:])
            if t.strip() and '$' not in t and '\n' not in t:
                u.sepline = t
        return u
    if cmd == 'cat':
        files = [w for w in ws[1:] if not w.startswith('-')]
        if u.silent or hd or not files:
            u.kind = 'write' if (u.silent or hd) else 'other'
            return u
        u.files = files
        if not fnames:
            u.kind = 'whole'
        elif fnames[0] in ('head', 'tail', 'sed') and frng:
            u.kind = 'paged'; u.rng = frng
        elif fnames[0] in ('grep', 'rg'):
            u.kind = 'search'
            g = parse_grep(clean_words(words_of(segs[1]))[1:])
            u.pattern, u.ctx, u.grepflags = g['pattern'], g['ctx'], g['flags']
        else:
            u.kind = 'other'
            return u
    elif cmd == 'sed':
        inplace, quiet, script, files = parse_sed(ws[1:])
        if inplace or not files:
            u.kind = 'write' if inplace else 'other'
            return u
        u.files = files
        u.rng = sed_range(script) or ('pattern', script)
        if fnames and fnames[0] in ('grep', 'rg'):
            u.kind = 'search'
            g = parse_grep(clean_words(words_of(segs[1]))[1:])
            u.pattern, u.ctx, u.grepflags = g['pattern'], g['ctx'], g['flags']
        else:
            u.kind = 'paged'
    elif cmd in ('head', 'tail'):
        n, files = parse_headtail(ws[1:])
        if not files:
            return u
        u.files = files
        u.kind = 'paged'
        u.rng = (1, n) if cmd == 'head' else ('tail', n)
    elif cmd in ('grep', 'rg', 'egrep', 'fgrep'):
        g = parse_grep(ws[1:])
        files = g['files']
        rec = cmd == 'rg' or any(f in g['flags'] for f in ('-r', '-R', '--recursive'))
        if not files and not rec:
            return u
        u.kind = 'search'
        u.files = files or ['.']
        u.pattern, u.ctx, u.grepflags, u.recursive = g['pattern'], g['ctx'], g['flags'], rec
    elif cmd in ('ls', 'tree', 'find'):
        files = []
        for w in ws[1:]:
            if w.startswith('-') or w in ('!', '('):
                if cmd == 'find':
                    break
                continue
            files.append(w)
        u.kind = 'listing'
        u.files = files or ['.']
    else:
        return u
    if u.silent:
        u.kind = 'other'
        return u
    classes = [classify_target(f, cwd) for f in u.files]
    u.files = [a or f for (c, a), f in zip(classes, u.files)]
    cs = [c for c, _ in classes]
    u.tclass = collections.Counter(cs).most_common(1)[0][0]
    if 'codedoc-out' in cs:
        u.kind, u.sub, u.via, u.addr = 'codedoc', OUT_FILES.get(u.files[0], 'changes'), 'outfile', None
        return u
    if 'dependency' in cs:
        u.tclass = 'dependency'
    if u.kind in ('whole', 'paged') and len(u.files) > 1 and u.kind == 'paged':
        pass
    return u


def attribute(call):
    """Split the call's result length over its units (exact when separators allow)."""
    R = call.result or ''
    units = [u for u in call.units if not u.silent and u.kind not in ('write',)]
    for u in call.units:
        u.chars, u.exact = 0.0, False
    if not units:
        return
    for u in units:
        if u.kind == 'echo':
            u.weight = 0.0
        elif u.cap is not None:
            u.weight = max(0.05, min(u.cap, 40) / 40.0)
        else:
            u.weight = 1.0
    groups, cur, pos = [], [], 0
    lines_pos = None
    for u in units:
        if u.sepline is not None:
            idx = -1
            needle = u.sepline
            if R.startswith(needle + '\n', pos) or R[pos:] == needle:
                idx = pos
            else:
                j = R.find('\n' + needle + '\n', max(0, pos - 1))
                if j < 0 and R.endswith('\n' + needle):
                    j = len(R) - len(needle) - 1
                idx = j + 1 if j >= 0 else -1
            if idx >= 0:
                groups.append((cur, R[pos:idx]))
                u.chars, u.exact = len(needle) + 1, True
                pos = min(len(R), idx + len(needle) + 1)
                cur = []
                continue
        cur.append(u)
    groups.append((cur, R[pos:]))
    for members, chunk in groups:
        real = [u for u in members if u.weight > 0]
        if not real:
            real = members
        if not real:
            continue
        if len(real) == 1:
            real[0].chars, real[0].exact, real[0].chunk = len(chunk), True, chunk
            continue
        tw = sum(u.weight for u in real) or 1.0
        for u in real:
            u.chars = len(chunk) * u.weight / tw
            u.exact = False
            u.chunk = None


# ---------------------------------------------------------------- transcripts

class Transcript:
    def __init__(self, path, session, agent):
        self.path, self.session, self.agent = path, session, agent
        self.calls = []
        self.requests = {}  # msg id -> usage
        self.req_order = []
        self.req_ts = {}
        self.epoch_last = {}
        self.boundaries = []
        self.skills = []  # (ts, name)
        self.reinject = []  # (ts, name, chars)
        self.listing = []  # (ts, chars of codedoc lines)
        self.first_ts = self.last_ts = None
        self.schemas = []  # (ts, name, chars)


def load_transcript(path, session, agent):
    T = Transcript(path, session, agent)
    by_id = {}
    epoch = 0
    seen_schema = set()
    with open(path, errors='replace') as fh:
        for line in fh:
            try:
                o = json.loads(line)
            except Exception:
                continue
            ts = o.get('timestamp')
            if ts:
                T.first_ts = T.first_ts or ts
                T.last_ts = ts
            t = o.get('type')
            if t == 'system' and o.get('subtype') == 'compact_boundary':
                epoch += 1
                T.boundaries.append(ts)
                continue
            if t == 'attachment':
                a = o.get('attachment') or {}
                if a.get('type') == 'invoked_skills':
                    for s in a.get('skills') or []:
                        if 'codedoc' in (s.get('name') or ''):
                            T.reinject.append((ts, s.get('name'), len(s.get('content') or '')))
                elif a.get('type') == 'skill_listing':
                    c = a.get('content') or ''
                    T.listing.append((ts, sum(len(l) + 1 for l in c.split('\n') if l.startswith('- codedoc'))))
                elif a.get('type') == 'deferred_tools_record':
                    for e in a.get('entries') or []:
                        if 'codedoc' in (e.get('name') or '') and e['name'] not in seen_schema:
                            seen_schema.add(e['name'])
                            T.schemas.append((ts, e['name'], len(json.dumps(e))))
                continue
            m = o.get('message') or {}
            c = m.get('content')
            if t == 'assistant':
                mid = m.get('id') or o.get('uuid')
                if mid not in T.requests:
                    T.req_order.append(mid)
                    T.req_ts[mid] = ts
                    T.epoch_last[epoch] = len(T.req_order)
                if m.get('usage'):
                    T.requests[mid] = m['usage']
                else:
                    T.requests.setdefault(mid, {})
            if not isinstance(c, list):
                continue
            for b in c:
                if not isinstance(b, dict):
                    continue
                if b.get('type') == 'tool_use':
                    k = Call()
                    k.ts, k.name, k.input, k.id = ts, b.get('name'), b.get('input') or {}, b.get('id')
                    k.cwd, k.file, k.session, k.agent = o.get('cwd'), path, session, agent
                    k.ordinal = len(T.calls)
                    k.mid = m.get('id') or o.get('uuid')
                    k.req_at, k.epoch = len(T.req_order), epoch
                    T.calls.append(k)
                    by_id[k.id] = k
                    if k.name == 'Skill':
                        T.skills.append((ts, k.input.get('skill')))
                elif b.get('type') == 'tool_result':
                    k = by_id.get(b.get('tool_use_id'))
                    if not k:
                        continue
                    cc = b.get('content')
                    if isinstance(cc, list):
                        cc = ''.join(x.get('text', '') for x in cc if isinstance(x, dict))
                    k.result = cc or ''
                    k.is_error = bool(b.get('is_error'))
                    k.req_at, k.epoch = len(T.req_order), epoch
    for k in T.calls:
        k.after_turns = max(0, T.epoch_last.get(k.epoch, k.req_at) - k.req_at)
    return T


def units_for(call):
    if call.name == 'Bash':
        us = parse_bash(call)
    else:
        u = Unit(call, call.name + ' ' + json.dumps(call.input)[:200], 'other')
        n = call.name or ''
        inp = call.input
        if 'codedoc' in n and n.startswith('mcp__'):
            u.kind, u.via = 'codedoc', 'mcp'
            u.sub = n.split('codedoc_')[-1].replace('doc_set', 'doc set').replace('doc_check', 'doc check')
            u.addr = inp.get('name') or inp.get('path') or inp.get('symbol') or inp.get('query') or inp.get('target')
            u.flags = {('--' + k): v for k, v in inp.items()}
        elif n == 'Read':
            fp = inp.get('file_path')
            u.kind = 'paged' if (inp.get('offset') or inp.get('limit')) else 'whole'
            if u.kind == 'paged':
                a = int(inp.get('offset') or 1)
                u.rng = (a, a + int(inp.get('limit') or 2000) - 1)
            cl, apath = classify_target(fp, call.cwd)
            u.files, u.tclass = [apath or fp], cl
            u.head_cmd = 'Read'
        elif n == 'Grep':
            u.kind = 'search'
            u.pattern = inp.get('pattern') or ''
            cl, apath = classify_target(inp.get('path') or '.', call.cwd)
            u.files, u.tclass, u.recursive = [apath], cl, True
            u.ctx = max(int(inp.get(k) or 0) for k in ('-A', '-B', '-C', 'context'))
            u.head_cmd = 'Grep'
        elif n == 'Glob':
            u.kind = 'listing'
            cl, apath = classify_target(inp.get('path') or '.', call.cwd)
            u.files, u.tclass = [apath], cl
            u.head_cmd = 'Glob'
        us = [u]
    call.units = us
    attribute(call)
    return us


def load_all():
    ts_list = []
    for f in sorted(glob.glob(PROJECT + '/*.jsonl')):
        sid = os.path.basename(f)[:-6]
        ts_list.append(load_transcript(f, sid, None))
        for sf in sorted(glob.glob(os.path.join(PROJECT, sid, '**', '*.jsonl'), recursive=True)):
            ts_list.append(load_transcript(sf, sid, os.path.basename(sf)[:-6]))
    for T in ts_list:
        for k in T.calls:
            units_for(k)
    return ts_list


# ---------------------------------------------------------------- measurement helpers

_cache = {}


def sh(cmd, cwd=None, timeout=30):
    key = (cmd, cwd)
    if key not in _cache:
        try:
            p = subprocess.run(['bash', '-c', cmd], cwd=cwd or REPO, capture_output=True, timeout=timeout,
                               stdin=subprocess.DEVNULL, env=dict(os.environ, LC_ALL='C.UTF-8', GIT_OPTIONAL_LOCKS='0'))
            _cache[key] = (p.stdout + p.stderr).decode('utf-8', 'replace')
        except Exception as e:
            _cache[key] = ''
    return _cache[key]


def env_root(v):
    """Directory of the isolated codedoc environment of binary label v."""
    global ENV_DIR
    if ENV_DIR is None:
        ENV_DIR = tempfile.mkdtemp(prefix='codedoc-usage-')
    return os.path.join(ENV_DIR, v)


def cd_env(v):
    """Environment of binary label v: isolated data, config, state and runtime dirs, embedder off."""
    E = env_root(v)
    for d in ('data', 'state', 'rt', 'xdgdata'):
        os.makedirs(os.path.join(E, d), exist_ok=True)
    cfg = os.path.join(E, 'config.yaml')
    if not os.path.exists(cfg):
        with open(cfg, 'w') as fh:
            fh.write('workspaces:\n  %s:\n    repos:\n      - path: %s\n' % (WORKSPACE, REPO))
    env = dict(os.environ, CODEDOC_DATA=E + '/data', CODEDOC_CONFIG=cfg, XDG_STATE_HOME=E + '/state',
               XDG_RUNTIME_DIR=E + '/rt', XDG_DATA_HOME=E + '/xdgdata', CODEDOC_EMBEDDER='off')
    return env


def have(v):
    return bool(BIN.get(v)) and os.access(BIN[v], os.X_OK)


def cdx(v, args, timeout=60, stdin=None):
    key = ('cd', v, tuple(args))
    if key not in _cache:
        try:
            p = subprocess.run([BIN[v]] + list(args), cwd=REPO, capture_output=True,
                               timeout=timeout, env=cd_env(v), input=stdin,
                               stdin=None if stdin is not None else subprocess.DEVNULL)
            _cache[key] = ((p.stdout + p.stderr).decode('utf-8', 'replace'), p.returncode)
        except Exception as e:
            _cache[key] = ('codedoc: run failed: %s' % e, 99)
    return _cache[key]


def cdj(v, args):
    out, rc = cdx(v, list(args) + ['--format', 'json', '--budget', '0'])
    try:
        d = json.loads(out)
    except Exception:
        return []
    es = []
    for s in d.get('sections') or []:
        es += s.get('entries') or []
    return es


def cd_ok(out, rc):
    return rc == 0 and not re.search(r'^codedoc: ', out, re.M) and not re.search(r'^== .*\(0\)\s*$', out, re.M)


def parse_loc(loc):
    m = re.match(r'^(.*?):(\d+)(?:-(\d+))?$', loc or '')
    if not m:
        return None
    a = int(m.group(2))
    return m.group(1), a, int(m.group(3) or a)


def rel(p):
    if p and p.startswith(REPO + '/'):
        return p[len(REPO) + 1:]
    return p


def q(s):
    return shlex.quote(s)


def file_lines(path):
    try:
        with open(path, errors='replace') as fh:
            return fh.read().split('\n')
    except Exception:
        return None


def resolve_range(u, path):
    """Line range (A,B) of a paged read against the file at its current state."""
    L = file_lines(path)
    if L is None:
        return None
    n = len(L) - (1 if L and L[-1] == '' else 0)
    r = u.rng
    if not r:
        return None
    if r[0] == 'tail':
        return (max(1, n - r[1] + 1), n)
    if r[0] == 'subst':
        inner = r[1].strip()
        if re.fullmatch(r"""grep -n ('[^']*'|"[^"$`]*") [\w./-]+( \| head -1)? \| cut -d: -f1""", inner):
            out = sh(inner).split()
            if out and out[0].isdigit():
                a = int(out[0])
                return (a, min(n, a + r[2]))
        return None
    if r[0] == 'pattern':
        script = r[1].strip()
        if re.fullmatch(r"(/([^/\\]|\\.)*/|\d+)\s*,\s*(/([^/\\]|\\.)*/|\$|\+?\d+)\s*p", script):
            out = sh('sed -n %s %s' % (q(script[:-1] + '='), q(path)))
            nums = [int(x) for x in out.split() if x.isdigit()]
            if nums:
                return (min(nums), max(nums))
        return None
    a, b = r
    b = n if b is None else min(b, n)
    return (min(a, max(n, 1)), max(b, min(a, n)))


# ---------------------------------------------------------------- main analysis

def main():
    TS = load_all()
    calls = [k for T in TS for k in T.calls if in_scope(k)]
    units = [u for k in calls for u in k.units]
    cd_units = [u for u in units if u.kind == 'codedoc']
    rd_units = [u for u in units if u.kind in ('whole', 'paged', 'search', 'listing')]
    sessions = sorted({T.session for T in TS})
    short = lambda s: s[:8]
    P = sys.stdout.write

    P(f'# codedoc usage and token cost in {os.path.basename(REPO)} sessions\n\n')
    P(f'Generated by `usage.py` over `{PROJECT}`' + (f' since {SINCE}' if SINCE else '') + (f' until {UNTIL}' if UNTIL else '') + '. Times are UTC. '
      f'Token estimates are **chars / {CPT:g}** (estimate; no tokenizer was run). '
      'Transcripts were only read.\n\n')

    # ---- 1.1 sessions
    P('## 1. Usage\n\n### 1.1 Sessions\n\n')
    rows = []
    for s in sessions:
        Ts = [T for T in TS if T.session == s]
        ks = [k for T in Ts for k in T.calls if in_scope(k)]
        us = [u for k in ks for u in k.units]
        cdl = [u for u in us if u.kind == 'codedoc']
        nreq = sum(len(T.req_order) for T in Ts)
        first = min((T.first_ts for T in Ts if T.first_ts), default='')
        last = max((T.last_ts for T in Ts if T.last_ts), default='')
        rows.append([short(s), first[:16], last[11:16], len(Ts) - 1, nreq, len(ks),
                     sum(1 for u in cdl if u.sub in LOOKUP), sum(1 for u in cdl if u.sub in MAINT),
                     sum(1 for u in cdl if u.sub in WRITE), sum(1 for u in cdl if u.via == 'mcp'),
                     sum(1 for u in us if u.kind == 'whole'), sum(1 for u in us if u.kind == 'paged'),
                     sum(1 for u in us if u.kind == 'search'), sum(1 for u in us if u.kind == 'listing'),
                     sum(len(T.boundaries) for T in Ts)])
    table(['session', 'first', 'last', 'subagents', 'model requests', 'tool calls', 'codedoc lookup',
           'codedoc changes/gaps', 'codedoc doc set', '(of which MCP)', 'whole-file reads', 'paged reads',
           'searches', 'listings', 'compactions'], rows)
    P('Counts are commands, not tool calls: a Bash call is split on `;`, `&&`, `||`, `|`, newlines; shell '
      'functions and `for` loops that call codedoc are expanded to one entry per invocation. Subagent '
      'transcripts are folded into their parent session.\n\n')

    # ---- 1.2 codedoc by command
    P('### 1.2 codedoc calls by command\n\n')
    by = collections.defaultdict(list)
    for u in cd_units:
        by[u.sub].append(u)
    rows = []
    for sub, us in sorted(by.items(), key=lambda kv: -len(kv[1])):
        cls = 'lookup' if sub in LOOKUP else 'maintain' if sub in MAINT else 'write' if sub in WRITE else 'other'
        rows.append([sub, cls, len(us), sum(1 for u in us if u.via == 'mcp'), sum(1 for u in us if u.wrapped),
                     sum(1 for u in us if u.filters), len({u.call.id for u in us}),
                     fmt(sum(u.chars for u in us)), fmt(tok(sum(u.chars for u in us))),
                     fmt(sum(u.text_in for u in us)) if sub == 'doc set' else ''])
    table(['command', 'class', 'calls', 'via MCP', 'via shell function', 'piped through a filter',
           'tool calls containing them', 'result chars', 'est. tokens', 'doc text written (chars)'], rows)

    # ---- 1.3 skills
    P('### 1.3 Skills loaded\n\n')
    rows = []
    for T in TS:
        for ts, name in T.skills:
            if (SINCE is None or ts >= SINCE) and (UNTIL is None or ts < UNTIL):
                rows.append([short(T.session) + ('/' + T.agent[:12] if T.agent else ''), ts[:19], name,
                             'yes' if 'codedoc' in (name or '') else ''])
    table(['session', 'time', 'skill', 'codedoc'], sorted(rows, key=lambda r: r[1]))
    cd_sk = [r for r in rows if r[3]]
    P(f'codedoc skills loaded: {len(cd_sk)} ({", ".join(sorted({r[2] for r in cd_sk})) or "none"}). '
      '`codedoc:codedoc-lookup` loaded: ' + ('yes' if any('lookup' in r[2] for r in cd_sk) else '**never**') + '.\n\n')

    # ---- 1.4 hourly
    P('### 1.4 Hourly buckets\n\n')
    first_lookup = {}
    for u in cd_units:
        if u.sub in LOOKUP and u.via == 'cli':
            first_lookup[u.call.session] = min(first_lookup.get(u.call.session, 'z'), u.call.ts)
    eps = find_episodes(TS, in_scope)
    ep_by = collections.Counter((e['session'], e['ts'][:13]) for e in eps)
    hb = collections.defaultdict(lambda: collections.Counter())
    for u in units:
        key = (u.call.session, (u.call.ts or '')[:13])
        if u.kind == 'codedoc':
            c = 'cd_lookup' if u.sub in LOOKUP else 'cd_maint' if u.sub in MAINT else 'cd_write' if u.sub in WRITE else 'cd_other'
            hb[key][c] += 1
            hb[key]['cd_chars'] += u.chars
        elif u.kind in ('whole', 'paged', 'search', 'listing'):
            hb[key][u.kind] += 1
            hb[key]['rd_chars'] += u.chars
            if u.tclass in ('source', 'test', 'markdown'):
                hb[key]['rd_indexed'] += 1
    rows = []
    for (s, h) in sorted(hb):
        c = hb[(s, h)]
        if not (c['cd_lookup'] + c['cd_maint'] + c['cd_write'] + c['whole'] + c['paged'] + c['search'] + c['listing']):
            continue
        rows.append([short(s), h[5:] + 'h', c['cd_lookup'], c['cd_maint'], c['cd_write'], fmt(tok(c['cd_chars'])),
                     c['whole'], c['paged'], c['search'], c['listing'], c['rd_indexed'], fmt(tok(c['rd_chars'])),
                     ep_by.get((s, h), 0)])
    table(['session', 'hour', 'codedoc lookup', 'codedoc changes/gaps', 'codedoc doc set', 'codedoc est. tokens',
           'whole-file', 'paged', 'search', 'listing', 'reads on repo src/test/md', 'shell-read est. tokens',
           'paging episodes'], rows)
    for s, t0 in sorted(first_lookup.items()):
        b = collections.Counter(); a = collections.Counter()
        for u in units:
            if u.call.session != s:
                continue
            tgt = a if u.call.ts >= t0 else b
            if u.kind == 'codedoc':
                tgt['cd_lookup' if u.sub in LOOKUP else 'cd_other'] += 1
            elif u.kind in ('whole', 'paged', 'search', 'listing'):
                tgt[u.kind] += 1
                tgt['chars'] += u.chars
        P(f'Session {short(s)}: first CLI lookup command at {t0[:19]}. Before / after it: codedoc lookups '
          f'{b["cd_lookup"]} / {a["cd_lookup"]}; whole-file reads {b["whole"]} / {a["whole"]}; paged reads '
          f'{b["paged"]} / {a["paged"]}; searches {b["search"]} / {a["search"]}; listings {b["listing"]} / '
          f'{a["listing"]}; shell-read est. tokens {fmt(tok(b["chars"]))} / {fmt(tok(a["chars"]))}.\n\n')
        last_l = max(u.call.ts for u in cd_units if u.sub in LOOKUP and u.call.session == s)
        after_last = [u for u in rd_units if u.call.session == s and u.call.ts > last_l]
        P(f'Last lookup command in that session at {last_l[:19]}; competing shell reads/searches after it: '
          f'{len(after_last)}.\n\n')

    # ---- 1.5 competing reads matrix
    P('### 1.5 Competing shell/file reads by kind and target\n\n')
    kinds = ['whole', 'paged', 'search', 'listing']
    tcs = ['source', 'test', 'markdown', 'repo-other', 'dependency', 'outside-source', 'outside', 'unresolved']
    rows = []
    for kd in kinds:
        r = [kd]
        for tc in tcs:
            us = [u for u in rd_units if u.kind == kd and u.tclass == tc]
            r.append(f'{len(us)} / {fmt(tok(sum(u.chars for u in us)))}' if us else '0')
        us = [u for u in rd_units if u.kind == kd]
        r.append(f'{len(us)} / {fmt(tok(sum(u.chars for u in us)))}')
        rows.append(r)
    r = ['total']
    for tc in tcs:
        us = [u for u in rd_units if u.tclass == tc]
        r.append(f'{len(us)} / {fmt(tok(sum(u.chars for u in us)))}')
    r.append(f'{len(rd_units)} / {fmt(tok(sum(u.chars for u in rd_units)))}')
    rows.append(r)
    table(['kind (count / est. tokens)', 'repo source', 'test file', 'markdown', 'repo other (json, config)',
           'node_modules', 'source files outside the repo (downloaded SDK code, other projects)', 'other outside repo (logs, settings, /proc)', 'unresolved ($var)', 'all'], rows)
    tools = collections.Counter(u.head_cmd for u in rd_units)
    P('By command: ' + ', '.join(f'`{k}` {v}' for k, v in tools.most_common()) + '. '
      'Excluded: writes (`cat > f <<EOF`, `sed -i`), filters on piped command output, build/test/git commands. '
      f'whole = `cat f` / Read without range; paged = `sed -n A,Bp`, `head`, `tail`, `cat f | head`, Read with '
      f'offset/limit; search = `grep`/`rg`/Grep with file or -r targets; listing = `ls`/`find`/`tree`/Glob. '
      f'grep with context (-A/-B/-C >= 3): {sum(1 for u in rd_units if u.kind == "search" and u.ctx >= 3)} of '
      f'{sum(1 for u in rd_units if u.kind == "search")} searches.\n\n')

    # ---- 1.6 episodes
    P('### 1.6 Paging-for-a-symbol episodes\n\n')
    typ = collections.Counter(e['type'] for e in eps)
    P(f'Definition: reads of the same file at most {W_EP} tool calls apart, with either two or more different '
      f'line ranges (page-page) or a grep on the file followed by a ranged read of it (grep-then-page). '
      f'**{len(eps)} episodes** ({", ".join(f"{k}: {v}" for k, v in typ.items()) or "none"}), covering '
      f'{sum(len(e["units"]) for e in eps)} commands in {sum(e["ncalls"] for e in eps)} tool calls and '
      f'{fmt(tok(sum(e["chars"] for e in eps)))} est. tokens. Target classes: '
      + ', '.join(f'{k} {v}' for k, v in collections.Counter(e['tclass'] for e in eps).most_common()) + '.\n\n')
    rows = []
    for e in sorted(eps, key=lambda e: e['ts'], reverse=True)[:15]:
        rows.append([e['ts'][5:19], rel(e['file']), e['type'], e['ncalls'], fmt(tok(e['chars'])),
                     '; '.join('`' + c[:90] + '`' for c in e['cmds'][:5]) + (' ...' if len(e['cmds']) > 5 else '')])
    table(['time', 'file', 'type', 'tool calls', 'est. tokens', 'commands'], rows)

    # ---- 1.7 dead ends
    P('### 1.7 codedoc errors and dead ends\n\n')
    dead = find_dead_ends(TS, in_scope)
    kindc = collections.Counter(d['why'] for d in dead)
    nxt = collections.Counter(d['next'] for d in dead)
    P(f'{len(dead)} of {len(cd_units)} codedoc calls ended in an error or dead end: '
      + (', '.join(f'{k} {v}' for k, v in kindc.most_common()) or 'none') + '. What the agent did next: '
      + (', '.join(f'{k} {v}' for k, v in nxt.most_common()) or '-') + '.\n\n')
    rows = []
    for d in sorted(dead, key=lambda d: d['ts'], reverse=True)[:15]:
        rows.append([d['ts'][5:19], '`' + d['cmd'][:110] + '`', d['why'], d['res'][:200].replace('\n', ' / '), d['next']])
    table(['time', 'command', 'kind', 'result (first 200 chars)', 'next action'], rows)

    # ---- 2 tokens actual
    P('## 2. Tokens: actual\n\n')
    P(f'Method: length in characters of the `tool_result` returned to the model, tokens estimated as chars / {CPT:g}. '
      'A Bash call returns one result for all its commands; it is split between commands exactly where the agent '
      'printed `echo ----`-style separators or the call held a single output-producing command, otherwise prorated '
      '(equal weights, scaled down for commands capped by `head -N`/`tail -N`/`grep -c`).\n\n')
    cats = [('codedoc lookup', lambda u: u.kind == 'codedoc' and u.sub in LOOKUP),
            ('codedoc changes/gaps/doc check', lambda u: u.kind == 'codedoc' and u.sub in MAINT),
            ('codedoc doc set', lambda u: u.kind == 'codedoc' and u.sub in WRITE),
            ('codedoc other (help)', lambda u: u.kind == 'codedoc' and u.sub not in LOOKUP | MAINT | WRITE),
            ('shell whole-file read', lambda u: u.kind == 'whole'),
            ('shell paged read', lambda u: u.kind == 'paged'),
            ('shell search', lambda u: u.kind == 'search'),
            ('shell listing', lambda u: u.kind == 'listing')]
    rows = []
    mult = {}
    for name, pred in cats:
        us = [u for u in units if pred(u)]
        ch = sum(u.chars for u in us)
        ex = sum(u.chars for u in us if u.exact)
        tt = sum(u.chars * u.call.after_turns for u in us)
        m = (tt / ch) if ch else 0
        mult[name] = m
        rows.append([name, len(us), len({u.call.id for u in us}), fmt(ch), fmt(tok(ch)),
                     f'{100 * ex / ch:.0f}%' if ch else '-', f'{m:.0f}', fmt(tok(tt)),
                     fmt(tok(ch) * 2 + tok(tt) * 0.1)])
    table(['category', 'commands', 'tool calls', 'result chars', 'est. tokens (paid once)',
           'share attributed exactly', 'later turns in context (char-weighted mean)',
           'est. token-turns re-read from cache', 'est. cost in base-input-token equivalents'], rows)
    all_after = [k.after_turns for k in calls if k.result]
    P('**A result is paid again on every later model turn while it stays in context.** The first turn after it '
      'writes it to the prompt cache; each later turn reads it from cache until the next compaction or the end of '
      'the session. The "later turns" column is measured: for each result, the number of distinct assistant API '
      'requests (distinct `message.id`) after it and before the next `compact_boundary` in the same transcript. '
      f'Over all {len(all_after)} tool results in scope: mean {statistics.mean(all_after) if all_after else 0:.0f}, '
      f'median {statistics.median(all_after) if all_after else 0:.0f} later turns. The last column weights one cache '
      'write at 2x base input (the transcripts show 1-hour cache writes) and each cache read at 0.1x; these two '
      'price weights are assumptions, the turn counts are measured.\n\n')

    P('### 2.1 Per session\n\n')
    rows = []
    for s in sessions:
        us = [u for u in units if u.call.session == s]
        if not us:
            continue
        cdc = sum(u.chars for u in us if u.kind == 'codedoc')
        rdc = sum(u.chars for u in us if u.kind in kinds)
        allr = sum(len(k.result or '') for k in calls if k.session == s)
        rows.append([short(s), fmt(tok(cdc)), fmt(tok(rdc)), fmt(tok(allr)),
                     f'{100 * cdc / allr:.1f}%' if allr else '-', f'{100 * rdc / allr:.1f}%' if allr else '-'])
    table(['session', 'codedoc results est. tokens', 'shell read/search results est. tokens',
           'all tool results est. tokens', 'codedoc share of tool results', 'shell-read share'], rows)

    P('### 2.2 Model usage per session (from `message.usage`, deduplicated by `message.id`)\n\n')
    rows = []
    for s in sessions:
        tot = collections.Counter()
        n = 0
        for T in TS:
            if T.session != s:
                continue
            for mid, uu in T.requests.items():
                rts = T.req_ts.get(mid) or ''
                if (SINCE and rts < SINCE) or (UNTIL and rts >= UNTIL):
                    continue
                n += 1
                for k in ('input_tokens', 'cache_creation_input_tokens', 'cache_read_input_tokens', 'output_tokens'):
                    tot[k] += int(uu.get(k) or 0)
        us = [u for u in units if u.call.session == s]
        cdc = tok(sum(u.chars for u in us if u.kind == 'codedoc'))
        rdc = tok(sum(u.chars for u in us if u.kind in kinds))
        cdt = tok(sum(u.chars * (1 + u.call.after_turns) for u in us if u.kind == 'codedoc'))
        rdt = tok(sum(u.chars * (1 + u.call.after_turns) for u in us if u.kind in kinds))
        tin = tot['input_tokens'] + tot['cache_creation_input_tokens'] + tot['cache_read_input_tokens']
        fresh = tot['input_tokens'] + tot['cache_creation_input_tokens']
        rows.append([short(s), n, fmt(tot['input_tokens']), fmt(tot['cache_creation_input_tokens']),
                     fmt(tot['cache_read_input_tokens']), fmt(tin), fmt(tot['output_tokens']),
                     f'{100 * cdc / fresh:.2f}%' if fresh else '-', f'{100 * rdc / fresh:.2f}%' if fresh else '-',
                     f'{100 * cdt / tin:.2f}%' if tin else '-', f'{100 * rdt / tin:.2f}%' if tin else '-'])
    table(['session', 'requests', 'input (uncached)', 'cache creation', 'cache read', 'total input', 'output',
           'codedoc results / new input', 'shell reads / new input',
           'codedoc token-turns / total input', 'shell-read token-turns / total input'], rows)
    P('"new input" = uncached input + cache creation (every token that entered context once). "token-turns" = '
      'est. tokens x (1 + later turns in context), comparable with total input, which counts a token once per '
      'request that carried it. Shares are estimates (chars/4 over measured usage).\n\n')

    if ARGS.no_measure or not (have('old') and have('new')):
        P('## 3. Counterfactuals\n\nSkipped (`--no-measure` or the `codedoc-old` / `codedoc-new` binaries are '
          'missing: pass `--old BIN --new BIN`).\n')
        return
    counterfactuals(TS, units, cd_units, rd_units, eps, calls, P)


# ---------------------------------------------------------------- episodes and dead ends

def find_episodes(TS, in_scope):
    eps = []
    for T in TS:
        byfile = collections.defaultdict(list)
        for k in T.calls:
            if not in_scope(k):
                continue
            for u in k.units:
                if u.kind in ('paged', 'search') and len(u.files) == 1 and u.tclass in EP_CLASSES:
                    if u.kind == 'search' and (u.recursive and os.path.isdir(u.files[0])):
                        continue
                    byfile[u.files[0]].append(u)
        for f, us in byfile.items():
            chain = []
            for u in us + [None]:
                if u is not None and (not chain or u.call.ordinal - chain[-1].call.ordinal <= W_EP):
                    chain.append(u)
                    continue
                if chain:
                    rngs = {str(x.rng) for x in chain if x.kind == 'paged'}
                    gp = any(a.kind == 'search' and any(b.kind == 'paged' for b in chain[i + 1:])
                             for i, a in enumerate(chain))
                    t = 'page-page' if len(rngs) >= 2 else 'grep-then-page' if gp else None
                    if t:
                        eps.append({'ts': chain[0].call.ts, 'file': f, 'type': t, 'units': chain,
                                    'ncalls': len({x.call.id for x in chain}),
                                    'chars': sum(x.chars for x in chain), 'session': T.session,
                                    'tclass': chain[0].tclass, 'cmds': [x.raw for x in chain]})
                chain = [u] if u is not None else []
    return eps


EP_CLASSES = ('source', 'test', 'markdown', 'repo-other', 'dependency', 'outside-source')

DEAD_PATTERNS = [('not found', re.compile(r'^codedoc: .*(not found|no such|unknown symbol|no symbol)', re.I | re.M)),
                 ('ambiguous', re.compile(r'^codedoc: .*ambiguous|^== ambiguous', re.I | re.M)),
                 ('error', re.compile(r'^(codedoc: |Error: )|unknown (command|flag|shorthand)', re.M)),
                 ('empty result', re.compile(r'^== .*\(0\)\s*$', re.M)),
                 ('fresh:no', re.compile(r'fresh:no'))]


def addr_file(addr):
    if not addr:
        return None
    a = addr.split('#')[0]
    m = re.match(r'^[\w-]+:ts:(@[\w-]+/([\w-]+)|[\w-]+):(.*?\.[cm]?[jt]sx?)(/.*)?$', addr)
    if m:
        return ('packages/' + m.group(2) + '/' if m.group(2) else '') + m.group(3)
    a = re.sub(r':\d+(-\d+)?$', '', a)
    return a if '/' in a or '.' in a else None


def find_dead_ends(TS, in_scope):
    out = []
    for T in TS:
        seq = [u for k in T.calls for u in k.units]
        for i, u in enumerate(seq):
            if u.kind != 'codedoc' or not in_scope(u.call) or u.sub == 'help':
                continue
            R = u.call.result or ''
            text = u.chunk if (u.exact and u.chunk is not None) else None
            why = None
            if text is not None:
                for name, rx in DEAD_PATTERNS:
                    if name == 'empty result' and u.sub in ('overview', 'changes', 'gaps', 'status'):
                        continue
                    if rx.search(text):
                        why = name
                        break
                grepped = any(f in ('grep', 'rg', 'wc', 'cut', 'python3', 'awk') for f in u.filters)
                if why is None and not text.strip() and u.sub in LOOKUP and not grepped:
                    why = 'empty result'
            else:
                # prorated: only count error lines that name this call's address
                a = (u.addr or '')
                for ln in R.split('\n'):
                    if a and (a + ':') in ln and (u.sub.split()[0] + ' ') in ln and re.search(r'^codedoc: ', ln):
                        why = 'ambiguous' if 'ambiguous' in ln else 'not found' if 'not found' in ln else 'error'
                        text = ln
                        break
                if why is None and u.call.is_error and len(u.call.units) == 1:
                    why, text = 'error', R
            if why is None and u.call.is_error and u.exact and len([x for x in u.call.units if x.kind != 'echo']) == 1:
                why = 'error'
            if why is None:
                continue
            f = addr_file(u.addr)
            nxt = 'moved on'
            for v in seq[i + 1:]:
                if v.call.ordinal - u.call.ordinal > 3:
                    break
                if v.call.ordinal == u.call.ordinal:
                    continue  # same Bash call: written before the result was seen
                if v.kind == 'codedoc' and v.sub == u.sub and v.addr != u.addr and (
                        not f or (addr_file(v.addr) == f) or u.sub in ('find', 'search')):
                    nxt = 'retried codedoc with another address'
                    break
                if v.kind in ('whole', 'paged', 'search'):
                    same = f and any(rel(x) == f or (x or '').endswith(f) for x in v.files)
                    if same:
                        nxt = 'fell back to shell on the same file (%s)' % v.head_cmd
                        break
                    if u.sub in ('find', 'search', 'callers', 'refs') and v.kind == 'search':
                        nxt = 'fell back to grep'
                        break
            out.append({'ts': u.call.ts, 'cmd': u.raw, 'why': why, 'res': (text or '').strip(), 'next': nxt, 'unit': u})
    return out


# ---------------------------------------------------------------- counterfactuals

DECL_RX = r"^\s*(export |async |function |class |interface |type |const |let |static |public |private |#?[A-Za-z_]+\(.*\).*\{$)"


def sym_name(addr):
    if not addr:
        return None
    a = addr
    if '#' in a:
        a = a.split('#', 1)[1]
    elif re.match(r'^[\w-]+:\w+:', a):
        a = a.rsplit('/', 1)[-1]
    elif '/' in a:
        return None
    a = a.split('.')[-1] if not a.startswith('test:') else a
    return a.lstrip('#') or None


def counterfactuals(TS, units, cd_units, rd_units, eps, calls, P):
    for v in ('old', 'new'):
        cdx(v, ['index'], timeout=300)
    head_old = sh('git rev-parse --short HEAD').strip()
    P('## 3. Tokens: counterfactual (estimates)\n\n')
    vers = {}
    try:
        for ln in (open(ARGS.versions) if ARGS.versions else []):
            k, _, v = ln.strip().partition('=')
            vers[k] = v
    except OSError:
        pass
    P(f'All numbers in this section are **estimates**: the shell and codedoc commands were run now, against the '
      f'repo at `{head_old}`, not at the state each original call saw. codedoc was run from isolated indexes '
      f'(`codedoc-old` = {vers.get("old", "master build")}; `codedoc-new` = {vers.get("new", "feat/deps-index build")}), '
      'embedder off, default output budget (8000 bytes).\n\n')

    # observed habits
    sed_w = [u.rng[1] - u.rng[0] + 1 for u in rd_units if u.kind == 'paged' and u.rng and isinstance(u.rng[0], int)
             and u.rng[1] and u.tclass in ('source', 'test')]
    Wm = int(statistics.median(sed_w)) if sed_w else 40
    grep_caps = [u.cap for u in rd_units if u.kind == 'search' and u.cap and u.cap > 1]
    H = int(statistics.median(grep_caps)) if grep_caps else 40
    grep_sz = [u.chars for u in rd_units if u.kind == 'search' and u.exact and u.tclass in ('source', 'test')]
    paged_sz = [u.chars for u in rd_units if u.kind == 'paged' and u.exact and u.tclass in ('source', 'test')]
    ep_calls = [e['ncalls'] for e in eps]
    ep_cmds = [len(e['units']) for e in eps]
    P('### 3.1 Observed shell habits used for the "expected" column\n\n')
    table(['habit (measured from these sessions)', 'value'], [
        ['median `sed -n A,Bp` window on source/test files (lines)', Wm],
        ['median result of one paged read, exact-attributed (chars)', fmt(statistics.median(paged_sz)) if paged_sz else '-'],
        ['median result of one grep, exact-attributed (chars)', fmt(statistics.median(grep_sz)) if grep_sz else '-'],
        ['median `| head -N` cap the agent puts on greps (lines)', H],
        ['commands per paging episode (mean / median)', f'{statistics.mean(ep_cmds):.1f} / {statistics.median(ep_cmds):.0f}' if ep_cmds else '-'],
        ['tool calls per paging episode (mean / median)', f'{statistics.mean(ep_calls):.1f} / {statistics.median(ep_calls):.0f}' if ep_calls else '-'],
        ['share of searches piped to `head`', f'{100 * len(grep_caps) / max(1, sum(1 for u in rd_units if u.kind == "search")):.0f}%'],
    ])

    # ---------------- A: codedoc -> shell
    P('### 3.2 codedoc lookups that were made: what plain shell would have returned\n\n')
    P('low = tightest shell equivalent; expected = the agent\'s observed habit (grep for the name, then '
      f'`sed` windows of {Wm} lines until the symbol is covered; greps capped at `head -{H}`); high = `cat` of the '
      'whole file(s) involved. "actual" is what reached the model, i.e. after the agent\'s own `| head` / `| tail` '
      'filter; "unfiltered today" is the same codedoc command rerun now without the filter.\n\n')
    rowsA = []
    agg = collections.defaultdict(lambda: collections.Counter())
    detail = []
    for u in cd_units:
        if u.sub not in LOOKUP and u.sub not in MAINT:
            continue
        r = shell_equiv(u, Wm, H)
        a = agg[u.sub]
        a['n'] += 1
        a['actual'] += u.chars
        if r is None:
            a['noeq'] += 1
            a['actual_noeq'] += u.chars
            continue
        a['unf'] += r['unf']
        for k in ('low', 'exp', 'high', 'lowc', 'expc', 'highc'):
            a[k] += r[k]
        a['changed'] += 1 if r['changed'] else 0
        a['unres'] += 1 if r['unresolved'] else 0
        detail.append((u, r))
    nav = [s for s in agg if s in LOOKUP]
    for sub in sorted(agg, key=lambda s: -agg[s]['n']):
        a = agg[sub]
        ne = a['n'] - a['noeq']
        rowsA.append([sub, a['n'], ne, fmt(tok(a['actual'] - a['actual_noeq'])), fmt(tok(a['unf'])),
                      fmt(tok(a['low'])), fmt(tok(a['exp'])), fmt(tok(a['high'])),
                      ne, a['lowc'], a['expc'], a['highc'], a['changed'], a['unres']])

    def tot(keys, subs):
        return [sum(agg[s][k] for s in subs) for k in keys]
    for label, subs in (('**navigation lookups total**', nav), ('**all (incl. changes)**', list(agg))):
        n, noeq, act, actne, unf, lo, ex, hi, lc, ec, hc, chg, unr = tot(
            ['n', 'noeq', 'actual', 'actual_noeq', 'unf', 'low', 'exp', 'high', 'lowc', 'expc', 'highc', 'changed', 'unres'], subs)
        rowsA.append([label, n, n - noeq, fmt(tok(act - actne)), fmt(tok(unf)), fmt(tok(lo)), fmt(tok(ex)),
                      fmt(tok(hi)), n - noeq, lc, ec, hc, chg, unr])
    table(['codedoc command', 'calls', 'with a shell equivalent', 'actual est. tokens', 'unfiltered today',
           'shell low', 'shell expected', 'shell high', 'codedoc commands', 'shell commands low',
           'shell commands expected', 'shell commands high', 'file changed since call', 'symbol not resolvable now'],
          rowsA)
    P('`changes --since` is compared with `git diff --stat` (low), stat plus changed declaration lines (expected) '
      'and the full `git diff` (high) of the commit that followed the call; its doc-state flags (`doc:stale`, '
      '`doc:missing`), which is what the agent grepped for, have **no shell equivalent**, so read that row as '
      '"what git would print", not as an equivalent answer. `gaps`, `doc set` and `doc check` have no shell '
      'equivalent and are left out. The agent filtered most `changes` output down to a count or a few lines, so '
      'its actual cost is far below the unfiltered answer.\n\n')
    rows = []
    for u, r in detail:
        if u.sub in LOOKUP:
            rows.append([u.call.ts[5:19], '`' + u.raw[:80] + '`', fmt(u.chars) + ('' if u.exact else '~'), fmt(r['unf']),
                         fmt(r['low']), fmt(r['exp']), fmt(r['high']), r['note']])
    P('Per lookup call (chars; `~` = prorated share of a mixed Bash result):\n\n')
    table(['time', 'command', 'actual', 'unfiltered today', 'shell low', 'shell expected', 'shell high', 'note'], rows)

    # ---------------- B: shell -> codedoc
    P('### 3.3 Shell reads/searches that were made: what codedoc would have returned\n\n')
    P('For each competing read the equivalent codedoc command was run with both binaries. Because files changed '
      'since the reads, the baseline for paged and whole-file reads is the **same shell read rerun today** '
      '(`sed -n A,Bp` / `cat` on the current file), not the historical result; searches and listings keep their '
      'historical size.\n\n'
      '- Paged read of lines A-B. The symbols a reader of those lines needs are resolved by sampling '
      '`symbol <path>:<line>` across the range (members of 1-3 lines are lifted to their container). '
      'old = `symbol <key> --body` for each of them (expected and high; low = only the symbol holding the middle '
      'line). new: expected = `lines <path> A-B` (the exact range), low = `symbol <path>:<mid> --body` when smaller '
      '(it may cover less than the range), high = `symbol --body` for every symbol in the range.\n'
      '- grep for identifiers that are indexed symbols: `find <name>` (single-file or declaration grep), '
      '`callers` + `refs` (recursive usage grep), `symbol --body` (grep with -A/-B context). At most 5 names per grep.\n'
      '- Markdown: heading grep = `outline doc.md`; ranged read = `lines` / section body (new only).\n'
      '- `ls`/`find`/`tree` = `codedoc ls <path>` (new only). node_modules and downloaded SDK sources: '
      '`find <name> --deps` (new only, declaration lookup).\n'
      '- Whole-file `cat`: low = `outline <file>`, expected = outline plus one third of the file '
      '(**assumption**: a third of the bodies were needed), high = the file itself (no saving).\n\n')
    res = [(u, codedoc_equiv(u)) for u in rd_units]
    for u, r in res:
        r['base'] = r.get('now', u.chars)
    groups = collections.defaultdict(list)
    for u, r in res:
        groups[(u.kind, u.tclass)].append((u, r))
    rows = []
    T = collections.Counter()
    for (kd, tc), items in sorted(groups.items(), key=lambda kv: -sum(x[0].chars for x in kv[1])):
        n = len(items)
        act = sum(u.chars for u, r in items)
        eo = [(u, r) for u, r in items if r['old'] is not None]
        en = [(u, r) for u, r in items if r['new'] is not None]
        rows.append([kd, tc, n, fmt(tok(act)),
                     len(eo), fmt(tok(sum(r['base'] for u, r in eo))),
                     ' / '.join(fmt(tok(sum(r.get(k, r['old']) for u, r in eo))) for k in ('old_lo', 'old', 'old_hi')) if eo else '-',
                     len(en), fmt(tok(sum(r['base'] for u, r in en))),
                     ' / '.join(fmt(tok(sum(r.get(k, r['new']) for u, r in en))) for k in ('new_lo', 'new', 'new_hi')) if en else '-'])
        T['n'] += n; T['act'] += act
        T['eo'] += len(eo); T['eo_act'] += sum(r['base'] for u, r in eo); T['eo_cd'] += sum(r['old'] for u, r in eo)
        T['en'] += len(en); T['en_act'] += sum(r['base'] for u, r in en); T['en_cd'] += sum(r['new'] for u, r in en)
        T['eo_hist'] += sum(u.chars for u, r in eo); T['en_hist'] += sum(u.chars for u, r in en)
        T['eo_inv'] += sum(r['old_n'] for u, r in eo); T['en_inv'] += sum(r['new_n'] for u, r in en)
        T['eo_lo'] += sum(r.get('old_lo', r['old']) for u, r in eo); T['eo_hi'] += sum(r.get('old_hi', r['old']) for u, r in eo)
        T['en_lo'] += sum(r.get('new_lo', r['new']) for u, r in en); T['en_hi'] += sum(r.get('new_hi', r['new']) for u, r in en)
    rows.append(['**total**', '', T['n'], fmt(tok(T['act'])), T['eo'], fmt(tok(T['eo_act'])),
                 f'{fmt(tok(T["eo_lo"]))} / {fmt(tok(T["eo_cd"]))} / {fmt(tok(T["eo_hi"]))}',
                 T['en'], fmt(tok(T['en_act'])),
                 f'{fmt(tok(T["en_lo"]))} / {fmt(tok(T["en_cd"]))} / {fmt(tok(T["en_hi"]))}'])
    table(['kind', 'target', 'reads', 'actual est. tokens (historical)', 'old: reads with an equivalent',
           'old: shell baseline today', 'old: codedoc low / expected / high', 'new: reads with an equivalent',
           'new: shell baseline today', 'new: codedoc low / expected / high'], rows)
    P('Reads with **no codedoc equivalent**, by reason (old binary / new binary):\n\n')
    ro = collections.defaultdict(lambda: [0, 0.0]); rn = collections.defaultdict(lambda: [0, 0.0])
    for u, r in res:
        if r['old'] is None:
            ro[r['why_old']][0] += 1; ro[r['why_old']][1] += u.chars
        if r['new'] is None:
            rn[r['why_new']][0] += 1; rn[r['why_new']][1] += u.chars
    rows = []
    for why in sorted(set(ro) | set(rn), key=lambda w: -(ro[w][1] + rn[w][1])):
        rows.append([why, ro[why][0], fmt(tok(ro[why][1])), rn[why][0], fmt(tok(rn[why][1]))])
    table(['reason', 'old: reads', 'old: est. tokens', 'new: reads', 'new: est. tokens'], rows)

    pg = [(u, r) for u, r in res if u.kind == 'paged' and r['old'] is not None and r.get('now')]
    if pg:
        bigger = sum(1 for u, r in pg if r['old'] > r['now'] * 1.1)
        omitted = 0
        for u, r in pg:
            for key in cover_keys(rel(u.files[0]), *resolve_range(u, u.files[0]))[:12]:
                if 'body omitted' in cdx('old', ['symbol', key, '--body'])[0]:
                    omitted += 1
        P(f'Paged reads of source/test files with an old-binary equivalent: {len(pg)}. In {bigger} of them the '
          f'symbol bodies needed to cover the range are more than 10% larger than the range itself; median ratio '
          f'codedoc-old / `sed` today = {statistics.median(r["old"] / r["now"] for u, r in pg):.2f}; median '
          f'`symbol --body` commands per paged read = {statistics.median(r["old_n"] for u, r in pg):.0f} '
          f'(max {max(r["old_n"] for u, r in pg)}). {omitted} of those `symbol --body` answers came back with '
          f'`[body omitted ...; use --out]` because the symbol exceeds the 8000-byte budget.\n\n')
    lo = [r['lines_over'] for u, r in res if r.get('lines_over') is not None and r.get('now')]
    if lo:
        rel_over = [r['lines_over'] / r['now'] for u, r in res if r.get('lines_over') is not None and r.get('now')]
        P(f'`codedoc-new lines <path> A-B` against `sed -n A,Bp` for the same {len(lo)} ranges: median '
          f'+{statistics.median(lo):.0f} chars per call (+{100 * statistics.median(rel_over):.0f}%), from the two header '
          f'lines, the "in <symbols>" line and the line-number column.\n\n')
    ls_ = [(u, r) for u, r in res if u.kind == 'listing' and r['new'] is not None]
    if ls_:
        P(f'`codedoc-new ls` against the {len(ls_)} `ls` calls on indexed directories: '
          f'{fmt(sum(r["new"] for u, r in ls_))} chars against {fmt(sum(u.chars for u, r in ls_))} chars '
          '(it prints mtime, size, line count, kind and symbol count per file, recursively).\n\n')
    dp = [(u, r) for u, r in res if r.get('dep_find')]
    osrc = [u for u, r in res if u.tclass == 'outside-source']
    if osrc:
        P(f'Third-party source read outside the repo (SDK sources downloaded to the session scratchpad, other '
          f'checkouts): {len(osrc)} reads, {fmt(tok(sum(u.chars for u in osrc)))} est. tokens. Only '
          f'{len(dp)} greps on TypeScript files name a symbol that `find --deps` (new) resolves in the installed '
          'packages; the Python SDK sources and file-range reads have no equivalent.\n\n')

    # episode-level comparison: union of the ranges read vs the symbols covering them, each fetched once
    E = collections.Counter()
    for e in eps:
        if e['tclass'] not in ('source', 'test') or not os.path.isfile(e['file']):
            continue
        keys, now, seen = [], 0, set()
        for x in e['units']:
            if x.kind == 'paged':
                rg = resolve_range(x, e['file'])
                if not rg:
                    now += x.chars
                    continue
                if rg not in seen:
                    seen.add(rg)
                now += len(sh(f'sed -n {rg[0]},{rg[1]}p {q(e["file"])}'))
                for k in cover_keys(rel(e['file']), rg[0], rg[1]):
                    if k not in keys:
                        keys.append(k)
            else:
                now += x.chars
        if not keys:
            continue
        E['n'] += 1; E['shell'] += now; E['cmds'] += len(e['units']); E['calls'] += e['ncalls']
        E['keys'] += len(keys)
        E['old'] += sum(len(cdx('old', ['symbol', k, '--body'])[0]) for k in keys)
        E['new'] += sum(len(cdx('new', ['symbol', k, '--body'])[0]) for k in keys)
    if E['n']:
        P(f'**Per paging episode** (source/test files, {E["n"]} episodes): the shell route read '
          f'{fmt(tok(E["shell"]))} est. tokens (ranges rerun today) with {E["cmds"]} commands in {E["calls"]} tool '
          f'calls; fetching each symbol those ranges touch once with `symbol --body` returns '
          f'{fmt(tok(E["old"]))} (old) / {fmt(tok(E["new"]))} (new) est. tokens with {E["keys"]} commands, which fit '
          f'in {E["n"]} tool calls when the names are known. Estimate.\n\n')

    # ---------------- sums and round trips
    P('### 3.4 Summary, both directions (est. tokens, paid once per result)\n\n')
    navA = [(u, r) for u, r in detail if u.sub in LOOKUP]
    a_act = sum(u.chars for u, r in navA)
    rows = [
        ['codedoc lookups made (navigation)', len(navA), fmt(tok(a_act)),
         f'shell: {fmt(tok(sum(r["low"] for u, r in navA)))} / {fmt(tok(sum(r["exp"] for u, r in navA)))} / '
         f'{fmt(tok(sum(r["high"] for u, r in navA)))}',
         f'{len(navA)} codedoc commands in {len({u.call.id for u, r in navA})} tool calls',
         f'shell commands: {sum(r["lowc"] for u, r in navA)} / {sum(r["expc"] for u, r in navA)} / '
         f'{sum(r["highc"] for u, r in navA)}'],
        ['shell reads made, old binary could serve', T['eo'], fmt(tok(T['eo_act'])),
         f'codedoc old: {fmt(tok(T["eo_lo"]))} / {fmt(tok(T["eo_cd"]))} / {fmt(tok(T["eo_hi"]))}',
         f'{T["eo"]} shell commands', f'{T["eo_inv"]} codedoc commands'],
        ['shell reads made, new binary could serve', T['en'], fmt(tok(T['en_act'])),
         f'codedoc new: {fmt(tok(T["en_lo"]))} / {fmt(tok(T["en_cd"]))} / {fmt(tok(T["en_hi"]))}',
         f'{T["en"]} shell commands', f'{T["en_inv"]} codedoc commands'],
        ['shell reads made, no equivalent (new binary)', T['n'] - T['en'], fmt(tok(T['act'] - T['en_hist'])) + ' (historical)', '-', '', ''],
        ['shell reads made, no equivalent (old binary)', T['n'] - T['eo'], fmt(tok(T['act'] - T['eo_hist'])) + ' (historical)', '-', '', ''],
    ]
    table(['need', 'commands', 'actual est. tokens (shell reads: rerun today)', 'counterfactual low / expected / high', 'actual round trips',
           'counterfactual commands'], rows)
    rd_calls = {u.call.id for u in rd_units}
    readonly = set()
    for k in calls:
        real = [x for x in k.units if x.kind not in ('echo', 'write') and not x.silent]
        if real and all(x.kind in ('whole', 'paged', 'search', 'listing', 'codedoc') for x in real):
            readonly.add(k.id)
    saved = saved_calls = 0
    for e in eps:
        ids = []
        for x in e['units']:
            if x.call.id not in ids:
                ids.append(x.call.id)
        saved_calls += sum(1 for i in ids[1:] if i in readonly)
        mids = []
        for x in e['units']:
            if x.call.mid not in mids:
                mids.append(x.call.mid)
        for mm in mids[1:]:
            mine = [x.call.id for x in e['units'] if x.call.mid == mm]
            allc = [k.id for k in calls if k.mid == mm]
            if all(i in readonly for i in mine) and all(i in mine for i in allc):
                saved += 1
    per_req = []
    for T in TS:
        for mid, uu in T.requests.items():
            if T.agent is None and uu:
                per_req.append(sum(int(uu.get(k) or 0) for k in ('input_tokens', 'cache_creation_input_tokens', 'cache_read_input_tokens')))
    big = [x for x in per_req if x > 0]
    main_T = max((T for T in TS if T.agent is None), key=lambda T: len(T.req_order))
    mreq = [sum(int(uu.get(k) or 0) for k in ('input_tokens', 'cache_creation_input_tokens', 'cache_read_input_tokens'))
            for uu in main_T.requests.values() if uu]
    turn_cost = statistics.mean(mreq) if mreq else 0
    P(f'Round trips: the {len(rd_units)} competing shell reads were issued in {len(rd_calls)} tool calls '
      f'({len(rd_calls & readonly)} of them contain nothing but reads/searches; the rest also build, test or edit). '
      f'The agent batches several reads per call, and it could batch codedoc commands the same way, so the '
      f'counterfactual does not change the call count except inside paging episodes: of the '
      f'{sum(e["ncalls"] for e in eps)} tool calls spanned by the {len(eps)} episodes, **{saved_calls}** are '
      f'follow-up tool calls that only read more of the same file, and **{saved}** of them were the only tool call '
      f'of their model turn; so returning the whole symbol on the first call would have avoided between {saved} '
      f'and {saved_calls} model turns (estimate: the rest shared a turn with other work). **What a round trip costs:** in session {main_T.session[:8]} the '
      f'mean request carried {fmt(turn_cost)} input tokens (measured, almost all cache reads), so each avoided '
      f'turn avoids about that many cache-read tokens (about {fmt(turn_cost * 0.1)} base-input equivalents at 0.1x), '
      f'against a median paged-read result of {fmt(tok(statistics.median(paged_sz)) if paged_sz else 0)} est. tokens; '
      f'{saved}-{saved_calls} avoided turns = about {fmt(saved * turn_cost)}-{fmt(saved_calls * turn_cost)} '
      f'cache-read tokens. In the other direction, the {len(navA)} lookups made through codedoc took '
      f'{len({u.call.id for u, r in navA})} tool calls; the expected shell route needs '
      f'{sum(r["expc"] for u, r in navA)} commands, of which the grep-then-sed pairs cannot share a call '
      f'(the range comes from the grep), i.e. up to '
      f'{sum(1 for u, r in navA if r["expc"] >= 2)} extra round trips (estimate, upper bound).\n\n')

    # ---------------- fixed overhead
    P('### 3.5 Fixed overhead codedoc adds to context\n\n')
    rows = []
    inst = glob.glob(HOME + '/.claude/plugins/cache/codedoc/codedoc/*/skills/*/SKILL.md')
    for f in sorted(inst):
        n = os.path.getsize(f)
        rows.append(['skill text, installed plugin: ' + f.split('/skills/')[1].split('/')[0], fmt(n), fmt(tok(n)),
                     'paid when the skill is loaded, then on every later turn; re-injected after each compaction'])
    for f in sorted(glob.glob(os.path.join(ARGS.plugin, 'skills', '*', 'SKILL.md')) if ARGS.plugin else []):
        n = os.path.getsize(f)
        rows.append(['skill text, --plugin tree: ' + f.split('/skills/')[1].split('/')[0], fmt(n), fmt(tok(n)), 'not installed'])
    for T in TS:
        if T.agent is None and T.listing and any(c for _, c in T.listing):
            rows.append([f'skill listing lines for codedoc skills (session {T.session[:8]})', fmt(T.listing[0][1]),
                         fmt(tok(T.listing[0][1])), f'in every request of the session; listing attached {len(T.listing)} time(s)'])
            break
    for T in TS:
        for ts, name, n in T.schemas:
            rows.append([f'MCP schema loaded via ToolSearch: {name.split("__")[-1]} ({T.session[:8]})', fmt(n), fmt(tok(n)),
                         'MCP tools are deferred: only names are in context until a schema is fetched'])
        for ts, name, n in T.reinject:
            rows.append([f'skill re-injected after compaction at {ts[11:19]}: {name}', fmt(n), fmt(tok(n)), 'measured from the transcript attachment'])
    for v in ('old', 'new'):
        tl = mcp_tools(v)
        if tl:
            n = len(json.dumps(tl))
            names = sum(len(t.get('name', '')) + 30 for t in tl)
            rows.append([f'MCP `tools/list`, codedoc-{v}: {len(tl)} tools, full schemas', fmt(n), fmt(tok(n)),
                         f'only paid in full if the client does not defer tools; deferred names alone are about {fmt(names)} chars'])
    hdrs = []
    for u in cd_units:
        if u.chunk:
            hdrs += [l for l in u.chunk.split('\n') if re.match(r'^\S+ \S+@[0-9a-f]+ dirty:\d+ fresh:', l) or l.startswith('== ')]
    if hdrs:
        rows.append(['per-answer header lines (`<repo> <branch>@<sha> dirty:N fresh:yes` and `== title (n)`)',
                     f'{statistics.mean(len(h) + 1 for h in hdrs) * 2:.0f} per answer', f'{statistics.mean(len(h) + 1 for h in hdrs) * 2 / CPT:.0f}',
                     f'{len(hdrs)} header lines seen in exactly-attributed results'])
    table(['item', 'chars', 'est. tokens', 'when it is paid'], rows)
    for T in TS:
        if T.agent is None:
            for ts, name in T.skills:
                if 'codedoc' in (name or ''):
                    after = len([m for m in T.req_order if (T.req_ts.get(m) or '') > ts])
                    f = [x for x in inst if name.split(':')[-1] in x]
                    n = os.path.getsize(f[0]) if f else 0
                    P(f'Session {T.session[:8]} loaded `{name}` at {ts[:19]}: {fmt(n)} chars ({fmt(tok(n))} est. tokens), '
                      f'carried through {after} later requests = {fmt(tok(n) * after)} est. token-turns '
                      f'(cache reads), plus {len([r for r in T.reinject if r[1] == name])} re-injections after compaction.\n\n')

    # ---------------- verbosity
    P('### 3.6 What codedoc result bytes are made of\n\n')
    verbosity(cd_units, detail, P)

    # ---------------- spot-check
    P('### 3.7 Spot-check of `symbol --body` answers\n\n')
    spot_check(cd_units, P)


def shell_equiv(u, Wm, H):
    """Measure the plain-shell equivalents of one codedoc call. None if no equivalent."""
    sub = u.sub
    src = '--include=*.ts packages scripts'
    note = []
    r = {'low': 0, 'exp': 0, 'high': 0, 'lowc': 1, 'expc': 1, 'highc': 1, 'changed': False, 'unresolved': False,
         'unf': 0, 'note': ''}
    args = None
    if u.via == 'cli':
        ws = clean_words(words_of(u.raw.split(' | ')[0])) if not u.wrapped else None
        if ws and ws[0] == 'codedoc':
            args = [w for w in ws[1:] if not w.startswith('HEREDOC')]
    if args is None:
        args = [u.sub] + ([u.addr] if u.addr else [])
    if '--out' in u.flags:
        args = [a for a in args]
        if '--out' in args:
            i = args.index('--out'); args = args[:i] + args[i + 2:]
    if sub in MAINT:
        r['unf'] = 0  # depends on the working tree at call time; not reproducible
    else:
        out, rc = cdx('old', args)
        r['unf'] = len(out)

    def L(cmd):
        return len(sh(cmd))
    if sub in ('symbol', 'callees', 'explain', 'flow', 'history', 'tests', 'at'):
        name = sym_name(u.addr)
        es = cdj('old', ['symbol', u.addr]) if u.addr else []
        loc = parse_loc(es[0].get('loc')) if es else None
        f = loc[0] if loc else addr_file(u.addr)
        if not f or not os.path.exists(os.path.join(REPO, f)) or not name:
            if name:
                r['unresolved'] = True
                g = f'grep -rnE "\\b{name}\\b" {src}'
                r['low'] = L(f'grep -rnE "(function|class|interface|type|const) {name}\\b" {src}')
                r['exp'] = L(g + f' | head -{H}')
                r['high'] = L(g)
                r['note'] = 'address has no file; grep over sources'
                return r
            return None
        body = '--body' in u.flags or sub in ('callees', 'explain', 'flow')
        g = L(f'grep -n {q(name)} {q(f)}')
        r['changed'] = bool(sh(f'git log -1 --format=%cI --since={q(u.call.ts)} -- {q(f)}').strip())
        if loc is None:
            r['unresolved'] = True
            note.append('symbol not in index now')
            r['low'] = L(f'grep -n -A 30 {q(name)} {q(f)} | head -60') if body else g
            r['exp'] = g + L(f'sed -n 1,{Wm}p {q(f)}')
            r['lowc'], r['expc'] = 1, 2
        else:
            A, B = loc[1], loc[2]
            if body:
                nw = max(1, math.ceil((B - A + 1) / Wm))
                r['low'] = g + L(f'sed -n {A},{B}p {q(f)}')
                r['exp'] = g + L(f'sed -n {A},{A + nw * Wm - 1}p {q(f)}')
                r['lowc'], r['expc'] = 2, 1 + nw
                note.append(f'{B - A + 1} lines, {nw} window(s)')
            else:
                r['low'] = g
                r['exp'] = g + L(f'sed -n {A},{A + Wm - 1}p {q(f)}')
                r['lowc'], r['expc'] = 1, 2
        if sub == 'explain':
            c = L(f'grep -rn "{name}(" {src}')
            r['low'] += c; r['exp'] += c; r['lowc'] += 1; r['expc'] += 1
        r['high'] = L(f'cat {q(f)}')
        if r['changed']:
            note.append('file changed since call')
    elif sub == 'outline' or sub == 'ls' or sub == 'lines':
        f = addr_file(u.addr) or u.addr or '.'
        full = os.path.join(REPO, f)
        if os.path.isdir(full):
            r['low'] = L(f'ls {q(f)}'); r['exp'] = L(f'ls -la {q(f)}'); r['high'] = L(f'find {q(f)} -type f | xargs wc -l')
        elif os.path.exists(full):
            r['low'] = L(f'grep -nE {q(DECL_RX)} {q(f)}')
            r['exp'] = r['low'] + L(f'sed -n 1,{Wm}p {q(f)}')
            r['expc'] = 2
            r['high'] = L(f'cat {q(f)}')
            r['changed'] = bool(sh(f'git log -1 --format=%cI --since={q(u.call.ts)} -- {q(f)}').strip())
            if r['changed']:
                note.append('file changed since call')
        else:
            return None
    elif sub in ('find', 'search', 'callers', 'refs', 'impls', 'siblings', 'supertypes', 'config'):
        qy = u.addr or ''
        name = sym_name(qy) if (' ' not in qy) else None
        if name is None:
            wsq = [w for w in re.findall(r'[A-Za-z_][A-Za-z0-9_]{2,}', qy)]
            if not wsq:
                return None
            name = max(wsq, key=len)
            note.append(f'free-text query; grep for "{name}"')
            r['low'] = L(f'grep -rlni {q(name)} {src}')
        elif sub == 'callers':
            r['low'] = L(f'grep -rn "{name}(" {src}')
        elif sub == 'refs':
            r['low'] = L(f'grep -rnw {q(name)} {src}')
        else:
            r['low'] = L(f'grep -rnE "(function|class|interface|type|const|let) {name}\\b" {src}')
        r['exp'] = L(f'grep -rn {q(name)} {src} | head -{H}')
        if sub in ('callers', 'refs'):
            files = sh(f'grep -rl {q(name)} {src}').split()
            r['high'] = sum(L(f'cat {q(x)}') for x in files[:40])
            r['highc'] = max(1, len(files[:40]))
        else:
            r['high'] = L(f'grep -rni {q(name)} . --exclude-dir=node_modules --exclude-dir=.git')
    elif sub == 'overview' or sub == 'status' or sub in ('deps', 'dependents'):
        r['low'] = L('ls; git log --oneline -5')
        r['exp'] = L('git ls-files | head -60; sed -n 1,40p README.md')
        r['high'] = L('git ls-files; cat README.md')
        note.append('measured on the repo as it is now')
    elif sub == 'changes':
        since = u.flags.get('--since')
        nxt = sh(f'git log --reverse --format=%H --since={q(u.call.ts)} | head -1').strip()
        if not nxt or not isinstance(since, str):
            return None
        rng = f'{nxt}^..{nxt}' if since == 'HEAD' else f'{since}..{nxt}'
        r['low'] = L(f'git diff --stat {rng}')
        r['exp'] = r['low'] + L(f'git diff {rng} | grep -E "^[-+].*(function|class|interface|const|type) "')
        r['high'] = L(f'git diff {rng}')
        r['expc'] = 2
        note.append(f'git diff of {rng[:7]}..; no shell equivalent for doc state')
    else:
        return None
    r['note'] = '; '.join(note)
    return r


_outline = {}


def outline(v, relpath):
    key = (v, relpath)
    if key not in _outline:
        es = cdj(v, ['outline', relpath])
        items = []
        for e in es:
            loc = parse_loc(e.get('loc'))
            if loc and e.get('key'):
                items.append((e['key'], loc[1], loc[2]))
        _outline[key] = items
    return _outline[key]


_cover = {}


def cover_keys(relpath, A, B):
    """Symbols a reader of lines A-B needs, innermost first (resolved through the new index)."""
    key = (relpath, A, B)
    if key in _cover:
        return _cover[key]
    found = {}
    step = max(1, (B - A) // 12)
    for ln in sorted(set(list(range(A, B + 1, step)) + [B, (A + B) // 2])):
        es = cdj('new', ['symbol', f'{relpath}:{ln}'])
        if not es or not es[0].get('key'):
            continue
        loc = parse_loc(es[0].get('loc'))
        if not loc:
            continue
        k = es[0]['key']
        if loc[2] - loc[1] < 3 and '.' in k.rsplit('/', 1)[-1]:
            parent = k.rsplit('.', 1)[0]
            pe = cdj('new', ['symbol', parent])
            ploc = parse_loc(pe[0].get('loc')) if pe else None
            if ploc and ploc[2] - ploc[1] <= 120:
                k, loc = parent, ploc
        found[k] = (loc[1], loc[2])
    keys = [k for k, (a, b) in found.items()
            if not any(k2 != k and a2 <= a and b <= b2 and (b2 - a2) <= 120 for k2, (a2, b2) in found.items())]
    _cover[key] = keys
    return keys


_isym = {}


def is_symbol(name):
    if name not in _isym:
        es = cdj('old', ['find', name])
        _isym[name] = [e for e in es if e.get('key')]
    return _isym[name]


def pattern_idents(pat):
    """Split a grep pattern into alternatives; return (idents, n_alternatives, decl?)."""
    p = pat or ''
    decl = bool(re.search(r'\b(function|class|interface|type|const|export|async)\b|\s=\s*$|= $', p))
    alts = [a for a in re.split(r'\\\||\|', p) if a]
    ids = []
    for a in alts:
        a2 = re.sub(r'\\[bB<>]|\^|\$|\\\(|\($|\\s[*+]?', ' ', a)
        a2 = re.sub(r'\b(export|async|function|class|interface|type|const|let|static|import|default)\b', ' ', a2)
        a2 = a2.replace('=', ' ').replace('.*', ' ').strip()
        m = re.fullmatch(r'#?[A-Za-z_][A-Za-z0-9_]*', a2)
        if m and len(a2) >= 3:
            ids.append(a2.lstrip('#'))
    return ids, len(alts), decl


def codedoc_equiv(u):
    """Measure what codedoc (old, new) would return for one shell read."""
    r = {'old': None, 'new': None, 'new_alt': None, 'old_n': 0, 'new_n': 0, 'why_old': '', 'why_new': ''}

    def none(both, why_old, why_new=None):
        r['why_old'] = why_old
        r['why_new'] = why_new or why_old
        return r
    if u.tclass == 'outside':
        return none(True, 'outside the repo (not indexed)')
    if u.tclass == 'unresolved':
        return none(True, 'path is a shell variable (not resolvable)')
    if u.tclass == 'outside-source':
        why = 'third-party source downloaded outside the repo (not indexed)'
        r['why_old'] = r['why_new'] = why
        if u.kind == 'search' and u.files and re.search(r'\.[cm]?[jt]sx?$', u.files[0] or ''):
            ids, nalt, decl = pattern_idents(u.pattern)
            tot = n = 0
            for name in ids[:5]:
                out, rc = cdx('new', ['find', name, '--deps'])
                if cd_ok(out, rc):
                    tot += len(out); n += 1
            if n:
                r['new'], r['new_n'] = tot, n
                r['dep_find'] = True
        return r
    if u.tclass == 'repo-other':
        return none(True, 'config/JSON/other non-source file (no symbols)')
    f0 = u.files[0] if u.files else None
    rp = rel(f0) if f0 else None

    def new(args):
        out, rc = cdx('new', args)
        return (len(out), 1) if cd_ok(out, rc) else (None, 0)

    def old(args):
        out, rc = cdx('old', args)
        return (len(out), 1) if cd_ok(out, rc) else (None, 0)
    if u.tclass == 'dependency':
        r['why_old'] = 'node_modules (dependencies not indexed by the old binary)'
        if u.kind == 'search':
            ids, nalt, decl = pattern_idents(u.pattern)
            tot, n = 0, 0
            for name in ids[:5]:
                c, k = new(['find', name, '--deps'])
                if c:
                    tot += c; n += k
            if n:
                r['new'], r['new_n'] = tot, n
                return r
            r['why_new'] = 'node_modules grep for text that is not a dependency symbol'
            return r
        r['why_new'] = 'node_modules file read (no measured --deps equivalent for a file range)'
        return r
    if not f0 or not os.path.exists(f0):
        return none(True, 'file or directory no longer exists')
    if u.kind == 'listing':
        r['why_old'] = '`ls`/`find`/`tree` (old binary has no `ls`; `outline <dir>` lists one directory)'
        tot, n = 0, 0
        for f in u.files[:4]:
            if os.path.exists(f):
                c, k = new(['ls', rel(f) or '.'])
                if c:
                    tot += c; n += k
        if n:
            r['new'], r['new_n'] = tot, n
        else:
            r['why_new'] = 'listing of a path with no indexed files'
        return r
    if u.tclass == 'markdown':
        r['why_old'] = 'markdown document (old binary has no document sections)'
        if os.path.isdir(f0):
            c, k = new(['ls', rp])
            if c:
                r['new'], r['new_n'] = c, k
            else:
                r['why_new'] = 'recursive grep over documents for free text'
            return r
        heads = cdx('new', ['outline', rp])
        if not cd_ok(*heads):
            r['why_new'] = 'markdown file not indexed'
            return r
        if u.kind == 'search':
            if re.search(r'#', u.pattern or ''):
                r['new'], r['new_n'] = len(heads[0]), 1
            else:
                r['why_new'] = 'free-text grep inside a document (search covers names and docs, not prose bodies)'
            return r
        if u.kind == 'whole':
            n = min(len(sh(f'cat {q(f0)}')), 30000)  # Bash output is truncated near 30k chars
            r['now'] = n
            r['new'], r['new_n'] = len(heads[0]) + n // 3, 2
            r['new_lo'], r['new_hi'] = len(heads[0]), n
            return r
        rg = resolve_range(u, f0)
        if not rg:
            r['why_new'] = 'pattern range not resolvable'
            return r
        r['now'] = len(sh(f'sed -n {rg[0]},{rg[1]}p {q(f0)}'))
        c1, k1 = new(['symbol', f'{rp}:{(rg[0] + rg[1]) // 2}', '--body'])
        c2, k2 = new(['lines', rp, f'{rg[0]}-{rg[1]}'])
        if c1 is None and c2 is None:
            r['why_new'] = 'range not addressable'
            return r
        r['new'] = c2 if c2 is not None else c1
        r['new_alt'] = c1 if c1 is not None else c2
        r['new_n'] = 1
        r['new_lo'], r['new_hi'] = min(r['new'], r['new_alt']), max(r['new'], r['new_alt'])
        r['lines_over'] = (c2 - r['now']) if c2 is not None else None
        return r
    # source / test
    if u.kind == 'whole':
        tot_o = tot_n = now = 0
        nfile = 0
        for f in u.files:
            if not os.path.isfile(f):
                continue
            o = cdx('old', ['outline', rel(f)]); n_ = cdx('new', ['outline', rel(f)])
            if not cd_ok(*o):
                continue
            nfile += 1
            now += len(sh(f'cat {q(f)}'))
            tot_o += len(o[0]); tot_n += len(n_[0])
        if not nfile:
            return none(True, 'file has no indexed symbols')
        r['now'] = now
        r['old_lo'], r['new_lo'] = tot_o, tot_n
        r['old'], r['new'] = tot_o + now // 3, tot_n + now // 3
        r['old_n'] = r['new_n'] = 2 * nfile
        r['old_hi'] = r['new_hi'] = now
        return r
    if u.kind == 'paged':
        if len(u.files) != 1 or not os.path.isfile(f0):
            return none(True, 'paged read of several files')
        rg = resolve_range(u, f0)
        if not rg:
            return none(True, 'pattern range not resolvable')
        A, B = rg
        r['now'] = len(sh(f'sed -n {A},{B}p {q(f0)}'))
        keys = cover_keys(rp, A, B)
        c1, k1 = new(['symbol', f'{rp}:{(A + B) // 2}', '--body'])
        c2, k2 = new(['lines', rp, f'{A}-{B}'])
        if keys:
            to = tn = 0
            for key in keys[:12]:
                to += len(cdx('old', ['symbol', key, '--body'])[0])
                tn += len(cdx('new', ['symbol', key, '--body'])[0])
            r['old'], r['old_n'] = to, len(keys[:12])
            r['old_hi'] = to
            mid = cdj('new', ['symbol', f'{rp}:{(A + B) // 2}'])
            r['old_lo'] = len(cdx('old', ['symbol', mid[0]['key'], '--body'])[0]) if mid and mid[0].get('key') else to
            r['new_cover'] = tn
        else:
            r['why_old'] = 'lines hold no indexed symbol (imports, file header)'
        if c1 is None and c2 is None:
            r['why_new'] = 'range not addressable'
            return r
        r['new'] = c2 if c2 is not None else c1
        r['new_alt'] = c1 if c1 is not None else c2
        r['new_n'] = 1
        r['new_lo'] = min(r['new'], r['new_alt'])
        r['new_hi'] = max(r['new'], r['new_alt'], r.get('new_cover', 0))
        r['lines_over'] = (c2 - r['now']) if c2 is not None else None
        return r
    if u.kind == 'search':
        ids, nalt, decl = pattern_idents(u.pattern)
        syms = [i for i in ids if is_symbol(i)]
        if not syms:
            return none(True, 'free-text grep (string, regex or a name that is not an indexed symbol)')
        single = len(u.files) == 1 and os.path.isfile(f0)
        tot_o = tot_n = n = 0
        for name in syms[:5]:
            if (single or decl) and u.ctx >= 3:
                key = is_symbol(name)[0]['key']
                cmds = [['symbol', key, '--body']]
            elif single or decl:
                cmds = [['find', name]]
            else:
                cmds = [['callers', name], ['refs', name]]
            for c in cmds:
                o, _ = cdx('old', c); nn, _ = cdx('new', c)
                tot_o += len(o); tot_n += len(nn); n += 1
        r['old'], r['new'], r['old_n'], r['new_n'] = tot_o, tot_n, n, n
        if len(syms) < nalt:
            r['partial'] = True
        return r
    return none(True, 'unclassified')


def mcp_tools(v):
    msgs = [{'jsonrpc': '2.0', 'id': 1, 'method': 'initialize', 'params': {'protocolVersion': '2024-11-05', 'capabilities': {}, 'clientInfo': {'name': 'usage', 'version': '0'}}},
            {'jsonrpc': '2.0', 'method': 'notifications/initialized'},
            {'jsonrpc': '2.0', 'id': 2, 'method': 'tools/list', 'params': {}}]
    data = ''.join(json.dumps(m) + '\n' for m in msgs).encode()
    try:
        p = subprocess.Popen([BIN[v], 'mcp'], cwd=REPO, env=cd_env(v),
                             stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        p.stdin.write(data); p.stdin.flush()
        tools = None
        import select
        buf = b''
        for _ in range(200):
            rd, _, _ = select.select([p.stdout], [], [], 0.1)
            if rd:
                chunk = os.read(p.stdout.fileno(), 1 << 20)
                if not chunk:
                    break
                buf += chunk
                for ln in buf.split(b'\n'):
                    try:
                        o = json.loads(ln)
                    except Exception:
                        continue
                    if o.get('id') == 2:
                        tools = (o.get('result') or {}).get('tools')
                if tools is not None:
                    break
        p.kill()
        return tools
    except Exception:
        return None


ENTRY_RX = re.compile(r'^(\S+:\S+)  (\S+:\d+(?:-\d+)?)  (.*)$')


def breakdown(text, c):
    for ln in text.split('\n'):
        n = len(ln) + 1
        if re.match(r'^\S+ \S+@[0-9a-f]+ dirty:\d+ fresh:', ln) or ln.startswith('== '):
            c['header'] += n
        elif re.match(r'^\d+\t', ln):
            c['body'] += n
        else:
            m = ENTRY_RX.match(ln)
            if m:
                key, loc, rest = m.groups()
                parts = rest.split('  ')
                tags = [p for p in parts if re.fullmatch(r'(\[[^\]]*\] ?)+', p.strip())]
                parts = [p for p in parts if p not in tags]
                sig = parts[0] if parts else ''
                doc = '  '.join(parts[1:])
                c['key'] += len(key) + 2
                c['loc'] += len(loc) + 2
                c['sig'] += len(sig) + 2
                c['doc'] += len(doc)
                c['tags'] += sum(len(t) + 2 for t in tags)
                c['entries'] += 1
                c['_docs'].append(doc)
                # the key repeats the file path already given in loc
                pth = loc.rsplit(':', 1)[0]
                c['key_redundant'] += max(0, len(key) - len(key.rsplit('/', 1)[-1]))
            else:
                c['other'] += n
    return c


def verbosity(cd_units, detail, P):
    ds = [u for u in cd_units if u.sub == 'doc set' and u.exact and u.chunk and not any(f in ('grep', 'rg', 'tail', 'cut', 'head') for f in u.filters)]
    allds = [u for u in cd_units if u.sub == 'doc set']
    if allds:
        P(f'`doc set`: {len(allds)} calls wrote {fmt(sum(u.text_in for u in allds))} chars of doc text (model output) '
          f'and got {fmt(sum(u.chars for u in allds))} chars back (est., mostly prorated). Each answer repeats the '
          f'header, the key, the location and the doc text that was just sent'
          + (f'; in the {len(ds)} unfiltered answers attributed exactly, {fmt(sum(u.chars for u in ds))} chars came '
             f'back for {fmt(sum(u.text_in for u in ds))} chars written' if ds else '') + '. '
          'The agent\'s `grep -iE "fail|not found|ambiguous|unknown"` success filter let through every answer '
          'whose echoed doc text contains one of those words.\n\n')
    for label, texts in (
        ('actual results that reached the model (exactly attributed lookup answers, after the agent\'s filters)',
         [u.chunk for u in cd_units if u.chunk and u.sub in LOOKUP]),
        ('every outline/find/callers/refs answer codedoc-old produced during this measurement, unfiltered',
         [cdx('old', key[2])[0] for key in list(_cache) if isinstance(key, tuple) and key[0] == 'cd' and key[1] == 'old'
          and key[2] and key[2][0] in LOOKUP and '--format' not in key[2] and '--body' not in key[2] and key[2][0] != 'symbol']),
        ('actual `changes`/`gaps` output rerun today, unfiltered (codedoc-old)',
         [cdx('old', a)[0] for a in (['changes', '--since', 'HEAD~3'], ['gaps'])]),
    ):
        c = collections.Counter()
        c['_docs'] = []
        for t in texts:
            breakdown(t, c)
        docs = c.pop('_docs')
        total = sum(v for k, v in c.items() if k not in ('entries', 'key_redundant'))
        if not total:
            continue
        dup = sum(len(d) for d in docs) - sum(len(d) for d in set(docs))
        P(f'**{label}** - {len(texts)} answers, {fmt(total)} chars, {c["entries"]} entry lines:\n\n')
        table(['part', 'chars', 'share'], [
            ['stable keys (`repo:lang:module:path/Name`)', fmt(c['key']), f'{100 * c["key"] / total:.0f}%'],
            ['  of which path prefix that `loc` repeats', fmt(c['key_redundant']), f'{100 * c["key_redundant"] / total:.0f}%'],
            ['location (`path:A-B`)', fmt(c['loc']), f'{100 * c["loc"] / total:.0f}%'],
            ['signature', fmt(c['sig']), f'{100 * c["sig"] / total:.0f}%'],
            ['doc text on entry lines', fmt(c['doc']), f'{100 * c["doc"] / total:.0f}%'],
            ['  of which a doc text already printed in another answer', fmt(dup), f'{100 * dup / total:.0f}%'],
            ['tags (`[test]`, `[doc:missing]`, ...)', fmt(c['tags']), f'{100 * c["tags"] / total:.0f}%'],
            ['numbered body lines', fmt(c['body']), f'{100 * c["body"] / total:.0f}%'],
            ['  doc text beyond the first 80 chars of each entry', fmt(sum(max(0, len(d) - 80) for d in docs)),
             f'{100 * sum(max(0, len(d) - 80) for d in docs) / total:.0f}%'],
            ['header lines', fmt(c['header']), f'{100 * c["header"] / total:.0f}%'],
            ['other', fmt(c['other']), f'{100 * c["other"] / total:.0f}%'],
        ])
        if docs:
            P(f'Doc text per entry: mean {statistics.mean(len(d) for d in docs):.0f}, median '
              f'{statistics.median(len(d) for d in docs):.0f}, max {max(len(d) for d in docs)} chars; mean key '
              f'length {c["key"] / max(1, c["entries"]):.0f} chars.\n\n')


def spot_check(cd_units, P):
    rows = []
    n_ok = n_bad = 0
    for u in cd_units:
        if u.sub != 'symbol' or '--body' not in u.flags or not u.chunk:
            continue
        t = u.chunk
        h = re.search(r'^\S+ \S+@([0-9a-f]+) dirty:(\d+)', t, re.M)
        m = None
        for ln in t.split('\n'):
            m = ENTRY_RX.match(ln)
            if m:
                break
        body = [(int(a), b) for a, b in re.findall(r'^(\d+)\t(.*)$', t, re.M)]
        if not h or not body:
            if re.search(r'not found', t):
                rows.append([u.call.ts[5:19], u.addr, '-', '-', 'not found', check_absent(u, h)])
            continue
        sha, dirty = h.group(1), int(h.group(2))
        path = parse_loc(m.group(2))[0] if m else addr_file(u.addr)
        src = sh(f'git show {sha}:{q(path)}').split('\n')
        how = f'git show {sha}'
        bad = [n for n, txt in body if n > len(src) or src[n - 1] != txt]
        if bad and dirty:
            how += f' (dirty:{dirty} at call time, so the working tree differed from the commit)'
        span = parse_loc(m.group(2)) if m else None
        decl_ok = ''
        if span and not bad and span[1] <= len(src):
            nm = sym_name(u.addr) or ''
            first = '\n'.join(src[span[1] - 1:span[1] + 2])
            decl_ok = 'yes' if nm and nm in first else 'NO: first lines of the span do not name the symbol'
            end = src[span[2] - 1].strip() if span[2] <= len(src) else ''
            if span[2] > span[1] and not re.search(r'[})\];]$', end):
                decl_ok += '; span end line is not a closing line: ' + end[:40]
        if bad:
            n_bad += 1
        else:
            n_ok += 1
        rows.append([u.call.ts[5:19], u.addr, f'{path}:{body[0][0]}-{body[-1][0]}', len(body),
                     'all lines match' if not bad else f'{len(bad)} lines differ (first at {bad[0]})', (decl_ok + ' ' + how).strip()])
    table(['time', 'address', 'lines shown', 'lines compared', 'verdict', 'span starts at the declaration / method'], rows[:10])
    P(f'{n_ok} of {n_ok + n_bad} `symbol --body` answers match the file at the commit named in the answer header, '
      'line for line (only the lines that survived the agent\'s `head`/`tail` filter can be compared).\n\n')


def check_absent(u, h):
    f = addr_file(u.addr)
    nm = sym_name(u.addr)
    if not (f and nm and h):
        return ''
    out = sh(f'git show {h.group(1)}:{q(f)} | grep -nE "(function|class|const|interface|type) {nm}\\b" | head -3')
    return ('WRONG: the file declared it: ' + out.strip()[:80]) if out.strip() else f'correct: no declaration of `{nm}` in {f} at {h.group(1)}'


if __name__ == '__main__':
    configure(sys.argv[1:])
    main()
