package index

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/muralx/codedoc/internal/resolve"
	"github.com/muralx/codedoc/internal/store"
)

// pendingKey is the meta key holding the resolution a sync wrote files
// for but has not finished: a crash between writing and resolving leaves
// it set, and the next Ensure (or index run) resolves it first.
const pendingKey = "pending_resolution"

// pending is the resolution scope of one or more syncs (see resolveAfter):
// everything when Full, else the imports of ImportFiles with the module
// edges of ImportModules, then the references of Files, those named one
// of Names or going through a field named one of Fields, and the Go
// implements edges of Modules.
type pending struct {
	Full          bool           `json:"full,omitempty"`
	ImportFiles   []int64        `json:"import_files,omitempty"`
	ImportModules []store.Module `json:"import_modules,omitempty"`
	Files         []int64        `json:"files,omitempty"`
	Names         []string       `json:"names,omitempty"`
	Fields        []string       `json:"fields,omitempty"`
	Modules       []int64        `json:"modules,omitempty"`
}

// scope returns what a sync leaves to resolve: everything when the
// repo's modules or identities changed (imports anywhere may resolve
// differently), else the files it extracted, the module edges of the
// modules whose files it extracted or deleted, the names it added,
// removed or reshaped, the fields whose type it changed, and the modules
// whose Go method sets it changed.
func (sy *syncer) scope() pending {
	if sy.mods.changed {
		return pending{Full: true}
	}
	p := pending{ImportFiles: sy.extracted, Files: sy.extracted}
	for _, m := range sy.sources {
		p.ImportModules = append(p.ImportModules, m)
	}
	p.Names, p.Fields, p.Modules = keys(sy.names), keys(sy.fields), keys(sy.shapes)
	return p
}

// merge adds o to p.
func (p *pending) merge(o pending) {
	if p.Full = p.Full || o.Full; p.Full {
		*p = pending{Full: true}
		return
	}
	p.ImportFiles = union(p.ImportFiles, o.ImportFiles)
	p.Files = union(p.Files, o.Files)
	p.Names = union(p.Names, o.Names)
	p.Fields = union(p.Fields, o.Fields)
	p.Modules = union(p.Modules, o.Modules)
	seen := map[int64]bool{}
	for _, m := range p.ImportModules {
		seen[m.ID] = true
	}
	for _, m := range o.ImportModules {
		if !seen[m.ID] {
			seen[m.ID] = true
			p.ImportModules = append(p.ImportModules, m)
		}
	}
}

// savePending merges p into the pending resolution stored through b.
func savePending(b *store.Batch, p pending) error {
	old, ok, err := loadPending(b)
	if err != nil {
		return err
	}
	if ok {
		old.merge(p)
		p = old
	}
	return setPending(b, p)
}

// setPending stores p as the pending resolution.
func setPending(b *store.Batch, p pending) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return b.SetMeta(pendingKey, string(raw))
}

// guarded wraps fn so the first batch it runs in records a full
// resolution as pending (see syncer.guardPending).
func guarded[T any](sy *syncer, fn func(*store.Batch, T) error) func(*store.Batch, T) error {
	return func(b *store.Batch, it T) error {
		if err := sy.guardPending(b); err != nil {
			return err
		}
		return fn(b, it)
	}
}

// guardPending records, in the first batch a sync commits before finish,
// a full resolution as pending, keeping the resolution pending before it
// in sy.guard. A sync spanning several transactions that stops between
// them has written rows whose resolution its scope never recorded (a
// deleted method's Go implements edges, the names a rewrite changed):
// the next run resolves everything instead. finish narrows it back to
// the sync's scope (see settlePending).
func (sy *syncer) guardPending(b *store.Batch) error {
	if sy.guard != nil {
		return nil
	}
	prior, _, err := loadPending(b)
	if err != nil {
		return err
	}
	if err := savePending(b, pending{Full: true}); err != nil {
		return err
	}
	sy.guard = &prior
	return nil
}

// settlePending records the resolution the sync leaves pending: its
// scope merged into the resolution pending before it, replacing the full
// one guardPending recorded.
func (sy *syncer) settlePending(b *store.Batch) error {
	if sy.guard == nil {
		return savePending(b, sy.scope())
	}
	p := *sy.guard
	p.merge(sy.scope())
	return setPending(b, p)
}

