package resolve

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// manifests holds the identities declared by a repo's manifests.
type manifests struct {
	identities []Identity
	goMods     map[string]string // directory -> Go module path
	packages   map[string]string // directory -> npm package name
	pyRoots    map[string]string // directory of pyproject.toml or setup.py -> distribution name
}

// manifestReader reads the identity a manifest declares: its language
// family and a reader of the identity ("" when it declares none).
type manifestReader struct {
	lang string
	read func([]byte) string
}

// manifestReaders maps manifest file names to their readers.
var manifestReaders = map[string]manifestReader{
	"go.mod": {"go", goModulePath}, "package.json": {"ts", packageName},
	"pyproject.toml": {"py", pyprojectName}, "setup.py": {"py", setupName},
	"pom.xml": {"java", pomIdentity}, "settings.gradle": {"java", gradleName}, "settings.gradle.kts": {"java", gradleName},
}

// readManifests reads every manifest among files (go.mod, package.json,
// pyproject.toml, setup.py, pom.xml, settings.gradle), in path order.
// Every manifest names the modules below it, but a repo provides each
// (language, identity) once: when several manifests declare it (a copy
// under testdata/, say), the shallowest one is kept.
func readManifests(root string, files []string) (manifests, error) {
	mf := manifests{goMods: map[string]string{}, packages: map[string]string{}, pyRoots: map[string]string{}}
	for _, f := range sortedCopy(files) {
		rd, ok := manifestReaders[path.Base(f)]
		if !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return manifests{}, err
		}
		id := rd.read(data)
		switch rd.lang {
		case "go":
			mf.record(rd.lang, f, id, mf.goMods)
		case "ts":
			mf.record(rd.lang, f, id, mf.packages)
		case "py":
			mf.pyRoots[path.Dir(f)] = id
			mf.record(rd.lang, f, id, nil)
		default:
			mf.record(rd.lang, f, id, nil)
		}
	}
	mf.identities = shallowest(mf.identities)
	return mf, nil
}

// shallowest keeps, per language and identity, the identity whose
// manifest is least deep (the first in path order on ties), in path order.
func shallowest(ids []Identity) []Identity {
	best := map[[2]string]int{}
	for i, id := range ids {
		key := [2]string{id.Lang, id.Identity}
		if j, ok := best[key]; !ok || depthOf(id.Path) < depthOf(ids[j].Path) {
			best[key] = i
		}
	}
	out := make([]Identity, 0, len(best))
	for i, id := range ids {
		if best[[2]string{id.Lang, id.Identity}] == i {
			out = append(out, id)
		}
	}
	return out
}

func depthOf(p string) int { return strings.Count(p, "/") }

func (mf *manifests) record(lang, file, identity string, into map[string]string) {
	if identity == "" {
		return
	}
	if into != nil {
		into[path.Dir(file)] = identity
	}
	mf.identities = append(mf.identities, Identity{Lang: lang, Identity: identity, Path: file})
}

// goModulePath returns the path of the module directive, or "".
func goModulePath(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		line, _, _ = strings.Cut(line, "//")
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			if p, err := strconv.Unquote(fields[1]); err == nil {
				return p
			}
			return fields[1]
		}
	}
	return ""
}

// packageName returns the name field of a package.json, or "".
func packageName(data []byte) string {
	var pkg struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	return pkg.Name
}

func sortedCopy(files []string) []string {
	out := append([]string(nil), files...)
	sort.Strings(out)
	return out
}
