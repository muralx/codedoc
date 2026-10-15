package query

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode"

	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/store"
)

// Search tuning (spec 7.7): each text index and vector scan contributes
// its top searchDepth rows; the merged text list and the merged vector
// list (see rank.go) are fused with reciprocal rank fusion constant rrfK.
const (
	searchDepth  = 50
	rrfK         = 60
	defaultLimit = 20
)

// SearchOpts filter and size a search.
type SearchOpts struct {
	Kind  string // symbol kind; "module" keeps only modules
	Lang  string // language family (go, ts) or precise language
	Limit int    // most results of one page; 0 means defaultLimit
}

// hit is a symbol or module in a ranked list; dep marks a row of the
// dependency database, whose ids are its own.
type hit struct {
	module, dep bool
	id          int64
}

// Search finds the symbols and modules whose names, signatures and docs
// match free text: BM25 over the symbol and module text indexes and, with
// an embedder, cosine similarity over their vectors, fused by reciprocal
// rank (see rankedHits). Without an embedder (or when its model cannot load) the search
// is text-only and the note says so. Stale vectors are caught up first
// when there are few (index.EnsureEmbedMax). Each entry carries a
// snippet: the doc sentence sharing the most words with the query. The
// answer is one page of o.Limit hits after Ctx.Offset; its title counts
// every ranked hit, so the total is the same on every page. With
// Ctx.Deps the packages the workspace depends on are searched the same
// way in deps.db and their lists fused with the workspace's.
func Search(c *Ctx, text string, o SearchOpts) (*output.Response, error) {
	if o.Limit < 0 {
		return nil, errors.New("--limit must not be negative")
	}
	if o.Limit == 0 {
		o.Limit = defaultLimit
	}
	if err := checkSearchOpts(o); err != nil {
		return nil, err
	}
	repos := c.scope()
	if err := c.EnsureFresh(repos...); err != nil {
		return nil, err
	}
	title := fmt.Sprintf("search %q", text)
	if len(queryWords(text)) == 0 {
		c.notes = append(c.notes, "query has no searchable words")
		return c.respond(repos, c.section(title, nil)), nil
	}
	f, err := c.searchFilter(o)
	if err != nil {
		return nil, err
	}
	hits, err := c.rankedHits(text, f, o.Kind)
	if err != nil {
		return nil, err
	}
	page := hits[:min(len(hits), c.Offset+o.Limit)]
	entries, err := c.searchEntries(page, searchTerms(text))
	if err != nil {
		return nil, err
	}
	sec := c.section(title, entries)
	// The title counts every ranked hit, whatever page is printed.
	sec.Total = len(entries) + len(hits) - len(page)
	return c.respond(repos, sec), nil
}

// rankedHits ranks the hits of a search: the hits of the text indexes and
// the symbols whose bodies use a name of the query (bodyUses), merged by
// how many query terms they hold (byCoverage), the hits of the
// vector scans merged by cosine (byCosine), the two fused by reciprocal
// rank (fuse), a document and the section that is its whole text listed
// once (oncePerText), and sections of one document kept apart (spread). The
// lists are those of the workspace index and, with Ctx.Deps, of the
// packages it depends on. A hit of a vector scan carries the passages
// that matched (see rankedHit). Every ranked hit matched: the answer's
// total counts no row a list returned only to fill its depth.
func (c *Ctx) rankedHits(text string, f store.SearchFilter, kind string) ([]rankedHit, error) {
	terms := searchTerms(text)
	found := passagesByHit{}
	lists, vecs, err := c.searchLists(text, terms, f, kind, found)
	if err != nil {
		return nil, err
	}
	uses, err := c.bodyUses(terms, f, kind)
	if err != nil {
		return nil, err
	}
	lists = append(lists, uses.list)
	textOf, err := c.hitTexts(lists, uses)
	if err != nil {
		return nil, err
	}
	uses.fill(found)
	near := byCosine(vecs)
	words := byCoverage(lists, terms, textOf, func(h hit) bool { return slices.Contains(near.hits, h) })
	syms, err := c.hitSymbols([][]hit{words, near.hits})
	if err != nil {
		return nil, err
	}
	isTest := func(h hit) bool { return !h.module && !h.dep && isTestSymbol(syms[h.id]) }
	order, scores := fuse(isTest, ordinal(words), near)
	order = oncePerText(order, syms, scores, found)
	return found.attach(spread(order, docFiles(syms)), scores), nil
}

