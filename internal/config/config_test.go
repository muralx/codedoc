package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/config"
)

const sample = `
workspaces:
  demo:
    repos:
      - path: %[1]s/a
      - path: %[1]s/b
        name: bee
        languages: [go]
    exclude: ["**/gen/**"]
`

func writeSample(t *testing.T) (cfgPath, root string) {
	t.Helper()
	root = t.TempDir()
	for _, d := range []string{"a", "b", "c"} {
		if err := os.MkdirAll(filepath.Join(root, d, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfgPath = filepath.Join(root, "config.yaml")
	body := []byte(fmt.Sprintf(sample, root))
	if err := os.WriteFile(cfgPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return cfgPath, root
}

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaultsRepoNameToDirName(t *testing.T) {
	cfgPath, root := writeSample(t)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	ws := cfg.Workspaces["demo"]
	if ws.Repos[0].Name != "a" || ws.Repos[1].Name != "bee" {
		t.Fatalf("names: %+v", ws.Repos)
	}
	if ws.Repos[0].Path != filepath.Join(root, "a") {
		t.Fatalf("path not absolute: %s", ws.Repos[0].Path)
	}
	if cfg.DBPath("demo") == "" {
		t.Fatal("db path empty")
	}
}

func TestLoadResolvesRelativeRepoPathsAgainstConfigDir(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "conf")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := writeConfig(t, cfgDir, "workspaces:\n  w:\n    repos: [{path: ../repos/a}, {path: b}]\n")
	t.Chdir(t.TempDir())
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	repos := cfg.Workspaces["w"].Repos
	if repos[0].Path != filepath.Join(root, "repos", "a") {
		t.Fatalf("relative path ../repos/a resolved to %s", repos[0].Path)
	}
	if repos[1].Path != filepath.Join(cfgDir, "b") {
		t.Fatalf("relative path b resolved to %s", repos[1].Path)
	}
}

func TestLoadExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p := writeConfig(t, t.TempDir(), "workspaces:\n  w:\n    repos: [{path: ~/src/a}]\n")
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Workspaces["w"].Repos[0].Path; got != filepath.Join(home, "src", "a") {
		t.Fatalf("got %s", got)
	}
}

func TestDBPathUsesDataDir(t *testing.T) {
	data := t.TempDir()
	t.Setenv("CODEDOC_DATA", data)
	cfgPath, _ := writeSample(t)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.DBPath("demo"); got != filepath.Join(data, "demo.db") {
		t.Fatalf("got %s", got)
	}
}

func TestDBPathHonorsXDGDataHome(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	t.Setenv("CODEDOC_DATA", "")
	cfgPath, _ := writeSample(t)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.DBPath("demo"); got != filepath.Join(xdg, "codedoc", "demo.db") {
		t.Fatalf("got %s", got)
	}
	data := t.TempDir()
	t.Setenv("CODEDOC_DATA", data)
	if cfg, err = config.Load(cfgPath); err != nil || cfg.DBPath("demo") != filepath.Join(data, "demo.db") {
		t.Fatalf("CODEDOC_DATA must win: %v", err)
	}
}

func TestDBPathDefaultsUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEDOC_DATA", "")
	t.Setenv("XDG_DATA_HOME", "relative/ignored")
	cfgPath, _ := writeSample(t)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".local", "share", "codedoc", "demo.db")
	if got := cfg.DBPath("demo"); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("CODEDOC_CONFIG", "/etc/x.yaml")
	if got := config.DefaultPath(); got != "/etc/x.yaml" {
		t.Fatalf("got %s", got)
	}
	t.Setenv("CODEDOC_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "/cfg")
	if got := config.DefaultPath(); got != filepath.Join("/cfg", "codedoc", "config.yaml") {
		t.Fatalf("got %s", got)
	}
}

func TestLoadErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := config.Load(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Fatal("expected error for missing file")
	}
	for name, body := range map[string]string{
		"parse":           "workspaces: [unclosed",
		"null workspace":  "workspaces:\n  demo:\n",
		"null repo":       "workspaces:\n  demo:\n    repos:\n      -\n",
		"empty repo path": "workspaces:\n  demo:\n    repos:\n      - name: x\n",
		"repeated path":   "workspaces:\n  demo:\n    repos:\n      - {path: " + dir + "/a}\n      - {path: " + dir + "/a/, name: b}\n",
	} {
		if _, err := config.Load(writeConfig(t, dir, body)); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

func TestNamesSorted(t *testing.T) {
	root := t.TempDir()
	body := "workspaces:\n  zed:\n    repos: [{path: " + root + "/z}]\n  alpha:\n    repos: [{path: " + root + "/a}]\n"
	cfg, err := config.Load(writeConfig(t, root, body))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Names(); !reflect.DeepEqual(got, []string{"alpha", "zed"}) {
		t.Fatalf("got %v", got)
	}
}

func TestResolveWorkspaceFromCwd(t *testing.T) {
	cfgPath, root := writeSample(t)
	cfg, _ := config.Load(cfgPath)
	ws, repo, err := cfg.Resolve(filepath.Join(root, "b", "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if ws.Name != "demo" || repo.Name != "bee" {
		t.Fatalf("got %s %s", ws.Name, repo.Name)
	}
	if _, _, err := cfg.Resolve(filepath.Join(root, "c")); err == nil {
		t.Fatal("expected error for directory outside any workspace")
	}
}

func TestResolvePrefersInnermostRepo(t *testing.T) {
	root := t.TempDir()
	body := "workspaces:\n  outer:\n    repos: [{path: " + root + "/mono}]\n  inner:\n    repos: [{path: " + root + "/mono/vendor/lib}]\n"
	cfg, err := config.Load(writeConfig(t, root, body))
	if err != nil {
		t.Fatal(err)
	}
	ws, repo, err := cfg.Resolve(filepath.Join(root, "mono", "vendor", "lib", "pkg"))
	if err != nil {
		t.Fatal(err)
	}
	if ws.Name != "inner" || repo.Name != "lib" {
		t.Fatalf("got %s %s", ws.Name, repo.Name)
	}
	if _, repo, _ := cfg.Resolve(filepath.Join(root, "mono-other")); repo != nil {
		t.Fatal("sibling directory with shared prefix must not match")
	}
}

func TestLoadRejectsDuplicateRepoNamesInWorkspace(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "x", "platform"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, "y", "platform"), 0o755)
	body := "workspaces:\n  w:\n    repos: [{path: " + root + "/x/platform}, {path: " + root + "/y/platform}]\n"
	p := filepath.Join(root, "c.yaml")
	_ = os.WriteFile(p, []byte(body), 0o644)
	if _, err := config.Load(p); err == nil {
		t.Fatal("expected duplicate name error")
	}
}

func TestLoadRejectsDirectoryInTwoWorkspaces(t *testing.T) {
	root := t.TempDir()
	body := "workspaces:\n  x:\n    repos: [{path: " + root + "}]\n  y:\n    repos: [{path: " + root + "}]\n"
	p := filepath.Join(root, "c.yaml")
	_ = os.WriteFile(p, []byte(body), 0o644)
	if _, err := config.Load(p); err == nil {
		t.Fatal("expected error")
	}
}

func TestWorkspaceRepoNamesKeepConfigOrder(t *testing.T) {
	ws := &config.Workspace{Repos: []*config.Repo{{Name: "b"}, {Name: "a"}}}
	if got := ws.RepoNames(); len(got) != 2 || got[0] != "b" || got[1] != "a" {
		t.Fatalf("%v", got)
	}
}

func TestFileLanguagesSelectsDocuments(t *testing.T) {
	r := &config.Repo{Languages: []string{"go", "doc"}}
	got := r.FileLanguages()
	for _, lang := range []string{"go", "markdown", "rst", "asciidoc", "text"} {
		if !got[lang] {
			t.Errorf("languages [go, doc] lacks %s: %v", lang, got)
		}
	}
	if r := (&config.Repo{Languages: []string{"markdown"}}); len(r.FileLanguages()) != 1 {
		t.Errorf("markdown alone = %v", r.FileLanguages())
	}
}
