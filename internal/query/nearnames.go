package query

import (
	"cmp"
	"slices"
	"strings"
	"unicode"

	"github.com/muralx/codedoc/internal/store"
)

// How near a name is to the one asked for, nearest first.
const (
	nearEqual  = iota // equal ignoring case
	nearSuffix        // ends with it: setRequestHandler for RequestHandler
	nearPart          // holds it, or is held by it: runMain for main
	nearWord          // shares a word with it, among the symbols of one file (see closest)
	nearTypo          // within typoLimit edits
	nearNot
)

// minPart is the shortest name offered for being a part of the one
// asked for, or asked for to find the names it is a part of: a
// one-letter variable is part of nearly everything.
const minPart = 4

// nearness ranks name against want, both lower case; d is the edit
// distance of a typo, the difference in length otherwise.
func nearness(name, want string) (tier, d int) {
	d = max(len(name)-len(want), len(want)-len(name))
	switch {
	case name == want:
		return nearEqual, 0
	case len(want) < minPart:
	case strings.HasSuffix(name, want):
		return nearSuffix, d
	case strings.Contains(name, want), len(name) >= minPart && strings.Contains(want, name):
		return nearPart, d
	}
	limit := typoLimit(len(want))
	if d = editDistance(name, want, limit); d <= limit {
		return nearTypo, d
	}
	return nearNot, d
}

// minWord is the shortest word two names are near for sharing.
const minWord = 4

// identWords splits an identifier into its lower-case words: at
// anything that is no letter or digit and before an upper-case letter
// that follows a lower-case one (registerTool: register, tool).
func identWords(name string) []string {
	var b strings.Builder
	prev := rune(0)
	for _, r := range name {
		if unicode.IsUpper(r) && unicode.IsLower(prev) {
			b.WriteByte(' ')
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteByte(' ')
		}
		prev = r
	}
	return strings.Fields(b.String())
}

// sharesWord reports whether two identifiers have a word of at least
// minWord letters in common.
func sharesWord(a, b string) bool {
	words := identWords(b)
	for _, w := range identWords(a) {
		if len(w) >= minWord && slices.Contains(words, w) {
			return true
		}
	}
	return false
}

// nearName is a name of the index with its nearness to the one asked
// for.
type nearName struct {
	name    string
	tier, d int
}

// rankNames returns the names near want, nearest first (see nearness):
// by tier, then by distance, then by name.
func rankNames(names []string, want string) []nearName {
	want = strings.ToLower(want)
	var out []nearName
	for _, n := range names {
		if tier, d := nearness(strings.ToLower(n), want); tier != nearNot {
			out = append(out, nearName{n, tier, d})
		}
	}
	slices.SortStableFunc(out, func(a, b nearName) int {
		return cmp.Or(cmp.Compare(a.tier, b.tier), cmp.Compare(a.d, b.d), cmp.Compare(a.name, b.name))
	})
	return out
}

// names returns the distinct symbol names the lookup covers.
func (l lookup) names() ([]string, error) {
	switch {
	case l.repos != nil:
		return l.s.SymbolNames(l.repos...)
	case l.repoID != 0:
		return l.s.SymbolNames(l.repoID)
	}
	return l.s.SymbolNames()
}

// The ranks of the symbols near an address, best first: the nearness of
// their name, a name equal ignoring case split by how much of the
// qualified address the symbol has.
const (
	rankQualified = iota // the address, ignoring case: memstore.get for MemStore.Get
	rankEnding           // its qualified name ends with the address: Store.get for MemStore.get
	rankName             // the name, in another container
	rankSuffix
	rankPart
	rankTypo
	rankCount
)

// rankOf ranks a symbol whose name has the given nearness to the last
// segment of addr.
func rankOf(sy store.Symbol, tier int, addr string) int {
	q, want := strings.ToLower(sy.Qualified), strings.ToLower(addr)
	switch {
	case tier == nearEqual && matchQualified(q, want):
		return rankQualified
	case tier == nearEqual && strings.HasSuffix(q, want):
		return rankEnding
	case tier == nearEqual:
		return rankName
	case tier == nearSuffix:
		return rankSuffix
	case tier == nearPart:
		return rankPart
	}
	return rankTypo
}

// near finds the symbols whose name is nearest to an address that names
// none: the last segment of the address is compared with every name of
// the lookup (see rankNames), and the symbols of the nearest names are
// returned by rank (see rankOf), within one rank those the lookup
// prefers (the repo the query runs in) and production code before
// tests, nearestCap at most. one tells that the first is the only
// plausible reading: the only symbol of the first rank that has any,
// when that is a name equal ignoring case or one ending with the
// address.
func (l lookup) near(addr string) (syms []store.Symbol, one bool, err error) {
	last := addr[strings.LastIndex(addr, "/")+1:]
	last = last[strings.LastIndex(last, ".")+1:]
	if last == "" {
		return nil, false, nil
	}
	names, err := l.names()
	if err != nil {
		return nil, false, err
	}
	ranks := make([][]store.Symbol, rankCount)
	fetched := 0
	for _, n := range rankNames(names, last) {
		if fetched >= nearestCap && n.tier > nearEqual {
			break
		}
		found, err := l.named(n.name, "")
		if err != nil {
			return nil, false, err
		}
		fetched += len(found)
		for _, sy := range found {
			r := rankOf(sy, n.tier, addr)
			ranks[r] = append(ranks[r], sy)
		}
	}
	for r, in := range ranks {
		slices.SortStableFunc(in, func(a, b store.Symbol) int {
			return cmp.Or(cmp.Compare(l.preferred(a), l.preferred(b)),
				cmp.Compare(boolRank(isTestSymbol(a)), boolRank(isTestSymbol(b))))
		})
		one = one || len(syms) == 0 && len(in) == 1 && r <= rankSuffix
		syms = append(syms, in...)
	}
	return syms[:min(len(syms), nearestCap)], one, nil
}

// preferred is the rank the lookup gives a candidate among those of
// one nearness, 0 without a preference.
func (l lookup) preferred(sy store.Symbol) int {
	if l.prefer == nil {
		return 0
	}
	return l.prefer(sy)
}

// nearNames finds the symbols nearest to a name no symbol has: of the
// workspace, else, for a query that answers for dependencies, of the
// packages it uses.
func (c *Ctx) nearNames(name string, mode depMode) (near, error) {
	l, err := c.workspace()
	if err != nil {
		return near{}, err
	}
	syms, one, err := l.near(name)
	if err != nil || len(syms) > 0 || mode == depsRefused {
		return near{syms: syms, one: one, byName: true}, err
	}
	v := c.deps()
	if v == nil {
		return near{byName: true}, nil
	}
	syms, one, err = v.lookup().near(name)
	return near{deps: v.wrap(syms), one: one, byName: true}, err
}