// searchLists returns the lists of the text indexes and the hits of the
// vector scans for a query: the workspace's and, with Ctx.Deps, those of
// the packages it depends on. The passages of the vector hits are
// recorded in found.
func (c *Ctx) searchLists(text string, terms []string, f store.SearchFilter, kind string,
	found passagesByHit) ([][]hit, []scoredHit, error) {
	lists, err := textLists(c.Store, ftsQuery(terms), f, kind, false)
	if err != nil {
		return nil, nil, err
	}
	q, err := c.queryVector(text, f)
	if err != nil {
		return nil, nil, err
	}
	vecs, err := nearestHits(c.Store, q, f, kind, false, found)
	if err != nil || !c.Deps {
		return lists, vecs, err
	}
	depLists, depVecs, err := c.depLists(terms, q, f, kind, found)
	return append(lists, depLists...), append(vecs, depVecs...), err
}

// searchKinds are the values --kind accepts: the symbol kinds and module.
var searchKinds = []string{"class", "const", "document", "enum", "field", "function", "interface", "method", "module",
	"section", "struct", "trait", "type", "var"}

// searchLangs are the values --lang accepts: the language families and
// the precise languages.
var searchLangs = LangValues()

// LangHelp describes the values of a --lang flag; LangValues lists them.
const LangHelp = "language family (go, java, py, ts, doc) or precise language (javascript, python, tsx, typescript, " +
	"markdown, rst, asciidoc, text)"

// LangValues lists the values a --lang flag accepts, sorted: the language
// families and the precise languages, documents included.
func LangValues() []string {
	out := slices.Concat(parse.Families(), parse.Languages(), parse.DocLanguages(), []string{parse.DocFamily})
	slices.Sort(out)
	return slices.Compact(out)
}

// checkSearchOpts rejects a --kind or --lang that names nothing, listing
// the valid values.
func checkSearchOpts(o SearchOpts) error {
	if o.Kind != "" && !slices.Contains(searchKinds, o.Kind) {
		return fmt.Errorf("unknown --kind %q; valid: %s", o.Kind, strings.Join(searchKinds, ", "))
	}
	if o.Lang != "" && !slices.Contains(searchLangs, o.Lang) {
		return fmt.Errorf("unknown --lang %q; valid: %s", o.Lang, strings.Join(searchLangs, ", "))
	}
	return nil
}

// searchFilter turns the options into a store filter: --lang names a
// family (every precise language in it) or one precise language.
func (c *Ctx) searchFilter(o SearchOpts) (store.SearchFilter, error) {
	id, err := c.filterID()
	f := store.SearchFilter{RepoID: id}
	if o.Kind != "module" {
		f.Kind = o.Kind
	}
	if o.Lang == "" {
		return f, err
	}
	f.Family = parse.Family(o.Lang)
	f.Langs = parse.LanguagesOf(o.Lang)
	return f, err
}

// textLists returns the BM25 lists of one store: symbols and the text
// of document sections (unless only modules are asked for) and modules
// (unless a symbol kind is). deps.db holds no sections, so that list is
// a workspace one.
func textLists(s *store.Store, match string, f store.SearchFilter, kind string, dep bool) ([][]hit, error) {
	var out [][]hit
	if kind != "module" {
		ids, err := s.SymbolMatches(match, f, searchDepth)
		if err != nil {
			return nil, err
		}
		out = append(out, hits(ids, false, dep))
		// The text of documents and sections, which the symbol text
		// index (heading and summary) does not cover.
		if ids, err = s.SectionMatches(match, f, searchDepth); err != nil {
			return nil, err
		}
		if len(ids) > 0 {
			out = append(out, hits(ids, false, dep))
		}
	}
	if kind == "" || kind == "module" {
		ids, err := s.ModuleMatches(match, f, searchDepth)
		if err != nil {
			return nil, err
		}
		out = append(out, hits(ids, true, dep))
	}
	return out, nil
}

// textOnlyNote says why a search runs on text only: embeddings switched
// off, or no model configured.
func textOnlyNote() string {
	if os.Getenv(embed.EmbedderEnv) == "off" {
		return "text search only: embeddings disabled (" + embed.EmbedderEnv + "=off)"
	}
	return "text search only: no embedding model configured (embed.model_dir or CODEDOC_MODEL_DIR)"
}

