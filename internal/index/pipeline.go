package index

import (
	"errors"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/extract/cfg"
	"github.com/muralx/codedoc/internal/gitmeta"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/resolve"
	"github.com/muralx/codedoc/internal/stablekey"
	"github.com/muralx/codedoc/internal/store"
)

// batchSize is the number of file writes committed per transaction.
const batchSize = 500

// maxPathspec caps how many paths are passed to one git log call; above
// it the full history is read instead.
const maxPathspec = 500

// Options control one repo index run or freshness check.
type Options struct {
	// Workspace is the name the repo row is filed under.
	Workspace string
	// Exclude holds the workspace's doublestar exclude globs.
	Exclude []string
	// MaxFiles is the most added plus changed files Ensure re-extracts;
	// above it the index is left untouched. It has no implicit default:
	// zero re-extracts nothing. Repo ignores it.
	MaxFiles int
	// RepoOrder lists the workspace's repo names in config order; import
	// resolution prefers earlier repos when several provide an identity.
	RepoOrder []string
	// Embedder, when set, embeds the vectors of the symbols Ensure and
	// Files re-extract (see embedTouched). Repo ignores it: codedoc index
	// runs EmbedVectors over the whole workspace afterwards.
	Embedder embed.Embedder
	// bulk tunes bulk FTS mode; the zero value is the default.
	bulk bulkTuning
}

// Result summarises one repo index run.
type Result struct {
	Branch, Commit string
	// FilesIndexed counts new and changed files; FilesAdded is the new subset.
	FilesIndexed, FilesAdded int
	// FilesSkipped counts unchanged files and files that could not be read.
	FilesSkipped, FilesDeleted int
	// Dirty counts the uncommitted and untracked files git status
	// reports after the run; 0 outside a git repository.
	Dirty int
}

// Repo brings the file and symbol rows of one repository up to date with
// its working tree: new and changed files (by content hash) are extracted
// and written, missing ones deleted, and last-commit metadata refreshed
// from git. A directory that is not a git repository is indexed with
// empty branch and commit. It holds the store's write lock throughout.
func Repo(s *store.Store, r *config.Repo, opts Options) (res Result, err error) {
	unlock, err := s.WriteLock()
	if err != nil {
		return Result{}, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	st, err := loadState(s, r.Path)
	if err != nil {
		return Result{}, err
	}
	found, unreadable, err := Discover(r.Path, opts.Exclude, r.FileLanguages())
	if err != nil {
		return Result{}, err
	}
	sy, err := sync(s, r, opts, st, fullChangeSet(found, st.rows))
	sy.res.FilesSkipped += unreadable
	if err != nil || !st.isRepo {
		return sy.res, err
	}
	dirty, err := gitmeta.DirtyFiles(r.Path)
	sy.res.Dirty = len(dirty)
	return sy.res, err
}

// state is what a sync starts from: the live head and the stored rows.
type state struct {
	head   gitmeta.HeadState
	isRepo bool
	prev   store.Repo // zero when the repo was never indexed
	rows   []store.File
	// reseed holds the paths of the rows extracted with another
	// extractor version (see checkVersion).
	reseed map[string]bool
}

func loadState(s *store.Store, root string) (state, error) {
	var st state
	var err error
	st.head, err = gitmeta.Head(root)
	st.isRepo = !errors.Is(err, gitmeta.ErrNotRepo)
	if err != nil && st.isRepo {
		return st, err
	}
	st.prev, err = s.RepoByPath(root)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return st, err
	}
	if st.rows, err = s.FilesByRepo(st.prev.ID); err != nil {
		return st, err
	}
	st.checkVersion()
	return st, nil
}

// changeSet is what one sync writes: files to write, the stored rows by
// path, and the rows of files that are gone.
type changeSet struct {
	write    []Discovered
	existing map[string]store.File
	gone     []store.File
}

// fullChangeSet writes every discovered file and deletes the rest.
func fullChangeSet(found []Discovered, rows []store.File) changeSet {
	cs := changeSet{write: found, existing: byPath(rows)}
	listed := make(map[string]bool, len(found))
	for _, d := range found {
		listed[d.Path] = true
	}
	for _, f := range rows {
		if !listed[f.Path] {
			cs.gone = append(cs.gone, f)
		}
	}
	return cs
}

