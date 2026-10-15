package resolve_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/resolve"
	"github.com/muralx/codedoc/internal/store"
)

// probe matches the edge a line of a scope fixture must yield: the name
// called, the confidence tier and the targets, joined by '|'; "none -"
// for a call that must yield no edge.
var probe = regexp.MustCompile(`// => (\w+) (\w+) (\S+)`)

// scopeFiles reads the fixture testdata/<dir> as repo files.
func scopeFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	root := filepath.Join("testdata", filepath.FromSlash(dir))
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		files[filepath.ToSlash(rel)] = string(raw)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// callsAt returns, by "line name", the call edges leaving the symbols of
// a file as "tier targets": the targets are qualified names without
// their file prefix, sorted and joined by '|'.
func callsAt(t *testing.T, s *store.Store, path string) map[string]string {
	t.Helper()
	repos, err := s.Repos()
	if err != nil || len(repos) != 1 {
		t.Fatalf("repos: %v %v", repos, err)
	}
	f, err := s.FileByPath(repos[0].ID, path)
	if err != nil {
		t.Fatal(err)
	}
	syms, err := s.SymbolsInFile(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, sy := range syms {
		edges, err := s.EdgesFrom(sy.ID, resolve.EdgeCalls)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range edges {
			var names []string
			for _, target := range strings.Split(callTarget(t, s, e), "|") {
				names = append(names, target[strings.LastIndex(target, "/")+1:])
			}
			sort.Strings(names)
			name := names[0][strings.LastIndex(names[0], ".")+1:]
			out[strconv.Itoa(e.Line)+" "+name] = e.Confidence + " " + strings.Join(names, "|")
		}
	}
	return out
}

// checkProbes compares every "// => name tier targets" line of a fixture
// file with the call edge the index holds for it.
func checkProbes(t *testing.T, s *store.Store, path, src string) {
	t.Helper()
	got := callsAt(t, s, path)
	probes := 0
	for i, line := range strings.Split(src, "\n") {
		m := probe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		probes++
		key := strconv.Itoa(i+1) + " " + m[1]
		want := m[2] + " " + m[3]
		if m[2] == "none" {
			want = ""
		}
		if got[key] != want {
			t.Errorf("%s:%d %s: edge %q, want %q", path, i+1, strings.TrimSpace(line[:strings.Index(line, "//")]), got[key], want)
		}
	}
	if probes == 0 {
		t.Fatalf("%s holds no probe", path)
	}
}

func TestGoVariablesResolveThroughLexicalScopes(t *testing.T) {
	files := scopeFiles(t, "scopes/go")
	s, _, _ := depWS(t, files)
	checkProbes(t, s, "main.go", files["main.go"])
}

func TestTypeScriptVariablesResolveThroughLexicalScopes(t *testing.T) {
	files := scopeFiles(t, "scopes/ts")
	s, _, _ := depWS(t, files)
	checkProbes(t, s, "a.ts", files["a.ts"])
}
