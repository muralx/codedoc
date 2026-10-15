package deps

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

// A mutable source is a package whose content is not fixed by its name
// and version (deps spec section 4): a Go module replaced by a local
// directory, an npm package installed from a directory, a link, a git
// repository or a tarball URL, and a patched package. Its version is
// LocalVersionPrefix and a short hash of its real directory, so only the
// workspaces that resolve to that directory see it, and it is extracted
// again when its files change (see Ecosystem.Fingerprint).

// legacyLocal is the version uses of a replaced Go module had before
// mutable sources were told apart by directory.
const legacyLocal = "local"

// maxFingerprintFiles bounds the files one fingerprint stats.
const maxFingerprintFiles = 20000

// localVersion returns the version of a mutable source installed in
// dir: LocalVersionPrefix and eight hex digits of the hash of its real
// path.
func localVersion(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	sum := sha256.Sum256([]byte(filepath.Clean(dir)))
	return store.LocalVersionPrefix + hex.EncodeToString(sum[:4])
}

// hasLegacyLocal reports whether a use still carries the version every
// replaced Go module once shared: discovery has to run again.
func hasLegacyLocal(uses []store.DepUse) bool {
	for _, u := range uses {
		if u.Version == legacyLocal {
			return true
		}
	}
	return false
}

// mutableRanges are the prefixes of an npm dependency range that names
// a source other than a registry version.
var mutableRanges = []string{"file:", "link:", "portal:", "workspace:", "patch:", "git+", "git:", "git@", "github:",
	"gitlab:", "bitbucket:", "gist:", "http:", "https:", "ssh:", "./", "../", "/", "~/"}

// gitShorthand matches the GitHub shorthand of npm: user/repo, with an
// optional committish.
var gitShorthand = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(#.+)?$`)

// mutableRange reports whether a declared npm range names a directory,
// a link, a git repository or a URL.
func mutableRange(rng string) bool {
	for _, p := range mutableRanges {
		if strings.HasPrefix(rng, p) {
			return true
		}
	}
	return gitShorthand.MatchString(rng)
}

// npmPatches holds the packages a repo patches: by name@version, or by
// name alone for a patch of every version.
type npmPatches map[string]bool

// has reports whether the package version is patched.
func (p npmPatches) has(name, version string) bool { return p[name] || p[name+"@"+version] }

// patchesOf collects the patched packages of the manifest's directory
// and of the repo root: pnpm's patchedDependencies (package.json and
// pnpm-workspace.yaml) and the patch files of patch-package
// (patches/<name>+<version>.patch, '/' written as '+').
func patchesOf(m Manifest) npmPatches {
	out := npmPatches{}
	for _, dir := range uniqueDirs(m.Dir(), m.Root) {
		var pj struct {
			Pnpm struct {
				Patched map[string]string `json:"patchedDependencies"`
			} `json:"pnpm"`
		}
		if readJSON(filepath.Join(dir, "package.json"), &pj) == nil {
			for key := range pj.Pnpm.Patched {
				out[key] = true
			}
		}
		var ws struct {
			Patched map[string]string `yaml:"patchedDependencies"`
		}
		if raw, err := readCapped(filepath.Join(dir, "pnpm-workspace.yaml"), maxManifest); err == nil && yaml.Unmarshal(raw, &ws) == nil {
			for key := range ws.Patched {
				out[key] = true
			}
		}
		entries, _ := os.ReadDir(filepath.Join(dir, "patches"))
		for _, e := range entries {
			if stem, ok := strings.CutSuffix(e.Name(), ".patch"); ok && !e.IsDir() {
				if at := strings.LastIndex(stem, "+"); at > 0 {
					out[strings.ReplaceAll(stem[:at], "+", "/")+"@"+stem[at+1:]] = true
				}
			}
		}
	}
	return out
}

// uniqueDirs returns the distinct directories of dirs, in order.
func uniqueDirs(dirs ...string) []string {
	var out []string
	for _, d := range dirs {
		if !contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

// npmMutable reports whether an installed npm package is a mutable
// source: its range names one, it is installed outside any node_modules
// (a link), or the repo patches it (pnpm marks the directory of a
// patched package with patch_hash).
func npmMutable(rng, dir, name, version string, patches npmPatches) bool {
	return mutableRange(rng) || !inNodeModules(dir) || strings.Contains(dir, "patch_hash") || patches.has(name, version)
}

// stamp renders the size and modification time of the file at path as
// one fingerprint line under the name rel; "" when it is not a regular
// file.
func stamp(path, rel string) string {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return fmt.Sprintf("%s %d %d\n", rel, info.Size(), info.ModTime().UnixNano())
}

// fingerprintOf hashes fingerprint lines, order ignored.
func fingerprintOf(lines []string) string {
	sort.Strings(lines)
	return index.ContentHash([]byte(strings.Join(lines, "")))
}

// Fingerprint stats what extraction of a local Go module would read:
// every source file of its package directories, go.mod and the README.
func (gomod) Fingerprint(p Package) string {
	dirs, err := goPackageDirs(p.Dir)
	if err != nil {
		return ""
	}
	lines := []string{stamp(filepath.Join(p.Dir, "go.mod"), "go.mod")}
	for _, d := range dirs {
		for _, name := range d.files {
			if len(lines) > maxFingerprintFiles {
				break
			}
			rel := filepath.Join(d.rel, name)
			lines = append(lines, stamp(filepath.Join(p.Dir, rel), filepath.ToSlash(rel)))
		}
	}
	return fingerprintOf(append(lines, readmeStamp(p.Dir)))
}

// Fingerprint stats what extraction of a local npm package may read:
// the declaration and TypeScript files below its directories, outside
// node_modules and hidden directories, its package.json and the README.
func (npm) Fingerprint(p Package) string {
	var lines []string
	for _, root := range uniqueDirs(p.Dir, p.root()) {
		lines = append(lines, stamp(filepath.Join(root, "package.json"), root+"/package.json"), readmeStamp(root))
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return nil
			case d.IsDir():
				if path != root && (d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".")) {
					return filepath.SkipDir
				}
			case len(lines) > maxFingerprintFiles:
				return filepath.SkipAll
			case stem(d.Name()) != d.Name():
				lines = append(lines, stamp(path, path))
			}
			return nil
		})
	}
	return fingerprintOf(lines)
}

// readmeStamp is the fingerprint line of the README of a package
// directory, "" when it has none.
func readmeStamp(dir string) string {
	if name := readmeName(dir); name != "" {
		return stamp(filepath.Join(dir, name), dir+"/"+name)
	}
	return ""
}

// digest hashes what an extraction read, so two extractions of the same
// content are told apart from a change: the modules with their docs,
// the import specifiers and the content hash of every file.
func digest(ex *Extraction) string {
	var b strings.Builder
	for _, m := range ex.Modules {
		fmt.Fprintf(&b, "m %s %s %s %s\n", m.Path, m.Identity, m.Name, index.ContentHash([]byte(m.Doc)))
	}
	for _, id := range ex.Identities {
		fmt.Fprintf(&b, "i %s %s\n", id.Identity, id.Path)
	}
	for _, f := range ex.Files {
		fmt.Fprintf(&b, "f %s %s\n", f.Path, f.Hash)
	}
	return index.ContentHash([]byte(b.String()))
}
