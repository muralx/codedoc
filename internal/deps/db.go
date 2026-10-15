package deps

import (
	"errors"
	"strings"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/docs"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/resolve"
	"github.com/muralx/codedoc/internal/stablekey"
	"github.com/muralx/codedoc/internal/store"
)

// truncatedKey prefixes the meta key that flags a package version whose
// extraction a limit cut short.
const truncatedKey = "dep_truncated:"

// extractionVersion names the semantics of the rows of a package
// version; versionKey prefixes the meta key recording it per package. A
// package written under another version (version 1 wrote no key and no
// extends edges, version 2 no export graph) still answers queries, and is extracted again by the
// next full Sync: codedoc index, codedoc deps index or the daemon's
// catch-up. Bump it whenever the rows of the same package would differ.
const (
	extractionVersion = "3"
	versionKey        = "dep_extraction:"
)

// fingerprintKey and digestKey prefix the meta keys of a mutable
// source: the stamp of its files when it was last read and the hash of
// what was read (see Ecosystem.Fingerprint and digest).
const (
	fingerprintKey = "dep_fingerprint:"
	digestKey      = "dep_digest:"
)

// packageKeys are the prefixes of the meta keys a package version owns.
var packageKeys = []string{truncatedKey, versionKey, fingerprintKey, digestKey, modulesKey, resolve.DepExportsKey}

// DB is the shared dependency database, deps.db: an ordinary store in
// which every package version is one repo row named by its package id
// (deps spec sections 2 and 3). Readers use Store without locking;
// writers serialize on the deps.lock file next to it, held only while
// one package is written or pruned.
type DB struct {
	s    *store.Store
	path string
}

// Open opens or creates the dependency database at path (see
// config.Config.DepsDBPath).
func Open(path string) (*DB, error) {
	s, err := store.Open(path)
	if err != nil {
		return nil, err
	}
	return &DB{s: s, path: path}, nil
}

// Close releases the database.
func (d *DB) Close() error { return d.s.Close() }

// Store returns the store to read packages with: symbols, modules and
// files of a package are those of its repo row (see RepoIDs).
func (d *DB) Store() *store.Store { return d.s }

// lock takes the write lock of the database, the file deps.lock.
func (d *DB) lock() (func() error, error) {
	return store.LockPath(strings.TrimSuffix(d.path, ".db"))
}