// metaReader reads meta keys; *store.Store and *store.Batch are both.
type metaReader interface {
	Meta(key string) (string, bool, error)
}

func loadPending(m metaReader) (pending, bool, error) {
	raw, ok, err := m.Meta(pendingKey)
	if err != nil || !ok {
		return pending{}, false, err
	}
	var p pending
	return p, true, json.Unmarshal([]byte(raw), &p)
}

// clearPending removes the pending resolution; it runs in the
// transaction that finishes resolving it.
func clearPending(b *store.Batch) error { return b.DeleteMeta(pendingKey) }

// resolvePending resolves the pending resolution, if any, and clears it
// in the same transaction as the reference resolution. The caller holds
// the write lock.
func resolvePending(s *store.Store, opts Options) error {
	p, ok, err := loadPending(s)
	if err != nil || !ok {
		return err
	}
	if p.Full, err = mostFiles(s, p); err != nil {
		return err
	}
	if p.Full {
		if err := resolve.Imports(s, opts.RepoOrder, nil); err != nil {
			return err
		}
		return resolve.ReferencesThen(s, nil, clearPending)
	}
	if err := resolve.Imports(s, opts.RepoOrder, &resolve.Scope{Files: p.ImportFiles, Modules: p.ImportModules}); err != nil {
		return err
	}
	return resolve.ReferencesThen(s, &resolve.RefScope{Files: p.Files, Names: p.Names, Fields: p.Fields,
		Modules: p.Modules}, clearPending)
}

// fullMinFiles is the fewest extracted files for which a pending
// resolution covering at least half the database's files resolves in
// full: one pass over everything is then cheaper than looking up what
// each touched name affects.
var fullMinFiles = batchSize

// mostFiles reports whether p resolves in full: it says so, or it covers
// at least fullMinFiles files and half of all files.
func mostFiles(s *store.Store, p pending) (bool, error) {
	if p.Full || len(p.Files) < fullMinFiles {
		return p.Full, nil
	}
	total, err := s.FileTotal()
	return 2*len(p.Files) >= total, err
}

// ResolvePending resolves what the syncs since the last resolution left
// pending (see syncer.scope): everything after a first index or when
// modules or identities changed, else only the files and names they
// touched; everything, too, when the config order of the repos changed
// since the last call. It holds the store's write lock.
func ResolvePending(s *store.Store, opts Options) (err error) {
	unlock, err := s.WriteLock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	if err := noteRepoOrder(s, opts.RepoOrder); err != nil {
		return err
	}
	return resolvePending(s, opts)
}

// repoOrderKey is the meta key holding the config order of the repos
// ResolvePending last ran with.
const repoOrderKey = "repo_order"

// noteRepoOrder records order, and a full resolution as pending when it
// differs from the recorded one: an import identity two repos share
// resolves to the first of them in config order.
func noteRepoOrder(s *store.Store, order []string) error {
	if len(order) == 0 {
		return nil
	}
	want := strings.Join(order, "\n")
	have, ok, err := s.Meta(repoOrderKey)
	if err != nil || (ok && have == want) {
		return err
	}
	return s.Batch(func(b *store.Batch) error {
		if ok {
			if err := savePending(b, pending{Full: true}); err != nil {
				return err
			}
		}
		return b.SetMeta(repoOrderKey, want)
	})
}

// finishPending resolves a resolution left pending by an interrupted run
// before anything else, under the write lock.
func finishPending(s *store.Store, opts Options) (err error) {
	if _, ok, err := loadPending(s); err != nil || !ok {
		return err
	}
	unlock, err := s.WriteLock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return resolvePending(s, opts)
}

func keys[K comparable](m map[K]bool) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// union returns a with the elements of b it lacks appended.
func union[T comparable](a, b []T) []T {
	seen := make(map[T]bool, len(a))
	for _, x := range a {
		seen[x] = true
	}
	for _, x := range b {
		if !seen[x] {
			seen[x] = true
			a = append(a, x)
		}
	}
	return a
}

// QueueResolution adds the references of files and the Go implements
// edges of modules to the pending resolution through b, so the next
// ResolvePending (or Ensure) resolves them again: a SCIP merge queues the
// references whose precise edges it dropped.
func QueueResolution(b *store.Batch, files, modules []int64) error {
	return savePending(b, pending{Files: files, Modules: modules})
}
