package index

import (
	"github.com/muralx/codedoc/internal/extract/cfg"
	"github.com/muralx/codedoc/internal/gitmeta"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/resolve"
	"github.com/muralx/codedoc/internal/store"
)

// lastCommits reads commits since the last indexed one, then backfills new
// files that no commit in that range touched (for example files that were
// excluded or deleted and came back).
func (sy *syncer) lastCommits(found []Discovered, existing map[string]store.File, since string) (map[string]gitmeta.Commit, error) {
	if !sy.isRepo || (since != "" && since == sy.res.Commit && !hasNew(found, existing)) {
		return map[string]gitmeta.Commit{}, nil
	}
	commits, err := gitmeta.LastCommits(sy.root, sy.res.Commit, since)
	if err != nil || since == "" {
		return commits, err
	}
	return commits, sy.backfill(commits, found, existing)
}

// backfill adds the last commit of new files that commits lacks.
func (sy *syncer) backfill(commits map[string]gitmeta.Commit, found []Discovered, existing map[string]store.File) error {
	var missing []string
	for _, d := range found {
		if _, known := existing[d.Path]; !known {
			if _, ok := commits[d.Path]; !ok {
				missing = append(missing, d.Path)
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if len(missing) > maxPathspec {
		missing = nil
	}
	extra, err := gitmeta.LastCommits(sy.root, sy.res.Commit, "", missing...)
	for p, c := range extra {
		if _, ok := commits[p]; !ok {
			commits[p] = c
		}
	}
	return err
}

// withMovedCommits adds the stored files not in cs.write whose last
// commit changed, so their metadata is refreshed without re-reading them;
// rows of another extractor version (no hash, see checkVersion) wait for
// the sync that re-extracts them.
func withMovedCommits(cs changeSet, commits map[string]gitmeta.Commit) []Discovered {
	inSet := make(map[string]bool, len(cs.write)+len(cs.gone))
	for _, d := range cs.write {
		inSet[d.Path] = true
	}
	for _, f := range cs.gone {
		inSet[f.Path] = true
	}
	write := cs.write
	for p, c := range commits {
		if f, ok := cs.existing[p]; ok && !inSet[p] && f.LastCommit != c.Commit && f.Hash != "" {
			write = append(write, fromRow(f))
		}
	}
	return write
}

func hasNew(found []Discovered, existing map[string]store.File) bool {
	for _, d := range found {
		if _, ok := existing[d.Path]; !ok {
			return true
		}
	}
	return false
}

// write stores one file through b; old is its stored row, the zero File
// when the file is new. New and changed content is extracted and its
// symbols and imports replaced in the same transaction; unchanged content only
// refreshes stat and commit metadata when needed.
func (sy *syncer) write(b *store.Batch, d Discovered, old store.File, commits map[string]gitmeta.Commit) error {
	known := old.ID != 0
	row := store.File{RepoID: sy.repoID, Path: d.Path, Lang: d.Lang, Size: d.Size, Mtime: d.Mtime, Hash: d.Hash,
		Lines: d.Lines, Generated: d.Generated, LastCommit: old.LastCommit, LastCommitAt: old.LastCommitAt,
		ExtractedVersion: old.ExtractedVersion, Title: old.Title}
	if c, ok := commits[d.Path]; ok {
		row.LastCommit, row.LastCommitAt = c.Commit, c.At
	}
	if known && old.Hash == d.Hash && old.Lang == d.Lang && !sy.force[d.Path] {
		sy.res.FilesSkipped++
		row.ID, row.ParseErrors = old.ID, old.ParseErrors
		if row == old {
			return nil
		}
		_, err := b.UpsertFile(row)
		return err
	}
	sy.res.FilesIndexed++
	if !known {
		sy.res.FilesAdded++
	}
	sy.readmeSource(d.Path)
	mod, moduleID, ok := sy.mods.moduleOf(parse.Family(d.Lang), d.Path)
	if ok {
		sy.addSource(moduleID, mod)
	}
	pre, ok := sy.pre[d.Path]
	if !ok {
		pre.ex, pre.err = extractFile(sy.root, sy.name, d, mod)
	}
	ex, err := pre.ex, pre.err
	if err != nil {
		return err
	}
	row.ParseErrors, row.ExtractedVersion, row.Title = ex.parseErrors, extractorVersion, ex.title
	return sy.store(b, row, moduleID, ex)
}

// store writes the row of an extracted file with its symbols, references
// and imports, and records the file and the names whose resolution the
// write may change (see recordChanges).
func (sy *syncer) store(b *store.Batch, row store.File, moduleID int64, ex extracted) error {
	id, err := b.UpsertFile(row)
	if err != nil {
		return err
	}
	sy.extracted = append(sy.extracted, id)
	if parse.IsDoc(row.Lang) {
		return sy.storeDocument(b, id, row.Path, ex)
	}
	if ex.config == nil {
		sy.modDocs[row.Path] = ex.moduleDoc
	}
	if ex.config != nil {
		res, err := b.SyncFileSymbols(id, 0, nil)
		if err != nil {
			return err
		}
		sy.recordChanges(row.Lang, 0, res)
		return sy.storeConfig(b, row.Path, id, ex.config)
	}
	if err := AssignKeys(b, id, row.Path, ex.syms); err != nil {
		return err
	}
	if err := sy.docStates(b, id, row.Path, &ex); err != nil {
		return err
	}
	res, err := b.SyncFileSymbols(id, moduleID, ex.syms)
	if err != nil {
		return err
	}
	sy.recordChanges(row.Lang, moduleID, res)
	return sy.storeReferences(b, row.Lang, id, moduleID, ex, res)
}

// storeReferences writes the references and imports of an extracted
// file. The precise implements edges a SCIP merge left on the file's
// types no longer hold for certain: they are dropped and the package's
// implements edges detected again.
func (sy *syncer) storeReferences(b *store.Batch, lang string, id, moduleID int64, ex extracted,
	res store.FileSymbols) error {
	dropped, err := b.DropPreciseDetected(id)
	if err != nil {
		return err
	}
	sy.markShape(lang, moduleID, dropped > 0)
	if err := b.ReplaceFileReferences(id, referenceRows(ex.refs, ex.syms, res.IDs)); err != nil {
		return err
	}
	return b.ReplaceFileImports(id, ex.imports)
}

// recordChanges records the names whose references may resolve
// differently after a file's symbols were synced: symbols added or
// removed, and kept ones whose kind, arity or constant value changed (a
// config read through a constant reads the key its value names); the
// fields added or removed, the Go, TS and Java fields, and the Go
// package variables, whose declared type may have changed (references
// reach other types through them); and, for Go, the module when its
// interfaces, structs or method sets changed (see goShape). A body-only
// edit records nothing, so references elsewhere keep their edges.
func (sy *syncer) recordChanges(lang string, moduleID int64, res store.FileSymbols) {
	for _, s := range append(res.Added, res.Removed...) {
		sy.names[s.Name] = true
		if s.Kind == "field" {
			sy.fields[s.Name] = true
		}
		sy.markShape(lang, moduleID, goShape(s.Kind))
	}
	for _, c := range res.Changed {
		sy.recordChange(lang, moduleID, c[0], c[1])
	}
}

// recordChange records what one kept symbol's new version changes; see
// recordChanges.
func (sy *syncer) recordChange(lang string, moduleID int64, before, after store.Symbol) {
	old, now := target(lang, before), target(lang, after)
	if old.Kind != now.Kind || !resolve.SameArity(old, now) || before.Value != after.Value {
		sy.names[now.Name] = true
	}
	fam := parse.Family(lang)
	typed := now.Kind == "field" && (fam == "go" || fam == "ts" || fam == "java") || now.Kind == "var" && fam == "go"
	if typed && old.Signature != now.Signature {
		sy.fields[now.Name] = true
	}
	sy.markShape(lang, moduleID, reshaped(before, after))
}

// reshaped reports whether a kept Go symbol's new version changes method
// sets: a kind change involving interfaces, structs or methods, a method
// signature change, or an interface body change (its embedded types).
func reshaped(before, after store.Symbol) bool {
	switch {
	case before.Kind != after.Kind:
		return goShape(before.Kind) || goShape(after.Kind)
	case after.Kind == "method":
		return before.Signature != after.Signature
	}
	return after.Kind == "interface" && before.BodyHash != after.BodyHash
}

// goShape reports whether a Go symbol kind takes part in implements
// detection.
func goShape(kind string) bool { return kind == "interface" || kind == "struct" || kind == "method" }

// markShape records a Go module whose implements edges must be computed
// again.
func (sy *syncer) markShape(lang string, moduleID int64, changed bool) {
	if changed && moduleID != 0 && parse.Family(lang) == "go" {
		sy.shapes[moduleID] = true
	}
}

// target is the part of a symbol resolution compares.
func target(lang string, s store.Symbol) store.Target {
	return store.Target{Kind: s.Kind, Name: s.Name, Qualified: s.Qualified, Signature: s.Signature, Lang: lang}
}

// storeConfig writes the key definitions and references of a config file
// and records the names of the keys whose definition it added or
// removed: code reads of those keys are matched again.
func (sy *syncer) storeConfig(b *store.Batch, path string, fileID int64, res *cfg.Result) error {
	defs, refs := configEntries(res)
	before := map[string]bool{}
	for _, n := range sy.defined[path] {
		before[n] = true
	}
	delete(sy.defined, path)
	now := map[string]bool{}
	for _, d := range defs {
		now[d.Key] = true
		if !before[d.Key] {
			sy.names[d.Key] = true
		}
	}
	for n := range before {
		if !now[n] {
			sy.names[n] = true
		}
	}
	return b.ReplaceFileConfig(fileID, sy.repoID, res.Format, defs, refs)
}

// inBatches applies fn to every item, committing one transaction per
// batchSize items.
func inBatches[T any](s *store.Store, items []T, fn func(*store.Batch, T) error) error {
	return inChunks(s, items, func(b *store.Batch, chunk []T) error {
		for _, it := range chunk {
			if err := fn(b, it); err != nil {
				return err
			}
		}
		return nil
	})
}

// inChunks applies fn to the items batchSize at a time, committing one
// transaction per call.
func inChunks[T any](s *store.Store, items []T, fn func(*store.Batch, []T) error) error {
	for lo := 0; lo < len(items); lo += batchSize {
		chunk := items[lo:min(lo+batchSize, len(items))]
		if err := s.Batch(func(b *store.Batch) error { return fn(b, chunk) }); err != nil {
			return err
		}
		if afterBatch != nil {
			if err := afterBatch(); err != nil {
				return err
			}
		}
	}
	return nil
}

// afterBatch, when set (by tests), runs after each batch inChunks
// commits, so a test can interrupt a sync between its batches.
var afterBatch func() error