// Has reports whether a package version is indexed.
func (d *DB) Has(id string) (bool, error) {
	_, err := d.s.RepoByName(config.DepsWorkspace, id)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// Current reports whether a package version is indexed with the rows
// this build writes (see extractionVersion).
func (d *DB) Current(id string) (bool, error) {
	has, err := d.Has(id)
	if err != nil || !has {
		return false, err
	}
	v, _, err := d.s.Meta(versionKey + id)
	return v == extractionVersion, err
}

// RepoIDs returns the repo row id of each of the package ids that is
// indexed: the id that scopes store lookups (SymbolsByName,
// ModulesByRepo, FilesByRepo) to one package version.
func (d *DB) RepoIDs(ids []string) (map[string]int64, error) {
	out := make(map[string]int64, len(ids))
	for _, id := range ids {
		row, err := d.s.RepoByName(config.DepsWorkspace, id)
		switch {
		case errors.Is(err, store.ErrNotFound):
		case err != nil:
			return nil, err
		default:
			out[id] = row.ID
		}
	}
	return out, nil
}

// PackageInfo describes one indexed package version.
type PackageInfo struct {
	// ID is the package id; RepoID its repo row in deps.db.
	ID     string
	RepoID int64
	// Symbols counts its symbol rows.
	Symbols int
	// Truncated reports that a limit cut its extraction short.
	Truncated bool
	// IndexedAt is when it was indexed, in Unix seconds.
	IndexedAt int64
	// Only lists the modules held of an ambient package indexed
	// partially (see nodeTypes); nil for a whole package.
	Only []string
	// Embedded counts its documented symbols and modules with a current
	// vector, Embeddable all of them: what search --deps finds by meaning.
	Embedded, Embeddable int
}

// Packages returns every indexed package version ordered by id.
func (d *DB) Packages() ([]PackageInfo, error) {
	repos, err := d.s.Repos()
	if err != nil {
		return nil, err
	}
	counts, err := d.s.SymbolCounts()
	if err != nil {
		return nil, err
	}
	out := make([]PackageInfo, len(repos))
	for i, r := range repos {
		_, truncated, err := d.s.Meta(truncatedKey + r.Name)
		if err != nil {
			return nil, err
		}
		out[i] = PackageInfo{ID: r.Name, RepoID: r.ID, Symbols: counts[r.ID], Truncated: truncated, IndexedAt: r.IndexedAt}
		out[i].Only, _ = d.held(r.Name)
		if out[i].Embedded, out[i].Embeddable, err = index.APIVectorCounts(d.s, r.ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// fingerprint returns the stamp stored with a mutable source, "" when
// there is none.
func (d *DB) fingerprint(id string) string {
	v, _, _ := d.s.Meta(fingerprintKey + id)
	return v
}

// Write stores the extraction of a package version in one transaction
// under the write lock, so a failure leaves no rows; the rows an older
// extraction version left of it are replaced, and so are those of a
// mutable source whose content changed (another digest) or of a partial
// extraction that lacks modules of this one (see covers). It reports
// false when the rows were left alone: another process indexed the
// version first, or a mutable source was read again and found the same,
// in which case only its stamp is stored.
func (d *DB) Write(id ID, ex *Extraction) (written bool, err error) {
	unlock, err := d.lock()
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	name := id.String()
	current, err := d.holds(name, ex.Only)
	if err != nil {
		return false, err
	}
	if have, _, _ := d.s.Meta(digestKey + name); current && (ex.Digest == "" || have == ex.Digest) {
		if ex.Digest == "" || d.fingerprint(name) == ex.Fingerprint {
			return false, nil
		}
		return false, d.s.Batch(func(b *store.Batch) error { return b.SetMeta(fingerprintKey+name, ex.Fingerprint) })
	}
	old, err := d.s.RepoByName(config.DepsWorkspace, name)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	return true, d.s.Batch(func(b *store.Batch) error {
		if old.ID != 0 {
			if err := b.DeleteRepo(old.ID); err != nil {
				return err
			}
		}
		return writePackage(b, id, ex)
	})
}

// writePackage writes the repo row of a package version with its
// modules, identities, files and symbols.
func writePackage(b *store.Batch, id ID, ex *Extraction) error {
	name := id.String()
	repoID, err := b.UpsertRepo(store.Repo{Workspace: config.DepsWorkspace, Name: name, Path: name, VCS: id.Eco,
		Commit: id.Version})
	if err != nil {
		return err
	}
	modIDs, err := writeModules(b, repoID, ex.Modules)
	if err != nil {
		return err
	}
	if err := b.ReplaceRepoIdentities(repoID, ex.Identities); err != nil {
		return err
	}
	supers := &superIndex{byName: map[string][]written{}}
	for _, f := range ex.Files {
		if err := writeFile(b, repoID, name, f, ex.Modules[f.Module], modIDs[f.Module], supers); err != nil {
			return err
		}
	}
	if err := linkSupers(b, ex.Files, supers); err != nil {
		return err
	}
	if err := setPackageMeta(b, name, ex); err != nil {
		return err
	}
	return b.SetRepoIndexedAt(repoID, time.Now().Unix())
}

// setPackageMeta stores the meta keys of a package version (see
// packageKeys), deleting those the extraction has no value for.
func setPackageMeta(b *store.Batch, name string, ex *Extraction) error {
	if err := setTruncated(b, name, ex.Truncated); err != nil {
		return err
	}
	if err := setExports(b, name, ex.Exports); err != nil {
		return err
	}
	for key, value := range map[string]string{fingerprintKey: ex.Fingerprint, digestKey: ex.Digest,
		modulesKey: encodeModules(name, ex.Only)} {
		if err := setOrDelete(b, key+name, value); err != nil {
			return err
		}
	}
	return b.SetMeta(versionKey+name, extractionVersion)
}

// setTruncated flags or clears the truncation of a package version.
func setTruncated(b *store.Batch, name string, truncated bool) error {
	if truncated {
		return b.SetMeta(truncatedKey+name, "1")
	}
	return b.DeleteMeta(truncatedKey + name)
}

// setOrDelete stores a meta value, or deletes the key for an empty one.
func setOrDelete(b *store.Batch, key, value string) error {
	if value == "" {
		return b.DeleteMeta(key)
	}
	return b.SetMeta(key, value)
}

// setExports stores or clears the export graph of a package version
// (see resolve.DepExports).
func setExports(b *store.Batch, name string, exports *resolve.DepExports) error {
	if exports == nil {
		return b.DeleteMeta(resolve.DepExportsKey + name)
	}
	return b.SetMeta(resolve.DepExportsKey+name, exports.Encode())
}

// writeModules writes the module rows, the root first and the others
// below it, with their docs, and returns their row ids in order.
func writeModules(b *store.Batch, repoID int64, mods []Module) ([]int64, error) {
	ids := make([]int64, len(mods))
	for i, m := range mods {
		row := store.Module{RepoID: repoID, Path: m.Path, Lang: m.Lang, Kind: m.Kind, Identity: m.Identity, Name: m.Name}
		if i > 0 {
			row.ParentID = ids[0]
		}
		id, err := b.UpsertModule(row)
		if err != nil {
			return nil, err
		}
		ids[i] = id
		if m.Doc == "" {
			continue
		}
		if err := b.SetModuleDoc(id, m.Doc, m.DocSource, string(docs.OK)); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// writeFile writes one file row and its symbols, and records its
// containers in supers. Keys carry the package
// id as repo and the module identity (spec 5.1). Go files of one package
// may declare a name twice under build constraints, which the collision
// rule of the index settles; TypeScript keys hold the file path and
// never collide.
func writeFile(b *store.Batch, repoID int64, pkg string, f File, mod Module, moduleID int64, supers *superIndex) error {
	fileID, err := b.UpsertFile(store.File{RepoID: repoID, Path: f.Path, Lang: f.Lang, Size: f.Size, Mtime: f.Mtime,
		Hash: f.Hash, Lines: f.Lines, ParseErrors: f.ParseErrors})
	if err != nil {
		return err
	}
	family := parse.Family(f.Lang)
	syms := make([]store.Symbol, len(f.Symbols))
	for i, sy := range f.Symbols {
		syms[i] = index.SymbolRow(stablekey.New(pkg, family, mod.Identity, sy.Qualified, sy.SigHash), sy)
		syms[i].DocState = string(docs.Missing)
		if sy.Doc != nil {
			syms[i].DocState = string(docs.OK)
		}
	}
	if family == "go" {
		if err := index.AssignKeys(b, fileID, f.Path, syms); err != nil {
			return err
		}
	}
	res, err := b.SyncFileSymbols(fileID, moduleID, syms)
	keys := make([]string, len(syms))
	for i, sy := range syms {
		keys[i] = sy.StableKey
	}
	supers.add(f, fileID, res.IDs, keys)
	return err
}
