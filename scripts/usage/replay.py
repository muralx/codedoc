#!/usr/bin/env python3
"""Replay recorded codedoc lookups and servable shell reads against two binaries.

Run (standard library only):
  python3 -I scripts/usage/replay.py --repo DIR --old BIN [--new BIN] [options]

  --repo DIR         repository the transcripts are about (commands run there).
  --project DIR      transcript directory (default: Claude Code's directory
                     for --repo under ~/.claude/projects).
  --workspace NAME   workspace name in the generated config (default: repo basename).
  --old BIN          the previous build. Alone, it records a baseline.
  --new BIN          the build under test.
  --old-profile P    shell read -> command mapping for --old (default v1).
  --new-profile P    the same for --new (default v2). Profiles: profiles.py.
  --since/--until    ISO UTC bounds on the tool calls replayed (until exclusive).
  --env-dir DIR      parent of the isolated environments, DIR/old and DIR/new
                     (default: a fresh temp dir). The user's index is never touched.
  --json FILE        write every measured item (command, argv, bytes, exit
                     status, shell bytes) and the index facts, for later comparison.
  --baseline FILE    load the `old` side from such a file instead of running
                     --old; its index facts are reused so both sides judge
                     "servable" identically.
  --plugin DIR       plugin tree whose skills and agents are sized
                     (default: plugin/ of the repo holding this script).
  --details          also print one row per shell-read job.

Each binary gets its own index (`<bin> index`, once), then runs with cwd =
--repo, text output and the default budget. Bytes = stdout + stderr.

Part A reruns the recorded codedoc lookups unfiltered. Part B maps each shell
read (whole, paged, search, listing) to codedoc commands through a profile;
shell bytes are the same read rerun today for paged and whole reads, the
historical result size for searches and listings. Part C is the fixed cost.
The last block prints the acceptance numbers of the efficiency spec, section 10.
A command that fails or times out still counts its bytes and is flagged.
"""
import argparse
import collections
import datetime
import glob
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))  # python3 -I leaves the script dir out
sys.dont_write_bytecode = True  # no __pycache__ in the repo

import profiles  # noqa: E402
import replay_lib as lib  # noqa: E402
import usage  # noqa: E402

fmt, tok, table = usage.fmt, usage.tok, usage.table
P = sys.stdout.write


def parse_args(argv):
    """Parse the command line (see the module docstring)."""
    ap = argparse.ArgumentParser(description=__doc__.split('\n')[0])
    ap.add_argument('--repo', required=True)
    ap.add_argument('--project')
    ap.add_argument('--workspace')
    ap.add_argument('--old', metavar='BIN')
    ap.add_argument('--new', metavar='BIN')
    ap.add_argument('--old-profile', default='v1', choices=sorted(profiles.PROFILES))
    ap.add_argument('--new-profile', default='v2', choices=sorted(profiles.PROFILES))
    ap.add_argument('--since')
    ap.add_argument('--until')
    ap.add_argument('--env-dir')
    ap.add_argument('--json', metavar='FILE')
    ap.add_argument('--baseline', metavar='FILE')
    ap.add_argument('--plugin', metavar='DIR',
                    default=os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))), 'plugin'))
    ap.add_argument('--details', action='store_true')
    a = ap.parse_args(argv)
    if not (a.old or a.baseline):
        ap.error('give --old BIN or --baseline FILE')
    return a


def configure_usage(a):
    """Hand the shared options to usage.py, whose loaders and classifiers read module globals."""
    argv = ['--repo', a.repo, '--no-measure']
    for flag, val in (('--project', a.project), ('--workspace', a.workspace), ('--since', a.since),
                      ('--until', a.until), ('--env-dir', a.env_dir), ('--new', a.new),
                      ('--old', None if a.baseline else a.old)):
        if val:
            argv += [flag, val]
    usage.configure(argv)


def load_baseline(path):
    """Read a --json file; return (old side result, saved facts) or exit with a message."""
    try:
        with open(path) as fh:
            doc = json.load(fh)
        return doc['sides']['old'], doc.get('facts') or {}
    except (OSError, ValueError, KeyError) as e:
        sys.exit('replay: cannot use baseline %s: %s' % (path, e))


