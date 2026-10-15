//go:build integration

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/deps"
)

// realDepsEnv names the directory holding the installed real-dependency
// fixture (deps spec section 11): testdata/realdeps with npm-app after a
// real `pnpm install --frozen-lockfile`, and gomodcache, the module cache
// a real `go mod download` of go-app filled. The integration image
// builds it (see the Dockerfile); nothing is fetched when the tests run.
const realDepsEnv = "CODEDOC_REALDEPS"

// Keys of the dependency symbols the fixture repos use.
const (
	nobleKey  = "npm/@noble/hashes@1.4.0:ts:@noble/hashes/"
	hashClone = nobleKey + "utils:utils.d.ts/Hash.clone"
	msKey     = "npm/ms@2.1.3:ts:ms:index.d.ts/ms"
	nowKey    = "go/github.com/jinzhu/now@v1.1.5:go:github.com/jinzhu/now:"
	errorsKey = "go/github.com/pkg/errors@v0.9.1:go:github.com/pkg/errors:"
)

// realDepsWorkspace copies the installed fixture repos into git
// repositories of a workspace "real", selects the fixture's module cache
// and indexes the workspace. It returns the repo copies by name and the
// output of the index run. The test is skipped where no fixture is named
// (outside the integration image) and fails when the one named is not
// installed.
func realDepsWorkspace(t *testing.T) (repos map[string]string, indexed string) {
	t.Helper()
	root := os.Getenv(realDepsEnv)
	if root == "" {
		t.Skipf("%s is not set: the real pnpm install and go mod download exist only in the integration image "+
			"(make docker-integration)", realDepsEnv)
	}
	for _, need := range []string{"npm-app/node_modules/.pnpm", "go-app/go.sum", "gomodcache/cache/download"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(need))); err != nil {
			t.Fatalf("%s=%s holds no installed fixture: %v", realDepsEnv, root, err)
		}
	}
	repos = map[string]string{}
	var list []string
	for _, name := range []string{"npm-app", "go-app"} {
		repos[name] = filepath.Join(t.TempDir(), name)
		// cp -a keeps the symlinks pnpm lays out in node_modules.
		if out, err := exec.Command("cp", "-a", filepath.Join(root, name), repos[name]).CombinedOutput(); err != nil {
			t.Fatalf("copying %s: %v\n%s", name, err, out)
		}
		git(t, repos[name], "init", "-q", "-b", "main")
		git(t, repos[name], "add", "-A")
		git(t, repos[name], "-c", "user.email=f@x", "-c", "user.name=fixture", "commit", "-qm", "fixture: initial")
		list = append(list, "{path: "+repos[name]+"}")
	}
	dir := t.TempDir()
	body := "workspaces:\n  real:\n    repos: [" + strings.Join(list, ", ") + "]\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEDOC_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("CODEDOC_DATA", filepath.Join(dir, "data"))
	t.Setenv(deps.ModCacheEnv, filepath.Join(root, "gomodcache"))
	return repos, cli(t, "index", "--workspace", "real", "--verbose")
}

// hasAll fails the test unless out contains every substring.
func hasAll(t *testing.T, what, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("%s misses %q:\n%s", what, w, out)
		}
	}
}

// TestIntegrationRealDepsAreDiscoveredAndIndexed checks the states of
// the packages a real pnpm install and a real go mod download provide:
// every declared dependency is found where the package manager put it
// (pnpm's symlinks into node_modules/.pnpm, the module cache) and
// indexed, the @types/ms declarations under the package they describe.
func TestIntegrationRealDepsAreDiscoveredAndIndexed(t *testing.T) {
	_, indexed := realDepsWorkspace(t)
	hasLines(t, indexed, "== deps (2)",
		"npm-app  indexed:2 pending:0 missing:0 failed:0 skipped:0 unused:0",
		"go-app  indexed:2 pending:0 missing:0 failed:0 skipped:0 unused:0",
		"indexed 4 package version(s)")
	out := cli(t, "deps", "--workspace", "real")
	hasLines(t, out, "== deps (4)")
	for _, line := range []string{
		`npm/@noble/hashes@1\.4\.0  npm-app:package\.json  indexed declared:1\.4\.0 symbols:[1-9]\d*`,
		`npm/ms@2\.1\.3  npm-app:package\.json  indexed declared:2\.1\.3 symbols:[1-9]\d*`,
		`go/github\.com/jinzhu/now@v1\.1\.5  go-app:go\.mod  indexed declared:v1\.1\.5 symbols:[1-9]\d*`,
		`go/github\.com/pkg/errors@v0\.9\.1  go-app:go\.mod  indexed declared:v0\.9\.1 symbols:[1-9]\d*`,
	} {
		// Each line ends with the embedding state: nothing is embedded
		// until codedoc deps index --embed or the daemon does it.
		if !regexp.MustCompile(`(?m)^` + line + ` vectors:0/[1-9]\d*$`).MatchString(out) {
			t.Errorf("deps misses a line matching %s:\n%s", line, out)
		}
	}
	if strings.Contains(out, "@types/ms") {
		t.Errorf("@types/ms is no package of its own:\n%s", out)
	}
	hasAll(t, "overview", cli(t, "overview", "--workspace", "real", "--budget", "0"), "== dependencies (4)\n",
		"\nnpm/@noble/hashes@1.4.0  npm-app indexed symbols:", "\ngo/github.com/pkg/errors@v0.9.1  go-app indexed symbols:")
	// A second run finds every version in deps.db.
	if again := cli(t, "index", "--workspace", "real", "--verbose"); strings.Contains(again, "package version(s)") {
		t.Errorf("an unchanged install is indexed once:\n%s", again)
	}
}

