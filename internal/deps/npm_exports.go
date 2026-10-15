package deps

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// npmEntry is one way into a package: an import subpath and the
// declaration file behind it.
type npmEntry struct {
	// Subpath is "." for the package itself, else "./sub/path".
	Subpath string
	// File is the declaration file, relative to the package root.
	File string
	// rank orders the entries naming one file; the first names its
	// module: plain exports (0), then pattern exports found through a
	// runtime target (1), then through a declaration target (2).
	rank int
}

// conditionOrder is the order export conditions are tried in; the rest
// follow by name.
var conditionOrder = []string{"types", "import", "default", "require"}

// maxPatternFiles bounds the files a subpath pattern is matched against.
const maxPatternFiles = 5000

// npmEntries returns the entry points of the package at dir (deps spec
// section 5), in the order of npmEntry.before: every subpath of exports that
// resolves to a declaration file, one file per subpath, and when "." is
// not among them the types or typings field, else the declaration file
// next to main, else index.d.ts. A subpath pattern ("./*") yields one
// entry per file that matches its target.
func npmEntries(dir string, pj packageJSON) []npmEntry {
	var out []npmEntry
	seen := map[string]bool{}
	add := func(e npmEntry) {
		if e.File != "" && !seen[e.Subpath] {
			seen[e.Subpath] = true
			out = append(out, e)
		}
	}
	exports := exportsMap(pj.Exports)
	for _, key := range sortedKeys(exports) {
		if !strings.Contains(key, "*") {
			add(npmEntry{Subpath: key, File: resolveTarget(dir, exports[key], "")})
		}
	}
	for _, cand := range []string{pj.Types, pj.Typings, pj.Main, "index.d.ts"} {
		if cand != "" && !seen["."] {
			add(npmEntry{Subpath: ".", File: declFile(dir, cand)})
		}
	}
	for _, key := range sortedKeys(exports) {
		if strings.Count(key, "*") == 1 {
			for _, e := range patternEntries(dir, key, exports[key]) {
				add(e)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].before(out[j]) })
	return out
}

// before orders entries: by rank, then subpaths ending in .cjs last (of
// the two specifiers of a dual package's file the ESM one names the
// module), then by subpath.
func (e npmEntry) before(o npmEntry) bool {
	if e.rank != o.rank {
		return e.rank < o.rank
	}
	if ec, oc := strings.HasSuffix(e.Subpath, ".cjs"), strings.HasSuffix(o.Subpath, ".cjs"); ec != oc {
		return oc
	}
	return e.Subpath < o.Subpath
}

// exportsMap normalizes the exports field to a map from subpath to
// target: a string, an array or a conditions object is the target of ".".
func exportsMap(raw json.RawMessage) map[string]any {
	var v any
	if len(raw) == 0 || json.Unmarshal(raw, &v) != nil || v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		for key := range m {
			if strings.HasPrefix(key, ".") {
				return m
			}
		}
	}
	return map[string]any{".": v}
}

// resolveTarget returns the declaration file an exports target leads to,
// with star substituted for "*": a string names a file (see declFile), an
// array its first target that resolves, a conditions object the first of
// its conditions that does, in conditionOrder. It returns "" for none.
func resolveTarget(dir string, target any, star string) string {
	switch t := target.(type) {
	case string:
		return declFile(dir, strings.ReplaceAll(t, "*", star))
	case []any:
		for _, alt := range t {
			if f := resolveTarget(dir, alt, star); f != "" {
				return f
			}
		}
	case map[string]any:
		for _, cond := range conditions(t) {
			if f := resolveTarget(dir, t[cond], star); f != "" {
				return f
			}
		}
	}
	return ""
}

