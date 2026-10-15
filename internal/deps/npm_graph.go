package deps

import (
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/resolve"
)

// exportGraph collects, while the declaration files of an npm package
// are followed, what each re-exports from the others, keyed by file
// stem (see stem): the ESM and CommonJS twins of a file are one.
type exportGraph struct {
	entries map[string]string // import specifier: stem of its entry file
	stars   map[string][]string
	named   map[string]map[string][2]string // stem: exported name: {stem, source name}
	spaces  map[string]map[string]string    // stem: namespace: stem
}

// entry records the entry file of an import specifier.
func (g *exportGraph) entry(specifier, file string) {
	if g.entries == nil {
		g.entries = map[string]string{}
	}
	g.entries[specifier] = file
}

// file records the re-exports and namespace bindings among the imports
// of the file at stem from; targets maps their relative specifiers to
// package files.
func (g *exportGraph) file(from string, imports []extract.Import, targets map[string]string) {
	for _, imp := range imports {
		next, ok := targets[imp.Raw]
		if !ok {
			continue
		}
		to := stem(next)
		switch {
		case imp.Alias != "":
			g.space(from, imp.Alias, to)
		case !imp.Reexport:
		case imp.Names == nil:
			if g.stars == nil {
				g.stars = map[string][]string{}
			}
			g.stars[from] = append(g.stars[from], to)
		default:
			g.names(from, imp.Names, to)
		}
	}
}

// space records that the file from binds the namespace ns to a file.
func (g *exportGraph) space(from, ns, to string) {
	if g.spaces == nil {
		g.spaces = map[string]map[string]string{}
	}
	if g.spaces[from] == nil {
		g.spaces[from] = map[string]string{}
	}
	g.spaces[from][ns] = to
}

// names records the names the file from re-exports from another, each
// under the name it exports it as (a, or c for "a=c").
func (g *exportGraph) names(from string, names []string, to string) {
	if g.named == nil {
		g.named = map[string]map[string][2]string{}
	}
	if g.named[from] == nil {
		g.named[from] = map[string][2]string{}
	}
	for _, n := range names {
		source, exported, renamed := strings.Cut(n, "=")
		if !renamed {
			exported, source = source, ""
		}
		g.named[from][exported] = [2]string{to, source}
	}
}

// build turns the graph into the form stored with the package: files
// are named by the paths of the extracted files, and what points at a
// file that was not extracted is dropped. It returns nil for a package
// without entry files.
func (g *exportGraph) build(files []File) *resolve.DepExports {
	x := &resolve.DepExports{Entries: map[string]int{}}
	index := map[string]int{}
	for _, f := range files {
		if _, ok := index[stem(f.Path)]; !ok {
			index[stem(f.Path)] = len(x.Files)
			x.Files = append(x.Files, f.Path)
		}
	}
	for spec, file := range g.entries {
		if i, ok := index[file]; ok {
			x.Entries[spec] = i
		}
	}
	if len(x.Entries) == 0 {
		return nil
	}
	g.link(x, index)
	return x
}

// link fills the edges of x from the collected ones.
func (g *exportGraph) link(x *resolve.DepExports, index map[string]int) {
	x.Stars, x.Named, x.Spaces = map[int][]int{}, map[int]map[string]resolve.DepExport{}, map[int]map[string]int{}
	for from, tos := range g.stars {
		for _, to := range tos {
			if i, ok := index[to]; ok {
				x.Stars[index[from]] = append(x.Stars[index[from]], i)
			}
		}
	}
	for from, names := range g.named {
		x.Named[index[from]] = map[string]resolve.DepExport{}
		for name, e := range names {
			if i, ok := index[e[0]]; ok {
				x.Named[index[from]][name] = resolve.DepExport{File: i, Name: e[1]}
			}
		}
	}
	for from, spaces := range g.spaces {
		x.Spaces[index[from]] = map[string]int{}
		for ns, to := range spaces {
			if i, ok := index[to]; ok {
				x.Spaces[index[from]][ns] = i
			}
		}
	}
}