// queryVector embeds the search text, after catching up a few stale
// vectors of the workspace; nil, with a note that the search is
// text-only, when there is no usable embedder.
func (c *Ctx) queryVector(text string, f store.SearchFilter) ([]float32, error) {
	if c.Embedder == nil {
		c.notes = append(c.notes, textOnlyNote())
		return nil, nil
	}
	res, err := index.EmbedVectors(c.Store, c.Embedder, store.VecScope{RepoID: max(f.RepoID, 0)}, index.EnsureEmbedMax)
	if err != nil {
		return nil, err
	}
	if res.Deferred > 0 {
		c.notes = append(c.notes, fmt.Sprintf("%d vectors are stale; run codedoc index", res.Deferred))
	}
	q, err := embed.Query(c.Embedder, text)
	if res.Unavailable || errors.Is(err, embed.ErrUnavailable) {
		c.notes = append(c.notes, "text search only: embedding model unavailable")
		return nil, nil
	}
	return q, err
}

// nearestHits returns the hits of the vector scans of one store for a
// query vector, none without one, and records in found the passages of
// the symbols among them.
func nearestHits(s *store.Store, q []float32, f store.SearchFilter, kind string, dep bool,
	found passagesByHit) ([]scoredHit, error) {
	if q == nil {
		return nil, nil
	}
	var out []scoredHit
	if kind != "module" {
		syms, err := s.NearestSymbols(q, f, searchDepth)
		if err != nil {
			return nil, err
		}
		for _, r := range syms {
			h := hit{dep: dep, id: r.ID}
			found.record(h, r.Passages)
			out = append(out, scoredHit{hit: h, score: r.Score})
		}
	}
	if kind == "" || kind == "module" {
		mods, err := s.NearestModules(q, f, searchDepth)
		if err != nil {
			return nil, err
		}
		for _, r := range mods {
			out = append(out, scoredHit{hit: hit{module: true, dep: dep, id: r.ID}, score: r.Score})
		}
	}
	return out, nil
}

// depLists returns the text lists and the vector hits of the packages
// the workspace depends on, after catching up a few of their missing
// vectors (deps spec section 8).
func (c *Ctx) depLists(terms []string, q []float32, f store.SearchFilter, kind string,
	found passagesByHit) ([][]hit, []scoredHit, error) {
	v := c.deps()
	if v == nil {
		return nil, nil, nil
	}
	df := store.SearchFilter{Kind: f.Kind, Langs: f.Langs, Family: f.Family, RepoIDs: v.repoIDs()}
	out, err := textLists(v.s, ftsQuery(terms), df, kind, true)
	if err != nil || q == nil {
		return out, nil, err
	}
	res, err := index.EmbedAPIVectors(v.s, c.Embedder, df.RepoIDs, index.EnsureEmbedMax)
	if err != nil {
		return nil, nil, err
	}
	if res.Deferred > 0 {
		c.notes = append(c.notes, fmt.Sprintf("%d dependency vectors are missing: dependencies are matched by keywords only; "+
			"run codedoc deps index --embed, or let the daemon embed them", res.Deferred))
	}
	vecs, err := nearestHits(v.s, q, df, kind, true, found)
	return out, vecs, err
}

func hits(ids []int64, module, dep bool) []hit {
	out := make([]hit, len(ids))
	for i, id := range ids {
		out[i] = hit{module: module, dep: dep, id: id}
	}
	return out
}

// hitSymbols returns the rows of the workspace symbol hits of lists, by
// id.
func (c *Ctx) hitSymbols(lists [][]hit) (map[int64]store.Symbol, error) {
	var ids []int64
	for _, l := range lists {
		for _, h := range l {
			if !h.module && !h.dep {
				ids = append(ids, h.id)
			}
		}
	}
	return c.Store.SymbolsByID(ids)
}

// ftsQuery turns search terms (see searchTerms) into an FTS5 query: each
// quoted (with a prefix star from two letters on, so persist matches
// persists) and joined with OR.
func ftsQuery(terms []string) string {
	parts := make([]string, len(terms))
	for i, w := range terms {
		parts[i] = `"` + w + `"`
		if len([]rune(w)) > 1 {
			parts[i] += "*"
		}
	}
	return strings.Join(parts, " OR ")
}

// queryWords returns the distinct lower-cased runs of letters, digits
// and underscores of text, in order.
func queryWords(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}
