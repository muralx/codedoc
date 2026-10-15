package index

import (
	"github.com/muralx/codedoc/internal/extract/cfg"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/stablekey"
	"github.com/muralx/codedoc/internal/store"
)

// deleteGone deletes the rows of one batch of files that are gone, all
// of them with one statement per table (see store.Batch.DeleteFiles), so
// a deleted tree costs a few statements per batchSize files. Each file's
// module is recorded as a source of changed module edges and docs (see
// noteGone), and the key bases, names and Go shapes of the symbols of
// its code files as changed (see forget). The sections of a document are
// only deleted: nothing resolves to them by name and their keys never
// collide.
func (sy *syncer) deleteGone(b *store.Batch, files []store.File) error {
	if err := sy.guardPending(b); err != nil {
		return err
	}
	ids := make([]int64, len(files))
	var code []store.File
	for i, f := range files {
		ids[i] = f.ID
		isCode, err := sy.noteGone(b, f)
		if err != nil {
			return err
		}
		if isCode {
			code = append(code, f)
		}
	}
	if err := sy.remember(b, code...); err != nil {
		return err
	}
	gone, err := b.DeleteFiles(ids)
	if err != nil {
		return err
	}
	for _, f := range code {
		sy.forget(f, gone[f.ID])
	}
	return nil
}

// noteGone records what the deletion of f changes besides its symbols:
// its module as a source of changed module edges and docs, the keys a
// config file defined, and that the repo's documents changed. It reports
// whether f is a code file.
func (sy *syncer) noteGone(b *store.Batch, f store.File) (bool, error) {
	if mod, id, ok := sy.mods.moduleOf(parse.Family(f.Lang), f.Path); ok {
		sy.addSource(id, mod)
		sy.modDocs[f.Path] = nil
	}
	sy.readmeSource(f.Path)
	switch {
	case cfg.For(f.Lang) != nil:
		return false, sy.clearConfig(b, f)
	case parse.IsDoc(f.Lang):
		sy.docs = true
		return false, nil
	}
	return true, nil
}

// forget records the key bases and names of the deleted symbols of a
// code file, and its Go module when they change its method sets.
func (sy *syncer) forget(f store.File, gone []store.Cleared) {
	_, moduleID, _ := sy.mods.moduleOf(parse.Family(f.Lang), f.Path)
	for _, c := range gone {
		sy.cleared[stablekey.Base(c.Key, hasSigHash(f.Lang, c.Kind))] = true
		sy.names[c.Name] = true
		sy.markShape(f.Lang, moduleID, goShape(c.Kind))
	}
}

// clearConfig deletes a config file's key uses and records the keys it
// defined.
func (sy *syncer) clearConfig(b *store.Batch, f store.File) error {
	names, err := b.ClearFileConfig(f.ID)
	sy.defined[f.Path] = names
	return err
}