def measure(a):
    """Load the transcripts, plan and run both sides. Returns (sides, facts, context for the report)."""
    ts_all = usage.load_all()
    units = [u for T in ts_all for k in T.calls if usage.in_scope(k) for u in k.units]
    dead = usage.find_dead_ends(ts_all, usage.in_scope)
    old_res, saved = load_baseline(a.baseline) if a.baseline else (None, {})
    todo = [(lbl, b, p) for lbl, b, p in (('old', None if a.baseline else a.old, a.old_profile),
                                          ('new', a.new, a.new_profile)) if b]
    side_objs = {lbl: lib.Side(lbl, b) for lbl, b, _ in todo}
    for s in side_objs.values():
        s.index()
    facts = lib.Facts(next(iter(side_objs.values()), None), saved)
    lookups = lib.recorded_lookups(units, dead)
    sides = collections.OrderedDict([('old', old_res)] if old_res else [])
    for lbl, _, pname in todo:
        prof = profiles.PROFILES[pname](facts.symbol_key)
        jobs, unserved = lib.plan_reads(units, prof, facts)
        sides[lbl] = lib.run_side(side_objs[lbl], prof, lookups, jobs, unserved)
    reads = [u for u in units if u.kind in lib.READ_KINDS]
    ctx = {'reads': len(reads), 'read_calls': len({u.call.id for u in reads}),
           'docset': [u for u in units if u.kind == 'codedoc' and u.sub == 'doc set'],
           'other_dead': sum(1 for d in dead if d['why'] in lib.DEAD_KINDS and not lib.is_lookup(d['unit']))}
    return sides, facts, ctx


def pct(new, old):
    """Signed percentage change, '-' when there is no base."""
    return '%+.1f%%' % (100.0 * (new - old) / old) if old else '-'


def part_a(sides):
    """Print the per-lookup table, totals and the recorded dead ends. Returns the acceptance inputs."""
    P('## A. Recorded codedoc lookups, rerun unfiltered\n\n')
    labels = list(sides)
    by = {l: {e['id']: e for e in sides[l]['lookups']} for l in labels}
    ids = [e['id'] for e in sides[labels[-1]]['lookups']]
    rows = []
    for i in ids:
        e = by[labels[-1]][i]
        r = [e['ts'][5:19], '`' + e['cmd'][:90] + '`', fmt(e['recorded'])]
        r += [fmt(by[l][i]['bytes']) if i in by[l] else '-' for l in labels]
        r += [' / '.join(str(by[l][i]['exit']) if i in by[l] else '-' for l in labels), e['dead'],
              '; '.join('%s: %s' % (l, by[l][i]['flag']) for l in labels if i in by[l] and by[l][i]['flag'])]
        rows.append(r)
    table(['time', 'command', 'recorded result (chars, after the agent\'s filter)'] + ['bytes ' + l for l in labels]
          + ['exit ' + '/'.join(labels), 'recorded dead end', 'flag'], rows)
    common = [i for i in ids if all(i in by[l] for l in labels)]
    tot = {l: sum(by[l][i]['bytes'] for i in common) for l in labels}
    P('Totals over %d lookups: ' % len(common) + '; '.join('%s %s bytes (%s est. tokens)' % (l, fmt(tot[l]), fmt(tok(tot[l])))
                                                           for l in labels))
    change = None
    if len(labels) == 2:
        change = 100.0 * (tot['new'] - tot['old']) / tot['old'] if tot['old'] else None
        P('; change %s' % pct(tot['new'], tot['old']))
        if len(common) != len(ids):
            P(' (%d lookups are missing from the baseline and left out)' % (len(ids) - len(common)))
    P('.\n\n### Recorded dead ends\n\n')
    dead = [i for i in ids if by[labels[-1]][i]['dead']]
    rows = []
    for i in dead:
        e = by[labels[-1]][i]
        r = [e['ts'][5:19], '`' + e['cmd'][:90] + '`', e['dead']]
        for l in labels:
            x = by[l].get(i)
            r += [x['exit'], fmt(x['bytes']), 'yes' if x['answers'] else 'no'] if x else ['-', '-', '-']
        rows.append(r)
    table(['time', 'command', 'recorded as'] + [h + l for l in labels for h in ('exit ', 'bytes ', 'answers ')], rows)
    P('"answers" = exit 0, at least one `path:line` location in the output, and no line starting with `codedoc: `.\n\n')
    last = by[labels[-1]]
    first = by[labels[0]]
    lost = sum(1 for i in common if first[i]['exit'] == 0 and last[i]['exit'] != 0) if len(labels) == 2 else 0
    return {'change': change, 'lost': lost, 'dead': len(dead), 'dead_ok': sum(1 for i in dead if last[i]['answers'])}


