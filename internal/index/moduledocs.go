package index

import (
	"errors"
	"path"
	"sort"

	"github.com/muralx/codedoc/internal/docs"
	"github.com/muralx/codedoc/internal/store"
)

// moduleDocs updates the doc of every module whose files the sync
// extracted or deleted (spec 7.2 and 10 sources, see docs.Lookup.Find).
func (sy *syncer) moduleDocs(b *store.Batch) error {
	stale := make(map[int64]bool, len(sy.mods.stale))
	for _, id := range sy.mods.stale {
		stale[id] = true
	}
	ids := make([]int64, 0, len(sy.sources))
	for id := range sy.sources {
		if !stale[id] {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	look := docs.Lookup{Root: sy.root, Known: sy.modDocs}
	for _, id := range ids {
		m, err := b.ModuleByID(id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if err := refreshModuleDoc(b, look, m, &docs.ModuleDoc{Text: m.DocText, Source: m.DocSource}); err != nil {
			return err
		}
	}
	return nil
}

// refreshModuleDoc looks a module's doc up and stores it when it moved;
// cur is the stored doc (nil looks everywhere, see docs.Lookup.Find).
func refreshModuleDoc(b *store.Batch, look docs.Lookup, m store.Module, cur *docs.ModuleDoc) error {
	d, err := look.Find(docs.ModuleRef{Path: m.Path, Lang: m.Lang, Kind: m.Kind}, cur)
	if err != nil {
		return err
	}
	st := string(d.State())
	if d.Text == m.DocText && d.Source == m.DocSource && st == m.DocState {
		return nil
	}
	return b.SetModuleDoc(m.ID, d.Text, d.Source, st)
}

// readmeSource records the TS/JS module of a README.md the sync wrote or
// deleted: README.md is a module doc source there (see docs.Lookup.Find).
func (sy *syncer) readmeSource(p string) {
	if path.Base(p) != "README.md" {
		return
	}
	if mod, id, ok := sy.mods.moduleOf("ts", p); ok && mod.Path == path.Dir(p) {
		sy.addSource(id, mod)
	}
}