// conditions orders the keys of a conditions object: conditionOrder
// first, the others by name.
func conditions(m map[string]any) []string {
	var out []string
	for _, c := range conditionOrder {
		if _, ok := m[c]; ok {
			out = append(out, c)
		}
	}
	for _, c := range sortedKeys(m) {
		if !contains(conditionOrder, c) {
			out = append(out, c)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// declSuffixes maps the extension of a JavaScript file to that of the
// declaration file TypeScript reads next to it.
var declSuffixes = [][2]string{{".mjs", ".d.mts"}, {".cjs", ".d.cts"}, {".jsx", ".d.ts"}, {".js", ".d.ts"}}

// isDecl reports whether a path names a TypeScript file: a declaration
// file, or source a package ships as its types.
func isDecl(p string) bool {
	for _, ext := range []string{".ts", ".mts", ".cts", ".tsx"} {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return false
}

// declFile returns the declaration file, relative to the package at dir,
// that a package-relative target stands for: the target itself when it
// is a TypeScript file, the declaration file next to a JavaScript file,
// and for a target without such an extension target.d.ts or
// target/index.d.ts. It returns "" when no such file exists or the
// target leaves the package.
func declFile(dir, target string) string {
	t := path.Clean(filepath.ToSlash(target))
	if t == ".." || strings.HasPrefix(t, "../") || path.IsAbs(t) {
		return ""
	}
	var cands []string
	if isDecl(t) {
		cands = []string{t}
	}
	for _, s := range declSuffixes {
		if base, ok := strings.CutSuffix(t, s[0]); ok && len(cands) == 0 {
			cands = []string{base + s[1], base + ".d.ts"}
		}
	}
	if len(cands) == 0 {
		cands = []string{t + ".d.ts", path.Join(t, "index.d.ts")}
	}
	for _, c := range cands {
		if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(c))); err == nil && info.Mode().IsRegular() {
			return c
		}
	}
	return ""
}

// patternEntries expands a subpath pattern such as "./*" statically:
// every file of the package that one of the pattern's targets matches
// stands for a subpath, which is then resolved like a plain one. A
// declaration file matched by a runtime target is no subpath anyone
// imports and is left out.
func patternEntries(dir, key string, target any) []npmEntry {
	keyPre, keyPost, _ := strings.Cut(key, "*")
	var out []npmEntry
	seen := map[string]bool{}
	for _, leaf := range starLeaves(target) {
		pre, post, _ := strings.Cut(path.Clean(leaf), "*")
		rank := 1
		if isDecl(leaf) {
			rank = 2
		}
		for _, f := range filesBelow(dir, path.Dir(pre+"x")) {
			star, ok := strings.CutPrefix(f, pre)
			if star, ok = cutSuffix(star, post, ok); !ok || star == "" || (rank == 1 && isDecl(star)) {
				continue
			}
			sub := keyPre + star + keyPost
			if file := resolveTarget(dir, target, star); file != "" && !seen[sub] {
				seen[sub] = true
				out = append(out, npmEntry{Subpath: sub, File: file, rank: rank})
			}
		}
	}
	return out
}

// cutSuffix is strings.CutSuffix carrying an earlier cut's outcome.
func cutSuffix(s, suffix string, ok bool) (string, bool) {
	if !ok {
		return s, false
	}
	return strings.CutSuffix(s, suffix)
}

// starLeaves lists the string targets holding one "*" below an exports
// target, in resolution order.
func starLeaves(target any) []string {
	switch t := target.(type) {
	case string:
		if strings.Count(t, "*") == 1 {
			return []string{t}
		}
	case []any:
		var out []string
		for _, alt := range t {
			out = append(out, starLeaves(alt)...)
		}
		return out
	case map[string]any:
		var out []string
		for _, cond := range conditions(t) {
			out = append(out, starLeaves(t[cond])...)
		}
		return out
	}
	return nil
}

// filesBelow lists the regular files below a package-relative directory,
// relative to the package, sorted, without those of nested node_modules
// and at most maxPatternFiles of them.
func filesBelow(dir, rel string) []string {
	var out []string
	root := filepath.Join(dir, filepath.FromSlash(rel))
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return nil
		case d.IsDir() && d.Name() == "node_modules":
			return filepath.SkipDir
		case len(out) >= maxPatternFiles:
			return filepath.SkipAll
		case d.Type().IsRegular():
			if r, err := filepath.Rel(dir, p); err == nil {
				out = append(out, filepath.ToSlash(r))
			}
		}
		return nil
	})
	return out
}