def job_cost(j, variant):
    """(bytes, commands, flagged commands) of a job under a whole-file variant ('strict' or 'outline')."""
    cmds = j['outline'] if variant == 'outline' and j.get('outline') is not None else j['cmds']
    return sum(c['bytes'] for c in cmds), len(cmds), sum(1 for c in cmds if c['flag'])


def side_totals(side):
    """Totals of one side's jobs: reads, shell bytes/commands and codedoc bytes/commands per variant."""
    t = collections.Counter()
    for j in side['jobs']:
        t['reads'] += j['reads']; t['shell'] += j['shell_bytes']; t['shell_cmds'] += j['shell_cmds']
        for v in ('strict', 'outline'):
            b, n, f = job_cost(j, v)
            t[v] += b; t[v + '_cmds'] += n; t[v + '_flag'] += f
    return t


def part_b(sides, ctx, details):
    """Print the shell-read tables per side, the totals and the unservable reasons. Returns totals per side."""
    P('## B. Servable shell reads, replayed through codedoc\n\n')
    P('%d shell reads in scope, issued in %d tool calls. Whole-file reads have two variants: **strict** = every '
      'line of the file (the information `cat` gave), **outline** = the file outline; other kinds are the same in '
      'both.\n\n' % (ctx['reads'], ctx['read_calls']))
    totals = {}
    for l, side in sides.items():
        P('### %s: `%s`, profile %s\n\n' % (l, os.path.basename(side['binary']), side['profile']))
        groups = collections.defaultdict(list)
        for j in side['jobs']:
            groups[(j['kind'], j['tclass'])].append(j)
        rows = []
        for (kd, tc), js in sorted(groups.items(), key=lambda kv: -sum(j['shell_bytes'] for j in kv[1])):
            c = [job_cost(j, 'strict') for j in js]
            o = [job_cost(j, 'outline') for j in js]
            rows.append([kd, tc, sum(j['reads'] for j in js), fmt(sum(j['shell_bytes'] for j in js)),
                         sum(j['shell_cmds'] for j in js), fmt(sum(x[0] for x in c)), sum(x[1] for x in c),
                         fmt(sum(x[0] for x in o)) if kd == 'whole' else '', sum(x[1] for x in o) if kd == 'whole' else '',
                         sum(x[2] for x in c) or ''])
        t = totals[l] = side_totals(side)
        rows.append(['**total**', '', t['reads'], fmt(t['shell']), t['shell_cmds'], fmt(t['strict']), t['strict_cmds'],
                     fmt(t['outline']), t['outline_cmds'], t['strict_flag'] or ''])
        table(['kind', 'target', 'reads', 'shell bytes', 'shell commands', 'codedoc bytes', 'codedoc commands',
               'outline variant: bytes', 'outline variant: commands', 'failed or flagged commands'], rows)
        if details:
            table(['kind', 'target', 'reads', 'shell bytes', 'codedoc bytes', 'commands', 'flag'],
                  [[j['kind'], j['target'], j['reads'], fmt(j['shell_bytes']), fmt(job_cost(j, 'strict')[0]),
                    ' ; '.join('`' + ' '.join(c['argv'])[:80] + '`' for c in j['cmds']),
                    '; '.join(sorted({c['flag'] for c in j['cmds'] if c['flag']}))] for j in side['jobs']])
        flagged = collections.Counter('%s (`%s ...`)' % (c['flag'], c['argv'][0]) for j in side['jobs']
                                      for c in j['cmds'] + (j['outline'] or []) if c['flag'])
        if flagged:
            P('Flagged: ' + ', '.join('%s x%d' % kv for kv in flagged.most_common()) + '. Their bytes are counted.\n\n')
        allc = [c for j in side['jobs'] for c in j['cmds'] + (j['outline'] or [])]
        P('Output budget: %d answers were cut by it. Paged and strict whole-file reads follow the printed '
          '`continue with codedoc ...` command until every line is returned: %d continuation commands, counted '
          'above as commands and bytes. Cut answers of other kinds (%d) are counted as returned.\n\n'
          % (sum(1 for c in allc if c.get('cut')), sum(1 for c in allc if c.get('cont')),
             sum(1 for j in side['jobs'] for c in (j['cmds'] if j['kind'] not in ('paged', 'whole') else j['outline'] or [])
                 if c.get('cut'))))
    rows = []
    for l, t in totals.items():
        for v in ('strict', 'outline'):
            rows.append([l, v, t['reads'], fmt(t['shell']), fmt(t[v]), pct(t[v], t['shell']), t['shell_cmds'],
                         t[v + '_cmds'], pct(t[v + '_cmds'], t['shell_cmds']), fmt(tok(t['shell'])), fmt(tok(t[v]))])
    P('### Totals\n\n')
    table(['binary', 'whole-file variant', 'reads served', 'shell bytes', 'codedoc bytes', 'bytes vs shell',
           'shell commands', 'codedoc commands', 'commands vs shell', 'shell est. tokens', 'codedoc est. tokens'], rows)
    P('### Reads that are not servable, by reason\n\n')
    why = collections.OrderedDict()
    for l, side in sides.items():
        for x in side['unserved']:
            w = why.setdefault(x['why'], {k: [0, 0] for k in sides})
            w[l][0] += 1; w[l][1] += x['bytes']
    rows = [[w] + [c for l in sides for c in (v[l][0], fmt(v[l][1]))]
            for w, v in sorted(why.items(), key=lambda kv: -max(x[1] for x in kv[1].values()))]
    table(['reason'] + [h % l for l in sides for h in ('%s: reads', '%s: bytes (historical)')], rows)
    return totals


