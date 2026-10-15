package deps_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/deps"
	"github.com/muralx/codedoc/internal/store"
	"github.com/muralx/codedoc/internal/testutil"
)

// localRE matches "version state" of a pending mutable source.
var localRE = regexp.MustCompile(`^local-[0-9a-f]{8} pending$`)

// sourceSpace is newSpace for a workspace of one repo made of files;
// prepare, when set, changes the repo on disk before it is indexed. db
// is the deps.db to share, "" for one of its own.
func sourceSpace(t *testing.T, name string, files map[string]string, db string, prepare func(repo string)) *space {
	t.Helper()
	repo := testutil.SourceRepo(t, "app", files)
	if prepare != nil {
		prepare(repo)
	}
	sp := &space{t: t, ws: &config.Workspace{Name: name, Repos: []*config.Repo{{Name: "app", Path: repo}}}}
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, name+".db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if db == "" {
		db = filepath.Join(dir, "deps.db")
	}
	sp.store, sp.opts = s, deps.Options{DB: db, Full: true}
	sp.index()
	return sp
}

// localLib writes a Go module example.com/lib declaring one function
// and returns its directory.
func localLib(t *testing.T, fn string) string {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module example.com/lib\n")
	write(t, filepath.Join(dir, "lib.go"), "package lib\n\n// "+fn+" is unpublished.\nfunc "+fn+"() {}\n")
	return dir
}

// appUsing is a Go repo that replaces example.com/lib by a directory.
func appUsing(lib string) map[string]string {
	return map[string]string{
		"go.mod":  "module example.com/app\n\ngo 1.22\n\nrequire example.com/lib v0.0.0\n\nreplace example.com/lib => " + lib + "\n",
		"main.go": "package main\n\nimport \"example.com/lib\"\n\nfunc main() { lib.Run() }\n",
	}
}

// names returns the symbol names of a package version, sorted.
func (p pkg) names() string {
	var out []string
	for _, k := range p.keys() {
		out = append(out, k[strings.LastIndex(k, ":")+1:])
	}
	return strings.Join(out, " ")
}

// Two workspaces replacing one module path by different directories
// must not share an entry of deps.db: each sees its own directory only.
func TestLocalReplacesOfTwoWorkspacesStayApart(t *testing.T) {
	t.Setenv(deps.ModCacheEnv, t.TempDir())
	one := sourceSpace(t, "one", appUsing(localLib(t, "SecretOne")), "", nil)
	two := sourceSpace(t, "two", appUsing(localLib(t, "SecretTwo")), one.opts.DB, nil)
	one.sync(one.opts)
	two.sync(two.opts)
	idOne, idTwo := one.uses()["go.mod example.com/lib"].PackageID(), two.uses()["go.mod example.com/lib"].PackageID()
	if idOne == idTwo || !strings.HasPrefix(idOne, "go/example.com/lib@local-") {
		t.Fatalf("ids %q and %q", idOne, idTwo)
	}
	eq(t, "first workspace", one.pkg(idOne).names(), "SecretOne")
	eq(t, "second workspace", two.pkg(idTwo).names(), "SecretTwo")
	ids, err := deps.PackageIDs(one.store, 0)
	eq(t, "the first workspace sees its own directory only", ids, []string{idOne})
	eq(t, "error", err, nil)
}

// A mutable source is read again when its files change, on the bounded
// sync of a query too, and left alone while they stay the same.
func TestLocalReplaceIsExtractedAgainWhenItChanges(t *testing.T) {
	t.Setenv(deps.ModCacheEnv, t.TempDir())
	lib := localLib(t, "Run")
	sp := sourceSpace(t, "w", appUsing(lib), "", nil)
	eq(t, "first sync indexes it", sp.sync(sp.opts).Indexed, 1)
	id := sp.uses()["go.mod example.com/lib"].PackageID()
	quick := deps.Options{DB: sp.opts.DB}
	eq(t, "unchanged files are not read again", sp.sync(quick).Indexed, 0)
	write(t, filepath.Join(lib, "more.go"), "package lib\n\n// Added is new.\nfunc Added() {}\n")
	eq(t, "a new file is noticed by a query's sync", sp.sync(quick).Indexed, 1)
	eq(t, "symbols", sp.pkg(id).names(), "Added Run")
	eq(t, "state", sp.states()["go.mod example.com/lib"], strings.TrimPrefix(id, "go/example.com/lib@")+" indexed")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(lib, "more.go"), later, later); err != nil {
		t.Fatal(err)
	}
	eq(t, "a touched file with the same content rewrites nothing", sp.sync(quick).Indexed, 0)
	eq(t, "nor does the sync after it", sp.sync(sp.opts).Indexed, 0)
}

