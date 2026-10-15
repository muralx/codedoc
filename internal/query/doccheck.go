package query

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/store"
)

// DocCheck brings the repos in scope fully up to date (see
// EnsureSynced: no file limit), then reports per repo the
// ok, stale and missing counts of symbols (tests excluded) and modules,
// and the docs whose placement or form is wrong: Go docs that do not
// start with the symbol name (warnings), Javadoc blocks detached from
// their declaration by a blank line, and Python docstrings in single
// quotes. Problems are part of the answer, never an error.
func DocCheck(c *Ctx) (*output.Response, error) {
	repos := c.scope()
	if err := c.EnsureSynced(repos...); err != nil {
		return nil, err
	}
	var counts, problems []output.Entry
	for _, r := range repos {
		id, err := c.repoID(r)
		if err != nil {
			return nil, err
		}
		if id == 0 {
			continue
		}
		n, err := c.Store.RepoDocCounts(id)
		if err != nil {
			return nil, err
		}
		counts = append(counts, countEntry(r.Name, n))
		more, err := c.docProblems(r, id)
		if err != nil {
			return nil, err
		}
		problems = append(problems, more...)
	}
	return c.respond(repos, c.tagged("doc states", counts), c.tagged("doc problems", problems)), nil
}

func countEntry(repo string, n store.DocCounts) output.Entry {
	return output.Entry{Key: repo, Text: fmt.Sprintf("symbols ok:%d stale:%d missing:%d  modules ok:%d missing:%d",
		n.Symbols["ok"], n.Symbols["stale"], n.Symbols["missing"], n.Modules["ok"], n.Modules["missing"]),
		Extra: map[string]any{"symbols": n.Symbols, "modules": n.Modules}}
}

// docProblems runs the per-language checks over one repo.
func (c *Ctx) docProblems(r *config.Repo, repoID int64) ([]output.Entry, error) {
	var out []output.Entry
	for lang, check := range map[string]func(*sourceCache, store.Symbol) string{
		parse.Go: goDocProblem, parse.Java: javadocProblem, parse.Python: docstringProblem,
	} {
		syms, err := c.Store.RepoSymbols(repoID, lang)
		if err != nil {
			return nil, err
		}
		src := &sourceCache{root: r.Path, files: map[string][]string{}}
		for _, sy := range syms {
			if msg := check(src, sy); msg != "" {
				out = append(out, problemEntry(sy, msg))
			}
		}
	}
	mods, err := c.pyModuleProblems(r, repoID)
	out = append(out, mods...)
	sortEntries(out)
	return out, err
}

func problemEntry(sy store.Symbol, msg string) output.Entry {
	e := symbolEntry(sy)
	e.Text, e.Doc = msg, ""
	e.Tags = []string{"problem"}
	if strings.HasPrefix(msg, "go doc") {
		e.Tags = []string{"warning"}
	}
	return e
}

// sortEntries orders problem entries by repo, then location.
func sortEntries(es []output.Entry) {
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].RepoID != es[j].RepoID {
			return es[i].RepoID < es[j].RepoID
		}
		return es[i].Loc < es[j].Loc
	})
}

// sourceCache reads the lines of a repo's files once.
type sourceCache struct {
	root  string
	files map[string][]string
}

// lines returns a file's lines, none when it cannot be read.
func (s *sourceCache) lines(p string) []string {
	if l, ok := s.files[p]; ok {
		return l
	}
	b, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(p)))
	var l []string
	if err == nil {
		l = strings.Split(string(b), "\n")
	}
	s.files[p] = l
	return l
}

// goDocProblem reports a Go doc of a top-level exported declaration
// that does not start with the symbol's name (optionally after A, An or
// The), the convention go doc and linters check; methods, fields and
// unexported declarations are free.
func goDocProblem(_ *sourceCache, sy store.Symbol) string {
	words := strings.Fields(sy.DocText)
	if len(words) == 0 || sy.IsTest || !goTopLevelExported(sy) {
		return ""
	}
	first := strings.TrimRight(words[0], ".,:;")
	if first == sy.Name || (len(words) > 1 && (first == "A" || first == "An" || first == "The") &&
		strings.TrimRight(words[1], ".,:;") == sy.Name) {
		return ""
	}
	return "go doc does not start with " + sy.Name
}

// goTopLevelExported reports whether a Go symbol is an exported
// declaration at the top level of its file.
func goTopLevelExported(sy store.Symbol) bool {
	return sy.ContainerID == 0 && sy.Kind != "method" && sy.Visibility == "public"
}

// javadocProblem reports a Java symbol whose /** block a blank line
// separates from the declaration (the extractor attaches it anyway).
func javadocProblem(src *sourceCache, sy store.Symbol) string {
	if sy.DocText == "" {
		return ""
	}
	lines := src.lines(sy.Path)
	for l := sy.DocEndLine + 1; l < sy.StartLine && l <= len(lines); l++ {
		if strings.TrimSpace(lines[l-1]) == "" {
			return "javadoc detached from the declaration by a blank line"
		}
	}
	return ""
}

// docstringProblem reports a Python docstring written with single
// quotes.
func docstringProblem(src *sourceCache, sy store.Symbol) string {
	if sy.DocText == "" {
		return ""
	}
	lines := src.lines(sy.Path)
	if sy.DocStartLine < 1 || sy.DocStartLine > len(lines) || !singleQuoted(lines[sy.DocStartLine-1]) {
		return ""
	}
	return "docstring uses single quotes; use triple double quotes"
}

// singleQuoted reports whether a line starts a string literal in single
// quotes, after an optional r, u or b prefix.
func singleQuoted(line string) bool {
	t := strings.TrimLeft(strings.TrimSpace(line), "rRuUbB")
	return strings.HasPrefix(t, "'")
}

// pyModuleProblems reports Python module docstrings in single quotes.
func (c *Ctx) pyModuleProblems(r *config.Repo, repoID int64) ([]output.Entry, error) {
	mods, err := c.Store.ModulesByRepo(repoID)
	if err != nil {
		return nil, err
	}
	src := &sourceCache{root: r.Path, files: map[string][]string{}}
	var out []output.Entry
	for _, m := range mods {
		if m.Lang != "py" || m.DocSource == "" || !singleQuoted(firstCode(src.lines(m.DocSource))) {
			continue
		}
		out = append(out, output.Entry{Key: m.Identity, Loc: r.Name + ":" + m.DocSource + ":1",
			Text: "module docstring uses single quotes; use triple double quotes", Tags: []string{"problem"}})
	}
	return out, nil
}

// firstCode returns the first line that is neither blank nor a comment.
func firstCode(lines []string) string {
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") {
			return t
		}
	}
	return ""
}