def part_c(sides, ctx, plugin):
    """Print the fixed cost: skills, agents, MCP tools/list, recorded doc set. Returns skill tokens by name."""
    P('## C. Fixed cost\n\n')
    rows, skills = [], {}
    files = sorted(glob.glob(os.path.join(plugin, 'skills', '*', 'SKILL.md'))) + sorted(glob.glob(os.path.join(plugin, 'agents', '*.md')))
    for f in files:
        try:
            with open(f, 'rb') as fh:
                data = fh.read()
        except OSError:
            continue
        chars = len(data.decode('utf-8', 'replace'))
        name = os.path.relpath(f, plugin)
        skills[name] = tok(chars)
        rows.append([name, fmt(len(data)), data.count(b'\n'), fmt(tok(chars))])
    for l, side in sides.items():
        tl = side.get('tools_list')
        rows.append(['MCP `tools/list`, %s' % l, fmt(tl['bytes']) if tl else 'no answer',
                     '%d tools' % tl['tools'] if tl else '', fmt(tok(tl['bytes'])) if tl else ''])
    table(['item (plugin tree: `%s`)' % plugin, 'bytes', 'lines', 'est. tokens (chars/4)'], rows)
    ds = ctx['docset']
    P('Recorded `doc set`: %d calls, %s chars of doc text written, %s chars returned to the model (est., prorated '
      'over mixed Bash results). The bytes a successful `doc set` prints are measured separately, not replayed '
      '(a replay would write to the repository).\n\n' % (len(ds), fmt(sum(u.text_in for u in ds)), fmt(sum(u.chars for u in ds))))
    return skills