// A use recorded with the version every local replace once shared is
// discovered again by the next sync, full or not.
func TestLegacyLocalVersionIsRediscovered(t *testing.T) {
	t.Setenv(deps.ModCacheEnv, t.TempDir())
	sp := sourceSpace(t, "w", appUsing(localLib(t, "Run")), "", nil)
	sp.sync(sp.opts)
	old, err := sp.store.DepUses(0)
	eq(t, "error", err, nil)
	old[0].Version = "local"
	err = sp.store.Batch(func(b *store.Batch) error { return b.ReplaceDepUses(old[0].RepoID, old) })
	eq(t, "error", err, nil)
	sp.sync(deps.Options{DB: sp.opts.DB})
	if v := sp.uses()["go.mod example.com/lib"].Version; !strings.HasPrefix(v, "local-") {
		t.Errorf("version %q", v)
	}
}

// npmSources is a repo declaring one package of every kind of source.
var npmSources = map[string]string{
	"package.json": `{"name": "app", "version": "0.0.0", "pnpm": {"patchedDependencies": {"pp@1.0.0": "patches/pp.patch"}},
  "dependencies": {"plain": "^1.0.0", "lodash": "npm:lodash-es@^4.0.0", "gitdep": "github:user/gitdep#abc",
  "tarball": "https://example.com/t.tgz", "patched": "^1.0.0", "pp": "1.0.0", "@s/scoped": "1.0.0",
  "linked": "file:../linked", "everywhere": "1.0.0"}}`,
	"pnpm-workspace.yaml":           "patchedDependencies:\n  everywhere: patches/e.patch\n",
	"patches/patched+1.0.0.patch":   "diff\n",
	"patches/@s+scoped+1.0.0.patch": "diff\n",
	"src/a.ts":                      "import { chunk } from \"lodash\";\nexport const x = chunk;\n",
}

func npmPackage(t *testing.T, dir, name, version string) {
	t.Helper()
	write(t, filepath.Join(dir, "package.json"), `{"name": "`+name+`", "version": "`+version+`", "types": "index.d.ts"}`)
	write(t, filepath.Join(dir, "index.d.ts"), "export declare function chunk(): void;\n")
}

func TestNpmSourcesThatMayChangeGetALocalVersion(t *testing.T) {
	linked := filepath.Join(t.TempDir(), "linked")
	npmPackage(t, linked, "linked", "0.1.0")
	sp := sourceSpace(t, "w", npmSources, "", func(repo string) {
		for dir, name := range map[string]string{"plain": "plain", "lodash": "lodash-es", "gitdep": "gitdep", "tarball": "tarball",
			"patched": "patched", "pp": "pp", "@s/scoped": "@s/scoped", "everywhere": "everywhere"} {
			npmPackage(t, filepath.Join(repo, "node_modules", dir), name, "1.0.0")
		}
		if err := os.Symlink(linked, filepath.Join(repo, "node_modules", "linked")); err != nil {
			t.Fatal(err)
		}
	})
	sp.ws.Deps.Include = []string{"**"} // the one source file imports lodash only
	sp.sync(deps.Options{DB: sp.opts.DB, Deadline: timePast})
	states := sp.states()
	eq(t, "registry package", states["package.json plain"], "1.0.0 pending")
	eq(t, "alias of a registry package", states["package.json lodash"], "1.0.0 pending")
	for _, name := range []string{"gitdep", "tarball", "patched", "pp", "@s/scoped", "linked", "everywhere"} {
		if !localRE.MatchString(states["package.json "+name]) {
			t.Errorf("%s: %q, want a local version", name, states["package.json "+name])
		}
	}
	alias := sp.uses()["package.json lodash"]
	eq(t, "the alias is identified by the installed name", []string{alias.Package, alias.PackageID()},
		[]string{"lodash-es", "npm/lodash-es@1.0.0"})
	sp.sync(sp.opts)
	eq(t, "alias indexed", sp.states()["package.json lodash"], "1.0.0 indexed")
	eq(t, "its symbols are keyed by the real name", sp.pkg("npm/lodash-es@1.0.0").keys(),
		[]string{"npm/lodash-es@1.0.0:ts:lodash-es:index.d.ts/chunk"})
	write(t, filepath.Join(linked, "index.d.ts"), "export declare function chunk(): void;\nexport declare function more(): void;\n")
	eq(t, "a changed linked package is read again", sp.sync(deps.Options{DB: sp.opts.DB}).Indexed, 1)
}
