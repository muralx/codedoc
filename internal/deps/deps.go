// Package deps indexes the third-party packages a workspace depends on
// (deps spec): it discovers the package versions the manifests of a repo
// resolve to, records them as dependency uses in the workspace database,
// and stores the public API of each version once in the shared deps.db.
package deps

import (
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/resolve"
	"github.com/muralx/codedoc/internal/store"
)

// ID identifies a package version: eco/name@version (deps spec section
// 3). Its string is the name of the package's repo row in deps.db and
// the repo segment of the stable keys of its symbols.
type ID struct {
	Eco, Name, Version string
}

// String renders eco/name@version.
func (id ID) String() string { return id.Eco + "/" + id.Name + "@" + id.Version }

// ParseID parses eco/name@version. The name may hold slashes and start
// with '@' (a scoped npm package); the version follows the last '@'.
func ParseID(s string) (ID, bool) {
	eco, rest, ok := strings.Cut(s, "/")
	at := strings.LastIndex(rest, "@")
	if !ok || eco == "" || at <= 0 || at == len(rest)-1 || strings.Contains(s, ":") {
		return ID{}, false
	}
	return ID{Eco: eco, Name: rest[:at], Version: rest[at+1:]}, true
}

// idOf returns the package id of a dependency use.
func idOf(u store.DepUse) ID { return ID{Eco: u.Eco, Name: u.PackageName(), Version: u.Version} }

// Ecosystem is one package manager's view of dependencies: where its
// manifests declare them, where they are installed and how their public
// API is read. Discovery and extraction go through it, so a new
// ecosystem (Python, Java) is one more implementation.
type Ecosystem interface {
	// Name is the ecosystem segment of package ids: npm, go.
	Name() string
	// Manifest is the base name of the files that declare dependencies.
	Manifest() string
	// Tracked lists the lock files and install markers, relative to a
	// manifest's directory, whose change means the installed versions
	// may have changed.
	Tracked() []string
	// Discover resolves the packages one manifest declares to their
	// installed versions; the state of a use is pending, missing or
	// skipped. It also returns those of m.Include it can find, with an
	// empty Declared.
	Discover(m Manifest) ([]store.DepUse, error)
	// Extract reads the public API of an installed package version.
	Extract(p Package, lim Limits) (*Extraction, error)
	// Fingerprint returns a cheap stamp of the files Extract would read
	// of a mutable source (names, sizes and modification times, no
	// content): while it stays the same the package is not read again.
	Fingerprint(p Package) string
}

// Manifest is the input of discovery for one manifest file.
type Manifest struct {
	// Root is the absolute directory of the repo; Path the manifest's
	// path relative to it, slash separated.
	Root, Path string
	// Include holds the deps.include names: packages resolved like
	// declared ones although only transitive.
	Include []string
	// InWorkspace reports whether an absolute directory lies inside a
	// repo of the workspace.
	InWorkspace func(dir string) bool
	// Provided reports whether a workspace repo provides a package name
	// (a module identity) of a language family.
	Provided func(lang, name string) bool
}

// Dir returns the absolute directory of the manifest.
func (m Manifest) Dir() string {
	return filepath.Join(m.Root, filepath.FromSlash(path.Dir(m.Path)))
}

// Package is an installed package version to extract.
type Package struct {
	ID ID
	// Dir is the absolute directory of the package; TypesDir, when set,
	// the directory of the @types package holding its declarations.
	Dir, TypesDir string
	// Only, when not nil, names the modules of an ambient package to
	// read instead of the whole package (see nodeTypes).
	Only []string
}

// root is the directory the package's indexed files are relative to.
func (p Package) root() string {
	if p.TypesDir != "" {
		return p.TypesDir
	}
	return p.Dir
}

// Limits bound what is read of one package (deps spec section 5).
type Limits struct {
	// Files is the most files read; Bytes the most bytes.
	Files int
	Bytes int64
}

// DefaultLimits are the limits of the spec: 600 files and 12 MB.
var DefaultLimits = Limits{Files: 600, Bytes: 12 << 20}

// budget counts what an extraction read against its limits.
type budget struct {
	lim       Limits
	files     int
	bytes     int64
	truncated bool
}

// take accounts for one more file of size bytes and reports whether it
// fits; the first file that does not marks the extraction truncated.
func (b *budget) take(size int64) bool {
	if b.truncated || b.files+1 > b.lim.Files || b.bytes+size > b.lim.Bytes {
		b.truncated = true
		return false
	}
	b.files++
	b.bytes += size
	return true
}

// Extraction is the public API read from one package version.
type Extraction struct {
	// Modules holds the root module first: the package itself, whose doc
	// is the README.
	Modules []Module
	Files   []File
	// Identities are the import specifiers the package answers to, each
	// with the path of the module it names.
	Identities []store.RepoIdentity
	// Truncated reports that a limit cut the extraction short.
	Truncated bool
	// Fingerprint and Digest are set for a mutable source only: the
	// stamp of its files taken before it was read (see
	// Ecosystem.Fingerprint) and the hash of what was read (see digest).
	Fingerprint, Digest string
	// Exports is the re-export graph of an npm package's declaration
	// files (see resolve.DepExports); nil for other ecosystems.
	Exports *resolve.DepExports
	// Only names the modules read of an ambient package extracted
	// partially (see Package.Only); nil for a whole package.
	Only []string
}

// Module is one module of a package: an npm entry point or a Go package.
type Module struct {
	// Path is unique in the package; "." for the root module.
	Path, Lang, Kind, Identity string
	// Name is the package name a Go package's files declare.
	Name string
	// Doc is the module doc; DocSource the file it comes from, relative
	// to the package.
	Doc, DocSource string
}

// File is one extracted file of a package.
type File struct {
	// Path is relative to the package root, slash separated.
	Path, Lang  string
	Size, Mtime int64
	Hash        string
	Lines       int
	ParseErrors int
	// Module indexes Extraction.Modules.
	Module  int
	Symbols []extract.Symbol
	// Supers are the supertypes the declarations of the file extend or
	// embed.
	Supers []Super
}

// ecosystems holds the registered ecosystems by name.
var ecosystems = map[string]Ecosystem{}

// register adds an ecosystem; the package's init functions call it.
func register(e Ecosystem) { ecosystems[e.Name()] = e }

// Ecosystems returns the registered ecosystems ordered by name.
func Ecosystems() []Ecosystem {
	out := make([]Ecosystem, 0, len(ecosystems))
	for _, e := range ecosystems {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// IsTrigger reports whether a repo-relative path names a manifest or a
// lock file of a registered ecosystem: a file whose change calls for
// dependency discovery, whether or not the file itself is indexed.
func IsTrigger(rel string) bool {
	base := path.Base(rel)
	for _, e := range ecosystems {
		if base == e.Manifest() {
			return true
		}
		for _, t := range e.Tracked() {
			if !strings.Contains(t, "/") && base == t {
				return true
			}
		}
	}
	return false
}