func byPath(rows []store.File) map[string]store.File {
	m := make(map[string]store.File, len(rows))
	for _, f := range rows {
		m[f.Path] = f
	}
	return m
}

// sync applies a change set and records the new head. The returned
// syncer tells what was extracted and whether modules changed.
func sync(s *store.Store, r *config.Repo, opts Options, st state, cs changeSet) (*syncer, error) {
	// Keep the previous head until the files are written, so an aborted
	// run is retried from the last commit that was fully indexed.
	id, err := s.UpsertRepo(store.Repo{Workspace: opts.Workspace, Name: r.Name, Path: r.Path,
		Branch: st.prev.Branch, Commit: st.prev.Commit})
	sy := &syncer{s: s, repoID: id, name: r.Name, root: r.Path, isRepo: st.isRepo, reseed: st.reseed,
		bulk: opts.bulk, res: Result{Branch: st.head.Branch, Commit: st.head.Commit}}
	if err != nil {
		return sy, err
	}
	if err := sy.apply(cs, st.prev.Commit); err != nil {
		return sy, err
	}
	if err := s.UpdateRepoHead(id, st.head.Branch, st.head.Commit); err != nil {
		return sy, err
	}
	return sy, s.SetRepoIndexedAt(id, time.Now().Unix())
}

// syncer reconciles discovered files with the stored rows of one repo.
type syncer struct {
	bulk   bulkTuning
	s      *store.Store
	repoID int64
	name   string
	root   string
	isRepo bool
	res    Result
	mods   *moduleState
	// force holds the paths re-extracted although their content did not
	// change, because their module identity did.
	force map[string]bool
	// extracted holds the ids of the files whose content was extracted.
	extracted []int64
	// sources holds, by row id, the modules of the files extracted or
	// deleted: the modules whose outgoing module edges may have changed.
	sources map[int64]store.Module
	// cleared holds the key bases of the symbols deleted by this sync.
	cleared map[string]bool
	// names holds the names of the symbols deleted or written by this
	// sync and of the config keys whose definitions it added or removed:
	// references named one of them may resolve differently now.
	names map[string]bool
	// defined holds, by path, the keys the cleared config files defined.
	defined map[string][]string
	// fields holds the names of the fields a rewrite added or removed and
	// of the Go, TS and Java fields whose signature it changed:
	// references through them are resolved again.
	fields map[string]bool
	// shapes holds the Go modules whose interfaces, structs or method
	// sets changed: their implements edges are computed again.
	shapes map[int64]bool
	// pre holds, by path, the extractions done ahead of the writes of
	// the current chunk.
	pre map[string]preExtracted
	// modDocs holds, by path, the module doc of every code file the sync
	// extracted, nil for a file without one or deleted.
	modDocs map[string]*extract.Doc
	// reseed holds the paths whose stored doc tracking state is ignored:
	// their rows come from another extractor version (see checkVersion).
	reseed map[string]bool
	// guard is set once the first batch before finish recorded a full
	// resolution as pending (see guardPending); prior is the pending
	// resolution found then.
	guard *pending
	// moved holds, by stable key, the doc tracking state of the symbols
	// this sync parked or deleted, so a symbol that moved to another file
	// keeps it (see docStates).
	moved map[string]store.DocPrior
	// docs is set once the sync wrote or deleted a document: the edges
	// between the repo's documents are derived again (see linkEdges).
	docs bool
}

// apply stores the module rows, then applies the change set plus stored
// files whose last commit or module identity moved, in the order the
// stable key rules need (see AssignKeys): rows of gone files are deleted,
// the symbols of files to re-extract cleared, the files written, and
// finally lone hashed keys restored, config keys no file defines or
// references any more deleted, and modules left without files deleted.
// Each step commits batchSize files per transaction. The files to write
// are extracted one chunk ahead of the writes (see extractAhead), from
// the start: the extraction of the first chunk overlaps the deletes and
// parks, that of each next chunk the writes of the current one.
func (sy *syncer) apply(cs changeSet, since string) error {
	var err error
	if sy.mods, err = sy.prepareModules(cs); err != nil {
		return err
	}
	commits, err := sy.lastCommits(cs.write, cs.existing, since)
	if err != nil {
		return err
	}
	var write []Discovered
	write, sy.force = withMovedModules(cs, sy.mods, withMovedCommits(cs, commits))
	sy.cleared, sy.names, sy.defined = map[string]bool{}, map[string]bool{}, map[string][]string{}
	sy.fields, sy.shapes, sy.modDocs = map[string]bool{}, map[int64]bool{}, map[string]*extract.Doc{}
	sy.res.FilesDeleted = len(cs.gone)
	return sy.writeAll(cs, write, commits)
}