// TestIntegrationRealNpmPackagesAnswerQueries asks about the packages
// of the pnpm install: symbols with --deps, a member inherited across
// two files of a scoped package with an exports map, the declarations of
// an @types pair, and the edges from the workspace into them.
func TestIntegrationRealNpmPackagesAnswerQueries(t *testing.T) {
	repos, _ := realDepsWorkspace(t)
	t.Chdir(repos["npm-app"])
	// No workspace symbol is named HMAC: the name needs no --deps.
	if out := cli(t, "find", "HMAC"); !strings.Contains(out, "[dep @noble/hashes@1.4.0]") {
		t.Errorf("without --deps:\n%s", out)
	}
	// Text names a symbol by its short name; --keys prints the stable
	// key, --detail every tag.
	hasAll(t, "find --deps", cli(t, "find", "HMAC", "--deps"),
		"HMAC  hmac.d.ts:2-15  export declare class HMAC<T extends Hash<T>> extends Hash<HMAC<T>>  [dep @noble/hashes@1.4.0]\n")
	hasAll(t, "find --deps --keys", cli(t, "find", "HMAC", "--deps", "--keys"), nobleKey+"hmac:hmac.d.ts/HMAC  hmac.d.ts:2-15  ",
		"export declare class HMAC<T extends Hash<T>> extends Hash<HMAC<T>>", "[dep @noble/hashes@1.4.0]")
	hasAll(t, "find --deps of the @types pair", cli(t, "find", "ms", "--deps", "--keys"), msKey+"  index.d.ts:", "[dep ms@2.1.3]")
	clone := repos["npm-app"] + "/node_modules/.pnpm/@noble+hashes@1.4.0/node_modules/@noble/hashes/utils.d.ts:58  "
	hasAll(t, "symbol --deps --body", cli(t, "symbol", "HMAC.clone", "--deps", "--body"),
		"Hash.clone  "+clone+"[dep @noble/hashes@1.4.0]\n", "58\t    clone(): T", "\nHMAC.clone is declared on Hash\n")
	hasAll(t, "symbol --deps --body --keys --detail", cli(t, "symbol", "HMAC.clone", "--deps", "--body", "--keys", "--detail"),
		hashClone+"  "+clone, "[declared on Hash]", "58\t    clone(): T")
	out := cli(t, "outline", "@noble/hashes")
	hasAll(t, "outline of a package", out, "== npm/@noble/hashes@1.4.0 modules (",
		"\n@noble/hashes/hmac  hmac  ts npm-export, ", "\n@noble/hashes/sha256  sha256  ts npm-export, ")
	hasAll(t, "outline of the @types pair", cli(t, "outline", "ms"), "== npm/ms@2.1.3 modules (1)\nms  .  ts npm-package, ")
	hasAll(t, "outline of a dependency class", cli(t, "outline", "HMAC", "--deps"), "== inherited members (1)\n",
		"\nHash.clone  utils.d.ts:58  clone(): T\n")
	hasAll(t, "outline of a dependency class --keys --detail", cli(t, "outline", "HMAC", "--deps", "--keys", "--detail"),
		"== inherited members (1)\n", hashClone+"  utils.d.ts:58  clone(): T  ", "[declared on Hash]")
	hasAll(t, "callees into an inherited member", cli(t, "callees", "Signer.snapshot", "--keys"),
		hashClone+"  src/signer.ts:12  return this.mac.clone();  [dep]\n")
	hasAll(t, "callees into the @types pair", cli(t, "callees", "lifetime", "--keys"), msKey+"  src/signer.ts:23  ", "[dep]")
	hasAll(t, "callers of an inherited member", cli(t, "callers", "HMAC.clone", "--keys"),
		"== callers of "+hashClone+" [dep @noble/hashes@1.4.0] (1)\n",
		"npm-app:ts:@fixture/real-npm:src/signer.ts/Signer.snapshot  src/signer.ts:12  ", "HMAC.clone is declared on Hash")
	hasAll(t, "callers of a dependency function", cli(t, "callers", "bytesToHex", "--keys"),
		"== callers of "+nobleKey+"utils:utils.d.ts/bytesToHex [dep @noble/hashes@1.4.0] (1)\n", "/Signer.sign  src/signer.ts:17  ")
	hasAll(t, "callers of a dependency function, by name", cli(t, "callers", "bytesToHex"),
		"== callers of bytesToHex [dep @noble/hashes@1.4.0] (1)\nSigner.sign  src/signer.ts:17  return bytesToHex(")
}

