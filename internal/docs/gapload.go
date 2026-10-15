package docs

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/muralx/codedoc/internal/gitmeta"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/stablekey"
	"github.com/muralx/codedoc/internal/store"
)

// RepoRef names an indexed repo: its row id, name and root.
type RepoRef struct {
	ID         int64
	Name, Root string
}

// trivialMaxLines bounds the size of a function checked for being a
// trivial accessor.
const trivialMaxLines = 5

// RepoGaps returns the ranked documentation gaps of one repo (see Rank):
// its symbols and modules whose doc is not ok. Files with uncommitted
// changes count as changed now. It is what the gaps query and an
// unattended documentation run work from.
func RepoGaps(s *store.Store, r RepoRef, now time.Time) ([]Gap, error) {
	dirty, err := dirtySet(r.Root)
	if err != nil {
		return nil, err
	}
	syms, err := s.GapSymbols(r.ID)
	if err != nil {
		return nil, err
	}
	mods, err := s.GapModules(r.ID)
	if err != nil {
		return nil, err
	}
	sizes, err := repoModuleSizes(s, r.ID)
	if err != nil {
		return nil, err
	}
	src := map[string][]string{}
	cands := make([]Candidate, 0, len(syms)+len(mods))
	for _, g := range syms {
		cands = append(cands, symbolCandidate(r, g, dirty, src))
	}
	for _, g := range mods {
		cands = append(cands, moduleCandidate(r, g, sizes[g.ID].Files, dirty))
	}
	return Rank(cands, now), nil
}

// repoModuleSizes returns the size of every module of a repo (see
// ModuleSizes).
func repoModuleSizes(s *store.Store, repoID int64) (map[int64]ModuleSize, error) {
	mods, err := s.ModulesByRepo(repoID)
	if err != nil {
		return nil, err
	}
	files, err := s.FilesByRepo(repoID)
	if err != nil {
		return nil, err
	}
	return ModuleSizes(mods, files), nil
}

// dirtySet returns the paths with uncommitted changes; none outside git.
func dirtySet(root string) (map[string]bool, error) {
	paths, err := gitmeta.DirtyFiles(root)
	if errors.Is(err, gitmeta.ErrNotRepo) {
		return nil, nil
	}
	out := make(map[string]bool, len(paths))
	for _, p := range paths {
		out[p] = true
	}
	return out, err
}

func symbolCandidate(r RepoRef, g store.GapSymbol, dirty map[string]bool, src map[string][]string) Candidate {
	c := Candidate{Key: g.StableKey, Module: stablekey.Module(g.StableKey), Path: g.Path, Line: g.StartLine, Kind: g.Kind,
		Signature: g.Signature, Visibility: g.Visibility, State: State(g.DocState), FanIn: g.FanIn,
		ChangedAt: g.ChangedAt, Lines: g.EndLine - g.StartLine + 1, Test: g.IsTest, Generated: g.Generated,
		DocumentedInterface: (g.ContainerKind == "interface" || g.ContainerKind == "trait") &&
			g.ContainerDoc != "" && g.ContainerDoc != string(Missing),
		Synthetic: synthetic(g.Symbol), RepoID: r.ID}
	if dirty[g.Path] {
		c.ChangedAt = math.MaxInt64
	}
	if (g.Kind == "method" || g.Kind == "function") && c.Lines <= trivialMaxLines {
		c.Trivial = TrivialBody(g.Lang, sourceLines(r.Root, g.Path, g.StartLine, g.EndLine, src))
	}
	return c
}

// moduleCandidate builds the candidate of a module of the given file
// count (see ModuleSizes).
func moduleCandidate(r RepoRef, g store.GapModule, files int, dirty map[string]bool) Candidate {
	c := Candidate{Key: r.Name + ":" + g.Path + "/", Module: g.Identity, Path: g.Path, Kind: "module",
		Signature: g.Lang + " " + g.Kind + " " + g.Identity, Visibility: "public", State: State(g.DocState),
		FanIn: g.FanIn, ChangedAt: g.ChangedAt, Files: files, RepoID: r.ID}
	for p := range dirty {
		if g.Path == "." || p == g.Path || strings.HasPrefix(p, g.Path+"/") {
			c.ChangedAt = math.MaxInt64
			break
		}
	}
	return c
}

// synthetic reports whether a symbol is implied by the language rather
// than written: a Java record accessor (its signature is the bare
// "name()" the extractor gives it), or a TypeScript test call (it: and
// test: symbols).
func synthetic(sy store.Symbol) bool {
	switch {
	case sy.Lang == "java":
		return sy.Kind == "method" && sy.Signature == sy.Name+"()"
	case parse.Family(sy.Lang) == "ts":
		q := "/" + sy.Qualified
		return strings.Contains(q, "/it:") || strings.Contains(q, "/test:")
	}
	return false
}

// sourceLines returns lines from to through of a repo file, read once
// per file into cache.
func sourceLines(root, p string, from, through int, cache map[string][]string) string {
	lines, ok := cache[p]
	if !ok {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err == nil {
			lines = strings.Split(string(b), "\n")
		}
		cache[p] = lines
	}
	if from < 1 || through > len(lines) || from > through {
		return ""
	}
	return strings.Join(lines[from-1:through], "\n")
}
