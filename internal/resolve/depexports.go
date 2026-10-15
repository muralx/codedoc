package resolve

import "encoding/json"

// DepExportsKey prefixes the meta key of deps.db that holds the export
// graph of an npm package version (see DepExports), followed by the
// package id.
const DepExportsKey = "dep_exports:"

// maxExportFiles bounds the files one export lookup visits.
const maxExportFiles = 500

// DepExports is the re-export graph of the declaration files of one npm
// package version: what an import specifier exports besides the
// declarations of its entry file. It lets a member reached through a
// namespace (import * as ns, or a named import that is a namespace
// re-export such as zod's z) be found in the files that namespace
// stands for. Files are indexed by their position in Files.
type DepExports struct {
	// Files are the package-relative paths of the files, as their file
	// rows name them.
	Files []string `json:"files"`
	// Entries maps an import specifier to its entry file.
	Entries map[string]int `json:"entries"`
	// Stars maps a file to the files it re-exports everything from
	// (export * from "./x").
	Stars map[int][]int `json:"stars,omitempty"`
	// Named maps a file to the names it re-exports one by one (export {
	// a, b as c } from "./x").
	Named map[int]map[string]DepExport `json:"named,omitempty"`
	// Spaces maps a file to the namespaces it binds: export * as ns
	// from "./x" and import * as ns from "./x".
	Spaces map[int]map[string]int `json:"spaces,omitempty"`
}

// DepExport is one name a file re-exports from another: the file, and
// the name it has there when the re-export renames it.
type DepExport struct {
	File int    `json:"file"`
	Name string `json:"name,omitempty"`
}

// Encode renders the graph as the JSON stored in deps.db.
func (x *DepExports) Encode() string {
	raw, _ := json.Marshal(x)
	return string(raw)
}

// DecodeDepExports parses what Encode wrote.
func DecodeDepExports(raw string) (*DepExports, error) {
	var x DepExports
	if err := json.Unmarshal([]byte(raw), &x); err != nil {
		return nil, err
	}
	return &x, nil
}

// Lookup returns the files that may declare what the import specifier
// exports under name, nearest first, and the name it is declared under
// there (another one after a renaming re-export). With ns it looks
// inside the namespace the specifier exports as ns. The files are the
// entry file (or the namespace's file) and those it re-exports
// everything from, transitively; a re-export of the name alone leads to
// the files of its source instead. It returns nil for a specifier or a
// namespace the graph does not know.
func (x *DepExports) Lookup(specifier, ns, name string) (files []string, as string) {
	if x == nil {
		return nil, ""
	}
	start, ok := x.Entries[specifier]
	if !ok {
		return nil, ""
	}
	if ns != "" {
		if start, ok = x.space(start, ns); !ok {
			return nil, ""
		}
	}
	for hops := 0; hops < maxInheritance; hops++ {
		closure := x.closure(start)
		next, renamed := -1, ""
		for _, f := range closure {
			if e, ok := x.Named[f][name]; ok {
				next, renamed = e.File, e.Name
				break
			}
		}
		if next < 0 {
			return x.paths(closure), name
		}
		if start = next; renamed != "" {
			name = renamed
		}
	}
	return nil, ""
}

// space returns the file the namespace ns stands for, bound by a file
// that from re-exports everything from, itself first.
func (x *DepExports) space(from int, ns string) (int, bool) {
	for _, f := range x.closure(from) {
		if target, ok := x.Spaces[f][ns]; ok {
			return target, true
		}
	}
	return 0, false
}

// closure returns a file and the files it re-exports everything from,
// transitively, breadth first.
func (x *DepExports) closure(from int) []int {
	seen := map[int]bool{from: true}
	out := []int{from}
	for i := 0; i < len(out) && len(out) < maxExportFiles; i++ {
		for _, next := range x.Stars[out[i]] {
			if !seen[next] {
				seen[next] = true
				out = append(out, next)
			}
		}
	}
	return out
}

// paths returns the paths of files, skipping indexes out of range.
func (x *DepExports) paths(files []int) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if f >= 0 && f < len(x.Files) {
			out = append(out, x.Files[f])
		}
	}
	return out
}
