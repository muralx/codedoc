package query

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/store"
)

// serverStarts are the calls that start a server (spec 9): the called
// name with the qualifiers that make it one ("" accepts any).
var serverStarts = map[string][]string{
	"ListenAndServe": {""}, "ListenAndServeTLS": {""},
	"listen": {"app"}, "run": {"uvicorn", "SpringApplication"},
}

// entryFileNames are the base names (without extension) of entry files.
var entryFileNames = map[string]bool{"main": true, "index": true, "app": true, "server": true}

// entryPoints lists a repo's entry points (spec 9): main functions,
// cmd/* and bin/* directories, Go package main directories elsewhere,
// files named main.*, index.*, app.* or server.*, and symbols that start
// a server.
func (c *Ctx) entryPoints(r *config.Repo, id int64) ([]output.Entry, error) {
	mains, err := c.Store.SymbolsByName("main", "", id)
	if err != nil {
		return nil, err
	}
	var out []output.Entry
	mainDirs := map[string]bool{}
	for _, sy := range mains {
		if (sy.Kind == "function" || sy.Kind == "method") && !sy.IsTest {
			e := symbolEntry(sy)
			e.Tags = append(e.Tags, "main")
			out = append(out, e)
			if parse.Family(sy.Lang) == "go" && sy.Kind == "function" {
				mainDirs[path.Dir(sy.Path)] = true
			}
		}
	}
	servers, err := c.serverSymbols(id)
	if err != nil {
		return nil, err
	}
	out = append(out, servers...)
	files, err := c.Store.FilesByRepo(id)
	if err != nil {
		return nil, err
	}
	return append(append(out, entryPaths(r, files)...), mainPackages(r, files, mainDirs)...), nil
}

// mainPackages lists the Go package main directories (those declaring a
// main function) that no cmd/* or bin/* directory already covers, with
// their Go file counts.
func mainPackages(r *config.Repo, files []store.File, dirs map[string]bool) []output.Entry {
	count := map[string]int{}
	for _, f := range files {
		if d := path.Dir(f.Path); dirs[d] && parse.Family(f.Lang) == "go" && !strings.HasSuffix(f.Path, "_test.go") {
			count[d]++
		}
	}
	keys := make([]string, 0, len(count))
	for d := range count {
		if _, covered := commandDir(d + "/x"); !covered {
			keys = append(keys, d)
		}
	}
	sort.Strings(keys)
	out := make([]output.Entry, 0, len(keys))
	for _, d := range keys {
		loc := d + "/"
		if d == "." {
			loc = "./"
		}
		out = append(out, output.Entry{Loc: r.Name + ":" + loc, Text: fmt.Sprintf("package main, %d %s", count[d],
			plural(count[d], "file", "files"))})
	}
	return out
}

// serverSymbols lists the symbols calling a known server start.
func (c *Ctx) serverSymbols(id int64) ([]output.Entry, error) {
	names := make([]string, 0, len(serverStarts))
	for n := range serverStarts {
		names = append(names, n)
	}
	sort.Strings(names)
	refs, err := c.Store.RepoReferencesNamed(id, names)
	if err != nil {
		return nil, err
	}
	var ids []int64
	call := map[int64]string{}
	for _, ref := range refs {
		if _, seen := call[ref.SymbolID]; seen || !startsServer(ref) {
			continue
		}
		ids = append(ids, ref.SymbolID)
		call[ref.SymbolID] = strings.TrimPrefix(ref.Qualifier+"."+ref.Name, ".")
	}
	syms, err := c.Store.SymbolsByID(ids)
	if err != nil {
		return nil, err
	}
	out := make([]output.Entry, 0, len(ids))
	for _, sid := range ids {
		e := symbolEntry(syms[sid])
		e.Tags = append(e.Tags, "calls "+call[sid])
		out = append(out, e)
	}
	return out, nil
}

// startsServer reports whether a reference calls a known server start.
func startsServer(ref store.Reference) bool {
	if ref.Kind != "call" {
		return false
	}
	for _, q := range serverStarts[ref.Name] {
		if q == "" || ref.Qualifier == q || strings.HasSuffix(ref.Qualifier, "."+q) {
			return true
		}
	}
	return false
}

// entryPaths lists cmd/* and bin/* directories (or files) and code
// files named main, index, app or server, by path.
func entryPaths(r *config.Repo, files []store.File) []output.Entry {
	dirs := map[string]int{}
	var named []string
	for _, f := range files {
		if d, ok := commandDir(f.Path); ok {
			dirs[d]++
		}
		base := path.Base(f.Path)
		if parse.Family(f.Lang) != "" && entryFileNames[strings.TrimSuffix(base, path.Ext(base))] {
			named = append(named, f.Path)
		}
	}
	keys := make([]string, 0, len(dirs))
	for d := range dirs {
		keys = append(keys, d)
	}
	sort.Strings(keys)
	var out []output.Entry
	for _, d := range keys {
		out = append(out, output.Entry{Loc: r.Name + ":" + d, Text: fmt.Sprintf("command directory, %d %s", dirs[d],
			plural(dirs[d], "file", "files"))})
	}
	sort.Strings(named)
	for _, p := range named {
		out = append(out, output.Entry{Loc: r.Name + ":" + p, Text: "entry file"})
	}
	return out
}

// commandDir returns the cmd/<x> or bin/<x> prefix of a path: the
// directory (with a trailing slash) or file right below a cmd or bin
// directory.
func commandDir(p string) (string, bool) {
	segs := strings.Split(p, "/")
	for i := 0; i+1 < len(segs); i++ {
		if segs[i] != "cmd" && segs[i] != "bin" {
			continue
		}
		d := strings.Join(segs[:i+2], "/")
		if i+2 < len(segs) {
			d += "/"
		}
		return d, true
	}
	return "", false
}
