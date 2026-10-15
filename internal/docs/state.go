// Package docs models documentation state: staleness of symbol docs
// (spec 7.5), module doc discovery, and the ranking of documentation
// gaps (spec 11.1).
package docs

// State is the documentation state of a symbol or module.
type State string

// Documentation states.
const (
	Missing State = "missing"
	Stale   State = "stale"
	OK      State = "ok"
)

// Prior is what the index knew about a symbol before this run; nil when
// new.
type Prior struct{ DocHash, BodyHash, DocBodyHash string }

// Seed is what git blame says about a symbol seen for the first time.
type Seed int

// Seeds of a documented symbol seen for the first time or whose doc
// changed.
const (
	// SeedUntracked: the file has no git history; the doc starts ok.
	SeedUntracked Seed = iota
	// SeedDocCurrent: the doc lines are at least as new as the body lines.
	SeedDocCurrent
	// SeedBodyNewer: some body line is newer than every doc line.
	SeedBodyNewer
)

// Next computes doc_body_hash and state for a symbol (spec 7.5). A kept
// doc keeps the body hash recorded before. A symbol without prior, or
// whose doc hash changed, is seeded from seed: a body newer than its doc
// leaves the empty hash ("older than the body"), anything else records
// the current body hash. Seeding a changed doc from blame makes a doc
// that git brought back (a checkout, a branch switch) as stale as its
// commit says, while an edited doc (uncommitted lines are newer than any
// commit) is current. The state is missing without a doc, stale when the
// recorded body hash differs from the current one, else ok.
func Next(prior *Prior, docHash, bodyHash string, seed func() Seed) (docBodyHash string, st State) {
	if docHash == "" {
		return "", Missing
	}
	switch {
	case prior == nil || prior.DocHash != docHash:
		docBodyHash = bodyHash
		if seed() == SeedBodyNewer {
			docBodyHash = ""
		}
	default:
		docBodyHash = prior.DocBodyHash
	}
	if docBodyHash != bodyHash {
		return docBodyHash, Stale
	}
	return docBodyHash, OK
}

// Lines is a 1-based inclusive line range; zero values mean none.
type Lines struct{ Start, End int }

// SeedFromBlame compares the newest blame time of the body lines (the
// symbol's span without its doc lines and the skip ranges) with that of
// the doc lines. Containers skip their members' bodies and docs, so only
// their own header and member declarations count (spec 7.5).
// Uncommitted lines are newer than any commit, so a symbol whose body
// and doc are both uncommitted seeds as current.
func SeedFromBlame(blame map[int]int64, span, doc Lines, skip ...Lines) Seed {
	newest := func(r Lines, skip []Lines) (int64, bool) {
		var t int64
		found := false
		for l := r.Start; l <= r.End; l++ {
			if inAny(l, skip) {
				continue
			}
			if at, ok := blame[l]; ok {
				t, found = max(t, at), true
			}
		}
		return t, found
	}
	docAt, okDoc := newest(doc, nil)
	bodyAt, okBody := newest(span, append(skip, doc))
	if okDoc && okBody && bodyAt > docAt {
		return SeedBodyNewer
	}
	return SeedDocCurrent
}

// inAny reports whether line l lies in one of rs.
func inAny(l int, rs []Lines) bool {
	for _, r := range rs {
		if l >= r.Start && l <= r.End {
			return true
		}
	}
	return false
}