// writeAll deletes the gone files (see deleteGone), parks those to
// re-extract, writes write in chunks extracted ahead (see extractAhead)
// and finishes the sync, with the FTS triggers suspended when the sync
// rewrites most of the database (see suspendFTS).
func (sy *syncer) writeAll(cs changeSet, write []Discovered, commits map[string]gitmeta.Commit) (err error) {
	stop := make(chan struct{})
	ahead := sy.extractAhead(write, cs.existing, stop)
	defer drain(stop, ahead)
	bulk, err := sy.suspendFTS(write, cs.existing)
	if err != nil {
		return err
	}
	if bulk {
		defer func() { err = errors.Join(err, sy.bulk.resumeFTS(sy.s)) }()
	}
	if err := inChunks(sy.s, cs.gone, sy.deleteGone); err != nil {
		return err
	}
	if err := inBatches(sy.s, sy.toReextract(write, cs.existing), guarded(sy, sy.park)); err != nil {
		return err
	}
	for lo := 0; lo < len(write); lo += batchSize {
		chunk := write[lo:min(lo+batchSize, len(write))]
		sy.pre = <-ahead
		err = inBatches(sy.s, chunk, guarded(sy, func(b *store.Batch, d Discovered) error {
			return sy.write(b, d, cs.existing[d.Path], commits)
		}))
		if err != nil {
			return err
		}
	}
	sy.pre = nil
	return sy.s.Batch(sy.finish)
}

// finish restores lone hashed keys, deletes config keys no file defines
// or references any more, deletes modules left without files, updates
// the docs of the modules whose files changed, and
// records the resolution the sync leaves pending, so a crash before it
// runs is recovered by the next run. The
// keys that cleared config files defined and no rewrite accounted for
// (the file is gone) count as changed names.
func (sy *syncer) finish(b *store.Batch) error {
	for _, names := range sy.defined {
		for _, n := range names {
			sy.names[n] = true
		}
	}
	if err := restoreLoneKeys(b, sy.cleared); err != nil {
		return err
	}
	if err := b.DeleteOrphanConfigKeys(sy.repoID); err != nil {
		return err
	}
	if err := b.DeleteModules(sy.mods.stale); err != nil {
		return err
	}
	if err := sy.moduleDocs(b); err != nil {
		return err
	}
	if sy.docs {
		if err := sy.linkEdges(b); err != nil {
			return err
		}
	}
	return sy.settlePending(b)
}

// addSource records the module (row id) of a file the sync rewrote or
// removed.
func (sy *syncer) addSource(id int64, mod resolve.Module) {
	if sy.sources == nil {
		sy.sources = map[int64]store.Module{}
	}
	sy.sources[id] = store.Module{ID: id, RepoID: sy.repoID, Lang: mod.Lang, Path: mod.Path}
}

// park prepares a file for re-extraction: a config file's key uses are
// cleared; code symbols are parked (see store.ParkFileSymbols), keeping
// their ids for the write to match by stable key. Their key bases are
// recorded; their names count as changed only when the write adds,
// removes or reshapes them (see recordChanges).
func (sy *syncer) park(b *store.Batch, f store.File) error {
	if cfg.For(f.Lang) != nil {
		return sy.clearConfig(b, f)
	}
	if parse.IsDoc(f.Lang) {
		_, err := b.ParkFileSymbols(f.ID)
		return err
	}
	if err := sy.remember(b, f); err != nil {
		return err
	}
	parked, err := b.ParkFileSymbols(f.ID)
	for _, c := range parked {
		sy.cleared[stablekey.Base(c.Key, hasSigHash(f.Lang, c.Kind))] = true
	}
	return err
}

// toReextract returns the stored rows of the files in write whose content
// or detected language changed or whose module identity moved: the files
// write re-extracts.
func (sy *syncer) toReextract(write []Discovered, existing map[string]store.File) []store.File {
	var out []store.File
	for _, d := range write {
		if old, ok := existing[d.Path]; ok && (old.Hash != d.Hash || old.Lang != d.Lang || sy.force[d.Path]) {
			out = append(out, old)
		}
	}
	return out
}
