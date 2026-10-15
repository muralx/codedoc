package deps

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"

	"github.com/muralx/codedoc/internal/store"
)

func init() { register(gomod{}) }

// gomod is the ecosystem of go.mod manifests and the Go module cache.
type gomod struct{}

// Name is the id segment of Go modules.
func (gomod) Name() string { return "go" }

// Manifest is go.mod.
func (gomod) Manifest() string { return "go.mod" }

// Tracked lists go.sum, which changes with the required versions.
func (gomod) Tracked() []string { return []string{"go.sum"} }

// ModCacheEnv names the variable that overrides the Go module cache
// codedoc reads, before GOMODCACHE; tests point it at a fixture.
const ModCacheEnv = "CODEDOC_GOMODCACHE"

// Discover resolves the direct requirements of a go.mod, and the
// indirect ones named in m.Include (recorded without a declared
// version, like every included package), to their directories in the
// module cache. A requirement replaced by a local directory uses that
// directory with a local version (see localVersion), a mutable source;
// one replaced by another module that module's directory and version. A module a workspace repo provides, or replaced
// by a directory inside one, is skipped.
func (e gomod) Discover(m Manifest) ([]store.DepUse, error) {
	full := filepath.Join(m.Root, filepath.FromSlash(m.Path))
	raw, err := readCapped(full, maxManifest)
	if err != nil {
		return nil, err
	}
	f, err := modfile.Parse(full, raw, nil)
	if err != nil {
		return nil, err
	}
	var uses []store.DepUse
	for _, req := range f.Require {
		if req.Indirect && !contains(m.Include, req.Mod.Path) {
			continue
		}
		u := e.resolve(m, req.Mod, replacement(f, req.Mod))
		if req.Indirect {
			u.Declared = ""
		}
		uses = append(uses, u)
	}
	return uses, nil
}

// replacement returns the replace directive that applies to a required
// module version, nil when none does; one naming the version wins over
// one for every version.
func replacement(f *modfile.File, mod module.Version) *modfile.Replace {
	var general *modfile.Replace
	for _, r := range f.Replace {
		switch {
		case r.Old.Path != mod.Path:
		case r.Old.Version == mod.Version:
			return r
		case r.Old.Version == "":
			general = r
		}
	}
	return general
}

// resolve finds the directory of one required module.
func (e gomod) resolve(m Manifest, mod module.Version, rep *modfile.Replace) store.DepUse {
	u := store.DepUse{Manifest: m.Path, Eco: e.Name(), Name: mod.Path, Declared: mod.Version, State: store.DepMissing}
	if m.Provided != nil && m.Provided("go", mod.Path) {
		u.State = store.DepSkipped
		return u
	}
	target := mod
	switch {
	case rep != nil && rep.New.Version == "":
		u.Dir = rep.New.Path
		if !filepath.IsAbs(u.Dir) {
			u.Dir = filepath.Join(m.Dir(), filepath.FromSlash(u.Dir))
		}
		if m.InWorkspace != nil && m.InWorkspace(u.Dir) {
			u.State = store.DepSkipped
			return u
		}
		u.Version = localVersion(u.Dir)
	case rep != nil:
		target = rep.New
	}
	if u.Version == "" {
		u.Version, u.Dir = target.Version, modCacheDir(target)
	}
	if info, err := os.Stat(u.Dir); err != nil || !info.IsDir() {
		u.Version = ""
		return u
	}
	u.State = store.DepPending
	return u
}

// modCacheDir returns where the module cache holds a module version:
// <cache>/<escaped path>@<escaped version>, upper-case letters written
// as '!' and the lower-case letter.
func modCacheDir(mod module.Version) string {
	p, err := module.EscapePath(mod.Path)
	if err != nil {
		p = mod.Path
	}
	v, err := module.EscapeVersion(mod.Version)
	if err != nil {
		v = mod.Version
	}
	return filepath.Join(modCache(), filepath.FromSlash(p)+"@"+v)
}

// goEnvModCache asks the go command for its module cache, once.
var goEnvModCache = sync.OnceValue(func() string {
	out, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
})

// modCache returns the Go module cache: CODEDOC_GOMODCACHE, else
// GOMODCACHE, else what go env reports, else ~/go/pkg/mod.
func modCache() string {
	for _, name := range []string{ModCacheEnv, "GOMODCACHE"} {
		if p := os.Getenv(name); p != "" {
			return p
		}
	}
	if p := goEnvModCache(); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "go", "pkg", "mod")
}