// TestIntegrationRealGoModulesAnswerQueries asks about the modules of
// the go mod download: symbols with --deps, a method a struct inherits
// from the one it embeds, and the edges from the workspace into them.
func TestIntegrationRealGoModulesAnswerQueries(t *testing.T) {
	repos, _ := realDepsWorkspace(t)
	t.Chdir(repos["go-app"])
	// No workspace symbol is named Wrap: the name needs no --deps.
	if out := cli(t, "find", "Wrap"); !strings.Contains(out, "[dep ") {
		t.Errorf("without --deps:\n%s", out)
	}
	hasAll(t, "find --deps --keys", cli(t, "find", "Wrap", "--deps", "--keys"),
		errorsKey+"Wrap  errors.go:184-196  func Wrap(err error, message string) error  ", "[dep github.com/pkg/errors@v0.9.1]")
	cache := os.Getenv(deps.ModCacheEnv)
	with := cache + "/github.com/jinzhu/now@v1.1.5/main.go:40-42  "
	hasAll(t, "symbol --deps --body", cli(t, "symbol", "Now.With", "--deps", "--body"),
		"Config.With  "+with+"[dep github.com/jinzhu/now@v1.1.5]\n", "41\t\treturn &Now{Time: t, Config: config}",
		"\nNow.With is declared on Config\n")
	hasAll(t, "symbol --deps --body --keys --detail", cli(t, "symbol", "Now.With", "--deps", "--body", "--keys", "--detail"),
		nowKey+"Config.With  "+with+"func (config *Config) With(t time.Time) *Now  ",
		"[declared on Config]", "41\t\treturn &Now{Time: t, Config: config}")
	hasAll(t, "outline of a module", cli(t, "outline", "github.com/jinzhu/now"),
		"== go/github.com/jinzhu/now@v1.1.5 modules (1)\ngithub.com/jinzhu/now  .  go go-package, ")
	// The members of one file are listed under its path.
	hasAll(t, "outline of a dependency struct", cli(t, "outline", "Now", "--deps"), "== inherited members (6)\nmain.go\n",
		"\n  Config.With  :40-42  func (config *Config) With(t time.Time) *Now\n")
	hasAll(t, "outline of a dependency struct --keys --detail", cli(t, "outline", "Now", "--deps", "--keys", "--detail"),
		"== inherited members (6)\nmain.go\n", "  "+nowKey+"Config.With  :40-42  func (config *Config) With(t time.Time) *Now  ",
		"[declared on Config]")
	// A callee is listed at the line of its call; --detail prints its
	// signature and the line under it.
	hasAll(t, "callees into the modules", cli(t, "callees", "calendar.monday", "--keys"), "\nmain.go\n",
		"  "+errorsKey+"New  :21  return time.Time{}, errors.New(\"no clock\")  [dep]\n", "  "+nowKey+"Config.With  :23  ")
	hasAll(t, "callees into the modules --detail", cli(t, "callees", "calendar.monday", "--keys", "--detail"),
		"  "+errorsKey+"New  :21  func New(message string) error  ", "  "+nowKey+"Config.With  :23  ", "[dep]",
		"\n21\t")
	hasAll(t, "callers of an inherited method", cli(t, "callers", "Now.With", "--keys"),
		"== callers of "+nowKey+"Config.With [dep github.com/jinzhu/now@v1.1.5] (1)\n",
		"go-app:go:github.com/fixture/realgo:calendar.monday  main.go:23  ", "Now.With is declared on Config")
	hasAll(t, "callers of a dependency function", cli(t, "callers", "errors.New", "--keys"),
		"== callers of "+errorsKey+"New [dep github.com/pkg/errors@v0.9.1] (1)\n", ":calendar.monday  main.go:21  ")
}
