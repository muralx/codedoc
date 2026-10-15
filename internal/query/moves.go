package query

import (
	"path"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/store"
)

// matchInFile pairs the current symbols of a file with those its content
// at the ref declared, by local qualified name (see localName) and
// signature hash. A renamed file pairs with its old content; its symbols
// count as moved.
func matchInFile(fc *fileChange) {
	before := make(map[string]int, len(fc.prior))
	for i, s := range fc.prior {
		before[priorName(fc, s)] = i
	}
	for i, sy := range fc.cur {
		if j, ok := before[curName(sy)]; ok && !fc.taken[j] {
			fc.pair[i] = priorRef{fc: fc, idx: j, moved: fc.ch.Status == 'R'}
			fc.taken[j] = true
		}
	}
}

// matchMoves pairs the symbols left over by matchInFile across the
// changeset: a symbol removed from one file and added to another is a
// move. Symbols match first by directory (the module of Go and the
// package of Java) and local name, then by local name alone where only
// one removed and one added symbol carry it.
func matchMoves(fcs []*fileChange) {
	for _, byDir := range []bool{true, false} {
		added, removed := leftovers(fcs, byDir)
		for k, as := range added {
			rs := removed[k]
			if len(rs) == 0 || (!byDir && (len(as) != 1 || len(rs) != 1)) {
				continue
			}
			for n := range min(len(as), len(rs)) {
				a, r := as[n], rs[n]
				a.fc.pair[a.idx] = priorRef{fc: r.fc, idx: r.idx, moved: true}
				r.fc.taken[r.idx] = true
			}
		}
	}
}

// leftovers returns the unpaired current (added) and ref-side (removed)
// symbols of the changeset by move key.
func leftovers(fcs []*fileChange, byDir bool) (added, removed map[string][]priorRef) {
	added, removed = map[string][]priorRef{}, map[string][]priorRef{}
	for _, fc := range fcs {
		for i, sy := range fc.cur {
			if _, ok := fc.pair[i]; !ok {
				k := moveKey(byDir, path.Dir(fc.ch.Path), sy.Kind, curName(sy))
				added[k] = append(added[k], priorRef{fc: fc, idx: i})
			}
		}
		for j, s := range fc.prior {
			if !fc.taken[j] {
				k := moveKey(byDir, path.Dir(fc.oldPath()), string(s.Kind), priorName(fc, s))
				removed[k] = append(removed[k], priorRef{fc: fc, idx: j})
			}
		}
	}
	return added, removed
}

// moveKey is the key symbols are matched by across files.
func moveKey(byDir bool, dir, kind, name string) string {
	if byDir {
		return dir + "\x00" + kind + "\x00" + name
	}
	return kind + "\x00" + name
}

// curName is a current symbol's local name: its qualified name without
// the TypeScript file prefix, with its signature hash.
func curName(sy store.Symbol) string {
	t := symbolTarget("", sy, store.File{})
	return localName(sy.Qualified, t.ExtractPath) + "#" + t.SigHash
}

// priorName is curName for a symbol extracted at the ref.
func priorName(fc *fileChange, s extract.Symbol) string {
	return localName(s.Qualified, fc.priorPath) + "#" + s.SigHash
}

// localName strips the file prefix TypeScript qualified names carry.
func localName(qualified, filePath string) string {
	if filePath == "" {
		return qualified
	}
	return strings.TrimPrefix(qualified, filePath+"/")
}
