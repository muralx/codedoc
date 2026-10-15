package docs

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Candidate is a symbol or module considered for the gaps list.
type Candidate struct {
	Key       string // stable key; for a module its repo:dir/ address
	Module    string // module identity; groups an unattended run
	Path      string // repo-relative file, or the module directory
	Line      int
	Kind      string // symbol kind, or "module"
	Signature string
	// Visibility is public, package or private; modules count as public.
	Visibility string
	State      State
	FanIn      int
	// ChangedAt is the last commit time of the file (newest file of a
	// module), gitmeta.Uncommitted for an uncommitted change, 0 unknown.
	ChangedAt int64
	Lines     int
	// Files counts the files of a module; it weighs a module by size.
	Files int
	// Test, Generated and Trivial exclude a candidate, and so do
	// DocumentedInterface (a method of an interface that has a doc) and
	// Synthetic (a symbol the language implies, such as a Java record
	// accessor, which has no source of its own to document).
	Test, Generated, Trivial, DocumentedInterface, Synthetic bool
	RepoID                                                   int64
}

// Gap is a ranked candidate.
type Gap struct {
	Candidate
	Score  float64
	Reason string // e.g. "public, fan-in 7, changed 3d ago"
}

// recentDays is how recent a change must be to raise a score.
const recentDays = 30

// Rank scores the candidates that are not excluded (spec 11.1) and
// orders them: stale before missing, then by score descending, then by
// key. now dates the recency factor.
func Rank(cands []Candidate, now time.Time) []Gap {
	var out []Gap
	for _, c := range cands {
		if c.State == OK || excluded(c) {
			continue
		}
		out = append(out, Gap{Candidate: c, Score: Score(c, now), Reason: reason(c, now)})
	}
	return Rerank(out)
}

// excluded applies the exclusions of spec 11.1: tests (test symbols,
// and anything in a test file or directory), fixtures, examples and
// vendored code, generated files, trivial accessors, synthetic symbols,
// symbols shorter than three lines unless public, and methods of
// documented interfaces.
func excluded(c Candidate) bool {
	if IsTestPath(c.Path) || isNoisePath(c.Path) {
		return true
	}
	if c.Kind == "module" {
		return false
	}
	return c.Test || c.Generated || c.Trivial || c.DocumentedInterface || c.Synthetic ||
		(c.Lines < 3 && c.Visibility != "public")
}

// noiseDirs are directories whose code is no documentation work: test
// fixtures, examples and vendored or third-party code.
var noiseDirs = map[string]bool{"testdata": true, "fixtures": true, "examples": true, "vendor": true,
	"node_modules": true, "third_party": true}

// isNoisePath reports whether a repo-relative path lies in a noiseDirs
// directory.
func isNoisePath(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if noiseDirs[seg] {
			return true
		}
	}
	return false
}

// Score is visibility × (1 + ln(1 + fan-in)) × kind weight × recency.
func Score(c Candidate, now time.Time) float64 {
	return visibilityWeight(c) * (1 + math.Log(1+float64(c.FanIn))) * kindWeight(c) * recency(c, now)
}

func visibilityWeight(c Candidate) float64 {
	switch {
	case c.Kind == "module" || c.Visibility == "public":
		return 3
	case c.Visibility == "package":
		return 1.5
	}
	return 1
}

// maxModuleWeight caps the kind weight of a module.
const maxModuleWeight = 4

// kindWeight weighs a candidate by kind; a module weighs 1 + ln(1 +
// files), at most maxModuleWeight, so a one-file package does not
// outrank a busy interface.
func kindWeight(c Candidate) float64 {
	switch c.Kind {
	case "module":
		return min(maxModuleWeight, 1+math.Log(1+float64(c.Files)))
	case "interface", "trait":
		return 3
	case "class", "struct":
		return 2
	case "field":
		return 0.5
	}
	return 1
}

func recency(c Candidate, now time.Time) float64 {
	if c.ChangedAt > 0 && (c.ChangedAt == math.MaxInt64 || now.Unix()-c.ChangedAt <= recentDays*24*3600) {
		return 1.5
	}
	return 1
}

// reason explains a score: visibility, fan-in and the last change.
func reason(c Candidate, now time.Time) string {
	vis := c.Visibility
	if c.Kind == "module" {
		vis = "module"
	}
	parts := []string{vis, fmt.Sprintf("fan-in %d", c.FanIn)}
	switch {
	case c.ChangedAt == math.MaxInt64:
		parts = append(parts, "uncommitted change")
	case c.ChangedAt > 0:
		parts = append(parts, "changed "+ago(now.Sub(time.Unix(c.ChangedAt, 0))))
	}
	return strings.Join(parts, ", ")
}

// ago renders a duration in whole days: "today" under a day, else
// "Nd ago".
func ago(d time.Duration) string {
	if d < 24*time.Hour {
		return "today"
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

var (
	trivialReturn = regexp.MustCompile(`^return\s*\(?\s*((this|self|[A-Za-z_]\w*)\.)?[A-Za-z_]\w*\s*\)?;?$`)
	trivialAssign = regexp.MustCompile(`^(this|self|[A-Za-z_]\w*)\.[A-Za-z_]\w*\s*=\s*[A-Za-z_]\w*;?$`)
)

// TrivialBody reports whether a function's source (signature and body)
// is a trivial accessor: its body is a single return of a field (or a
// name) or a single assignment of a name to a field. Python bodies
// follow the colon of the def line; braced bodies sit between the first
// '{' after the signature and the last '}'.
func TrivialBody(lang, src string) bool {
	var body string
	if lang == "python" {
		_, after, ok := strings.Cut(src, ":\n")
		if !ok {
			_, after, ok = strings.Cut(src, ":")
		}
		if !ok {
			return false
		}
		body = after
	} else {
		i, j := strings.Index(src, "{"), strings.LastIndex(src, "}")
		if i < 0 || j <= i {
			return false
		}
		body = src[i+1 : j]
	}
	body = strings.Join(strings.Fields(body), " ")
	return trivialReturn.MatchString(body) || trivialAssign.MatchString(body)
}

// Rerank orders gaps ranked separately (one list per repo) as Rank
// would have ranked them together.
func Rerank(gaps []Gap) []Gap {
	sort.SliceStable(gaps, func(i, j int) bool { return before(gaps[i], gaps[j]) })
	return gaps
}

// before is the gap order: stale first, then by score descending, then
// by key.
func before(a, b Gap) bool {
	if (a.State == Stale) != (b.State == Stale) {
		return a.State == Stale
	}
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.Key < b.Key
}

// IsTestPath reports whether a repo-relative file or directory is test
// code by the conventions of spec 7.2: Go _test.go files, TS/JS
// *.test.* and *.spec.* files and __tests__ directories, Python
// test_*.py and *_test.py files and tests directories, and Java
// src/test trees.
func IsTestPath(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == "tests" || seg == "__tests__" {
			return true
		}
	}
	base := p[strings.LastIndex(p, "/")+1:]
	switch {
	case strings.HasSuffix(base, "_test.go"), strings.HasSuffix(base, "_test.py"),
		strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py"),
		strings.Contains(base, ".test."), strings.Contains(base, ".spec."):
		return true
	}
	return strings.HasPrefix(p, "src/test/") || strings.Contains(p, "/src/test/")
}