def acceptance(sides, a_res, totals, skills, ctx):
    """Print the five acceptance numbers of the spec with PASS/FAIL where this run can compute them."""
    def verdict(ok):
        return 'n/a' if ok is None else 'PASS' if ok else 'FAIL'
    last = list(sides)[-1]
    t = totals[last]
    rows = [['1. replayed lookups: bytes, new vs old (need <= -30%, and no lookup that worked on old failing on new)',
             '%+.1f%%; %d lookups stopped working' % (a_res['change'], a_res['lost']) if a_res['change'] is not None
             else 'needs --old/--baseline and --new',
             verdict(None if a_res['change'] is None else a_res['change'] <= -30.0 and not a_res['lost'])]]
    for v in ('strict', 'outline'):
        rows.append(['2. shell reads on `%s`, whole files as %s: bytes and commands vs shell (need both lower, none failing)' % (last, v),
                     '%s vs %s bytes (%s); %d vs %d commands; %d commands failed or flagged'
                     % (fmt(t[v]), fmt(t['shell']), pct(t[v], t['shell']), t[v + '_cmds'], t['shell_cmds'], t[v + '_flag']),
                     verdict(t[v] < t['shell'] and t[v + '_cmds'] < t['shell_cmds'] and not t[v + '_flag'])])
    rows.append(['3. `doc set` on success (need 0 bytes)', 'not replayed; %d recorded calls' % len(ctx['docset']), 'n/a'])
    rows.append(['4. recorded dead ends answering on `%s` (need all)' % last, '%d of %d' % (a_res['dead_ok'], a_res['dead']),
                 verdict(a_res['dead_ok'] == a_res['dead'])])
    need = [n for n in skills if n.split(os.sep)[:2] in (['skills', 'codedoc-lookup'], ['skills', 'codedoc-document'])]
    total = sum(skills[n] for n in need)
    rows.append(['5. skills lookup + document, est. tokens (need < 1,200)', fmt(total) if len(need) >= 2 else 'skills not found',
                 verdict(total < 1200 if len(need) >= 2 else None)])
    P('## Acceptance (spec section 10)\n\n')
    table(['criterion', 'measured', 'verdict'], rows)
    if ctx['other_dead']:
        P('%d recorded dead ends were not lookups (`doc set`, `changes`) and are not replayed.\n\n' % ctx['other_dead'])


def main(argv):
    """Entry point: measure, optionally save, print the report."""
    a = parse_args(argv)
    configure_usage(a)
    sides, facts, ctx = measure(a)
    head = usage.sh('git rev-parse --short HEAD').strip()
    if a.json:
        doc = {'format': 1, 'generated': datetime.datetime.now(datetime.timezone.utc).isoformat(timespec='seconds'),
               'repo': usage.REPO, 'head': head, 'since': a.since, 'until': a.until, 'facts': facts.saved, 'sides': sides}
        os.makedirs(os.path.dirname(os.path.abspath(a.json)), exist_ok=True)
        with open(a.json, 'w') as fh:
            json.dump(doc, fh, indent=1)
    P('# codedoc replay on %s\n\n' % os.path.basename(usage.REPO))
    P('Transcripts `%s`%s%s, replayed against the repo at `%s`. Token estimates are chars / %g. '
      % (usage.PROJECT, ' since ' + a.since if a.since else '', ' until ' + a.until if a.until else '', head, usage.CPT))
    P('; '.join('**%s** = `%s` (profile %s%s, index: exit %s%s)'
                % (l, s['binary'], s['profile'], ', loaded from %s' % a.baseline if l == 'old' and a.baseline else '',
                   s['index']['exit'], ', ' + s['index']['flag'] if s['index']['flag'] else '')
                for l, s in sides.items()) + '.\n\n')
    a_res = part_a(sides)
    totals = part_b(sides, ctx, a.details)
    skills = part_c(sides, ctx, a.plugin)
    acceptance(sides, a_res, totals, skills, ctx)


if __name__ == '__main__':
    main(sys.argv[1:])
