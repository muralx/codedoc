package query

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/docs"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// ConfigOpts select the sections of a config answer; with neither flag
// set, all three render (defines, references, reads).
type ConfigOpts struct {
	Defines, Reads bool
}

// Config answers where a config key is defined, which config files and
// templates refer to it, and which code symbols read it (spec section
// 9), across the workspace or for the keys of the --repo repo. A glob
// (lib.*, *_MODE) first lists the matching keys with their counts, then
// the sections of each key in name order until the budget runs out. Code
// reads of a key no indexed config file defines (an environment variable
// set outside the repo) are listed too, tagged undefined; a glob lists
// such a key only when an accessor such as os.Getenv reads it. A key
// nothing defines, references or reads is an empty section with a note
// saying so.
func Config(c *Ctx, pattern string, o ConfigOpts) (*output.Response, error) {
	repos := c.scope()
	if err := c.EnsureFresh(repos...); err != nil {
		return nil, err
	}
	repoID, err := c.filterID()
	if err != nil {
		return nil, err
	}
	glob := strings.ContainsAny(pattern, "*?[")
	ku, err := c.keyUses(pattern, glob, repoID)
	if err != nil {
		return nil, err
	}
	if glob {
		return c.globAnswer(repos, pattern, ku, o)
	}
	if len(ku.names) == 0 {
		c.notes = append(c.notes, "no definitions, references or reads")
		return c.respond(repos, c.section("config "+pattern, nil)), nil
	}
	var secs []output.Section
	for _, name := range ku.names {
		more, err := c.keySections(name, ku, o)
		if err != nil {
			return nil, err
		}
		secs = append(secs, more...)
	}
	return c.respond(repos, secs...), nil
}

// globAnswer lists the keys matching a glob, then the sections of each
// key in name order while they fit in the budget (all of them without
// one); the keys left out are noted.
func (c *Ctx) globAnswer(repos []*config.Repo, pattern string, ku *keyUses, o ConfigOpts) (*output.Response, error) {
	names := ku.names
	secs := []output.Section{c.tagged("config keys matching "+pattern, keyEntries(ku))}
	used := output.SectionsSize(secs) + headerReserve
	shown := 0
	for _, name := range names {
		more, err := c.keySections(name, ku, o)
		if err != nil {
			return nil, err
		}
		size := output.SectionsSize(more)
		if c.Budget > 0 && used+size > c.Budget {
			break
		}
		used += size
		secs = append(secs, more...)
		shown++
	}
	if rest := len(names) - shown; rest > 0 {
		c.notes = append(c.notes, fmt.Sprintf("sections of %d more %s omitted; codedoc config <key> prints those of one key",
			rest, plural(rest, "key", "keys")))
	}
	return c.respond(repos, secs...), nil
}

// headerReserve is the room kept for the header and note lines when an
// answer sizes its sections against the budget.
const headerReserve = 400

// keyUses holds the keys of a config answer: the names, sorted, with
// the uses of their key rows and the code reads of each that no defined
// key matched (undefined keys, mostly environment variables set outside
// the repo).
type keyUses struct {
	names     []string
	uses      map[string][]store.ConfigUse
	undefined map[string][]store.ConfigCandidate
}

// keyUses collects the keys named pattern, or matching it when glob is
// set, with their uses and undefined reads. A glob lists an undefined
// key only when an accessor reads it (store.UnmatchedConfigReads).
func (c *Ctx) keyUses(pattern string, glob bool, repoID int64) (*keyUses, error) {
	var keys []store.ConfigKey
	var err error
	if glob {
		keys, err = c.Store.ConfigKeysMatching(pattern, repoID)
	} else {
		keys, err = c.Store.ConfigKeysByName(pattern, repoID)
	}
	if err != nil {
		return nil, err
	}
	ku, err := c.configUses(keys)
	if err != nil {
		return nil, err
	}
	reads, err := c.Store.UnmatchedConfigReads(pattern, glob, repoID)
	for _, r := range reads {
		if !slices.Contains(ku.names, r.Name) {
			ku.names = append(ku.names, r.Name)
		}
		ku.undefined[r.Name] = append(ku.undefined[r.Name], r)
	}
	sort.Strings(ku.names)
	return ku, err
}

// configUses groups the uses of key rows by key name.
func (c *Ctx) configUses(keys []store.ConfigKey) (*keyUses, error) {
	ku := &keyUses{uses: map[string][]store.ConfigUse{}, undefined: map[string][]store.ConfigCandidate{}}
	byID := make(map[int64]string, len(keys))
	ids := make([]int64, len(keys))
	for i, k := range keys {
		ids[i], byID[k.ID] = k.ID, k.Key
		if !slices.Contains(ku.names, k.Key) {
			ku.names = append(ku.names, k.Key)
		}
	}
	all, err := c.Store.ConfigUses(ids)
	for _, u := range all {
		ku.uses[byID[u.KeyID]] = append(ku.uses[byID[u.KeyID]], u)
	}
	return ku, err
}

// keyEntries lists keys with the number of uses of each kind (a read
// matched in several repos once) and the repos defining them; a key that
// code reads but no indexed config file defines is tagged undefined.
func keyEntries(ku *keyUses) []output.Entry {
	out := make([]output.Entry, len(ku.names))
	for i, name := range ku.names {
		n := map[string]int{}
		var repos []string
		seen := map[string]bool{}
		for _, u := range ku.uses[name] {
			if u.Kind == store.ConfigReads {
				if seen[readID(u)] {
					continue
				}
				seen[readID(u)] = true
			}
			n[u.Kind]++
			if u.Kind == store.ConfigDefines && !slices.Contains(repos, u.Repo) {
				repos = append(repos, u.Repo)
			}
		}
		undefined := len(ku.undefined[name])
		text := fmt.Sprintf("defines:%d references:%d reads:%d", n[store.ConfigDefines], n[store.ConfigReferences],
			n[store.ConfigReads]+undefined)
		if len(repos) > 0 {
			text += " in " + strings.Join(repos, ",")
		}
		out[i] = output.Entry{Key: name, Text: text, RepoID: onlyRepo(ku.uses[name])}
		if undefined > 0 {
			out[i].Tags = []string{undefinedTag}
		}
	}
	return out
}

// onlyRepo returns the repo id all of uses share, 0 when there are none
// or several.
func onlyRepo(uses []store.ConfigUse) int64 {
	var id int64
	for _, u := range uses {
		if id != 0 && u.RepoID != id {
			return 0
		}
		id = u.RepoID
	}
	return id
}

// undefinedTag marks a key, or a read of it, that no indexed config file
// defines.
const undefinedTag = "undefined"

// keySections renders the requested sections of one key; its reads
// that no defined key matched follow the others, tagged undefined. Each
// section lists production code before tests (tagged test).
func (c *Ctx) keySections(name string, ku *keyUses, o ConfigOpts) ([]output.Section, error) {
	all := !o.Defines && !o.Reads
	var defs, refs []output.Entry
	var reads []store.ConfigUse
	for _, u := range ku.uses[name] {
		switch u.Kind {
		case store.ConfigDefines:
			defs = append(defs, fileUseEntry(u))
		case store.ConfigReferences:
			refs = append(refs, fileUseEntry(u))
		default:
			reads = append(reads, u)
		}
	}
	var secs []output.Section
	if all || o.Defines {
		secs = append(secs, c.tagged("defines "+name, testsLast(defs)))
	}
	if all {
		secs = append(secs, c.tagged("references "+name, testsLast(refs)))
	}
	if all || o.Reads {
		entries, err := c.readEntries(reads)
		if err != nil {
			return nil, err
		}
		undefined, err := c.undefinedEntries(ku.undefined[name])
		if err != nil {
			return nil, err
		}
		entries = append(entries, undefined...)
		secs = append(secs, c.tagged("reads "+name, testsLast(entries)))
	}
	return secs, nil
}

// fileUseEntry renders a definition or reference in a config file:
// repo:path:line, format, value preview; tagged test in a test path.
func fileUseEntry(u store.ConfigUse) output.Entry {
	return output.Entry{Loc: fmt.Sprintf("%s:%s:%d", u.Repo, u.Path, u.Line), Text: u.Format, Doc: u.Preview,
		Extra: map[string]any{"kind": u.Kind}, Tags: pathTestTags(u.Path)}
}

// pathTestTags tags a location in a test file (docs.IsTestPath) test.
func pathTestTags(p string) []string {
	if docs.IsTestPath(p) {
		return []string{testTag}
	}
	return nil
}

// readEntries renders code reads as the reading symbol at the read's
// line, each read once even when it matched the key in several repos.
func (c *Ctx) readEntries(reads []store.ConfigUse) ([]output.Entry, error) {
	seen := map[string]bool{}
	var out []output.Entry
	for _, u := range reads {
		if seen[readID(u)] {
			continue
		}
		seen[readID(u)] = true
		e, err := c.readEntry(u.SymbolID, u.Repo, u.Path, u.Line)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// undefinedEntries renders the reads of a key no indexed config file
// defines like readEntries, tagged undefined.
func (c *Ctx) undefinedEntries(reads []store.ConfigCandidate) ([]output.Entry, error) {
	out := make([]output.Entry, 0, len(reads))
	for _, r := range reads {
		e, err := c.readEntry(r.SymbolID, r.Repo, r.Path, r.Line)
		if err != nil {
			return nil, err
		}
		e.Tags = append(e.Tags, undefinedTag)
		out = append(out, e)
	}
	return out, nil
}

// readEntry renders one code read: the reading symbol at path:line, or
// repo:path:line at file level when symbolID is 0.
func (c *Ctx) readEntry(symbolID int64, repo, path string, line int) (output.Entry, error) {
	loc := fmt.Sprintf("%s:%d", path, line)
	if symbolID == 0 {
		return output.Entry{Loc: repo + ":" + loc, Text: "(file level)", Tags: pathTestTags(path)}, nil
	}
	sy, err := c.Store.SymbolByID(symbolID)
	if err != nil {
		return output.Entry{}, err
	}
	e := symbolEntry(sy)
	e.Loc = loc
	return e, nil
}

// readID identifies a code read across the key rows it matched.
func readID(u store.ConfigUse) string { return fmt.Sprintf("%d:%d", u.FileID, u.Line) }
