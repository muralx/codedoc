package config_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/config"
)

const sectionsBody = `
embed:
  model_dir: models
workspaces:
  myapp:
    repos:
      - path: myapp
      - path: myapp-config
    scip:
      enabled: [myapp]
      every: 6h
    autodoc:
      every: 1d
      at: "02:00"
      max_items: 40
      commit: true
      group_size: 3
      max_cost_usd: 0.5
`

func TestLoadParsesScipAutodocAndEmbed(t *testing.T) {
	t.Setenv("CODEDOC_MODEL_DIR", "")
	dir := t.TempDir()
	cfg, err := config.Load(writeConfig(t, dir, sectionsBody))
	if err != nil {
		t.Fatal(err)
	}
	ws := cfg.Workspaces["myapp"]
	if len(ws.SCIP.Enabled) != 1 || ws.SCIP.Enabled[0] != "myapp" || ws.SCIP.Every.D() != 6*time.Hour {
		t.Fatalf("scip: %+v", ws.SCIP)
	}
	a := ws.Autodoc
	if a.Every.D() != 24*time.Hour || a.At != "02:00" || a.MaxItems != 40 || !a.Commit || a.GroupSize != 3 || a.MaxCostUSD != 0.5 {
		t.Fatalf("autodoc: %+v", a)
	}
	if cfg.Embed.ModelDir != filepath.Join(dir, "models") {
		t.Fatalf("model dir: %q", cfg.Embed.ModelDir)
	}
}

func TestModelDirEnvOverrides(t *testing.T) {
	t.Setenv("CODEDOC_MODEL_DIR", "/opt/models")
	cfg, err := config.Load(writeConfig(t, t.TempDir(), sectionsBody))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Embed.ModelDir != "/opt/models" {
		t.Fatalf("model dir: %q", cfg.Embed.ModelDir)
	}
}

func TestModelDirUnsetStaysEmpty(t *testing.T) {
	t.Setenv("CODEDOC_MODEL_DIR", "")
	body := "workspaces:\n  w:\n    repos:\n      - path: a\n"
	cfg, err := config.Load(writeConfig(t, t.TempDir(), body))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Embed.ModelDir != "" {
		t.Fatalf("model dir: %q", cfg.Embed.ModelDir)
	}
	if ws := cfg.Workspaces["w"]; ws.SCIP.Every.D() != 0 || ws.Autodoc.At != "" {
		t.Fatalf("sections not zero: %+v %+v", ws.SCIP, ws.Autodoc)
	}
}

func TestAutodocMaxItemsDefaultsLikeTheCLI(t *testing.T) {
	head := "workspaces:\n  w:\n    repos:\n      - path: a\n"
	for _, body := range []string{"", "    autodoc: {max_items: 0}\n"} {
		cfg, err := config.Load(writeConfig(t, t.TempDir(), head+body))
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.Workspaces["w"].Autodoc.MaxItems; got != 40 {
			t.Fatalf("%q: max_items %d", body, got)
		}
	}
}

func TestDurationUnits(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"30m": 30 * time.Minute, "6h": 6 * time.Hour, "24h": 24 * time.Hour,
		"2d": 48 * time.Hour, "1w": 7 * 24 * time.Hour, "1h30m": 90 * time.Minute, "0": 0,
	} {
		d, err := config.ParseDuration(in)
		if err != nil || d.D() != want {
			t.Fatalf("%s: got %v, %v; want %v", in, d.D(), err, want)
		}
	}
	for _, in := range []string{"", "x", "d", "-1h", "-2d", "1.5d", "3y"} {
		if _, err := config.ParseDuration(in); err == nil {
			t.Fatalf("%q: expected error", in)
		}
	}
}

func TestSectionValidation(t *testing.T) {
	head := "workspaces:\n  w:\n    repos:\n      - path: a\n"
	for name, tc := range map[string]struct{ body, msg string }{
		"unknown scip repo": {"    scip: {enabled: [b]}\n", `scip.enabled: no repo named "b"`},
		"bad every":         {"    scip: {every: soon}\n", "soon"},
		"bad at":            {"    autodoc: {at: \"25:00\"}\n", "autodoc.at"},
		"at without colon":  {"    autodoc: {at: \"0200\"}\n", "autodoc.at"},
		"negative items":    {"    autodoc: {max_items: -1}\n", "autodoc.max_items must be a positive number"},
		"negative group":    {"    autodoc: {group_size: -1}\n", "group_size"},
		"negative cost":     {"    autodoc: {max_cost_usd: -1}\n", "max_cost_usd"},
		"every not scalar":  {"    autodoc: {every: [1h]}\n", "duration"},
	} {
		_, err := config.Load(writeConfig(t, t.TempDir(), head+tc.body))
		if err == nil || !strings.Contains(err.Error(), tc.msg) {
			t.Fatalf("%s: err %v, want %q", name, err, tc.msg)
		}
	}
}

func TestDurationString(t *testing.T) {
	d, err := config.ParseDuration("2d")
	if err != nil {
		t.Fatal(err)
	}
	if d.String() != "48h0m0s" {
		t.Fatalf("string: %s", d)
	}
}

func TestDepsSectionDefaultsOnAndIsValidated(t *testing.T) {
	dir := t.TempDir()
	base := "workspaces:\n  demo:\n    repos:\n      - path: " + dir + "/a\n"
	cfg, err := config.Load(writeConfig(t, dir, base))
	if err != nil || !cfg.Workspaces["demo"].Deps.On() {
		t.Fatalf("deps must default to enabled: %v", err)
	}
	cfg, err = config.Load(writeConfig(t, dir, base+
		"    deps:\n      enabled: false\n      include: [zod-to-json-schema]\n      exclude: [\"@types/*\", \"eslint*\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.Workspaces["demo"].Deps
	if d.On() || len(d.Include) != 1 || !d.Excluded("@types/node") || !d.Excluded("eslint-plugin-x") || d.Excluded("zod") {
		t.Fatalf("got %+v", d)
	}
	for body, want := range map[string]string{
		base + "    deps:\n      exclude: [\"a[\"]\n":                     `workspace demo: deps.exclude: invalid pattern "a["`,
		base + "    deps:\n      include: [\" \"]\n":                      `workspace demo: deps.include: empty package name`,
		base + "    deps:\n      include: [\"b[\"]\n":                     `workspace demo: deps.include: invalid pattern "b["`,
		base + "    deps:\n      enabeld: true\n":                         `unknown key "enabeld" in deps`,
		"workspaces:\n  deps:\n    repos:\n      - path: " + dir + "/a\n": `workspace deps: the name is reserved`,
	} {
		if _, err := config.Load(writeConfig(t, dir, body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("got %v, want %q", err, want)
		}
	}
}

// deps.include takes package names and patterns: both make a declared
// package indexed, and only the plain names are looked up among the
// transitive packages.
func TestDepsIncludeTakesNamesAndPatterns(t *testing.T) {
	d := config.Deps{Include: []string{"zod-to-json-schema", "@types/*", "eslint*", "github.com/acme/**"}}
	for name, want := range map[string]bool{"zod-to-json-schema": true, "@types/node": true, "eslint-plugin-x": true,
		"github.com/acme/kit/v2": true, "zod": false, "@scope/eslint": false} {
		if d.Included(name) != want {
			t.Errorf("Included(%q) = %v", name, !want)
		}
	}
	if got := d.IncludeNames(); len(got) != 1 || got[0] != "zod-to-json-schema" {
		t.Errorf("plain names: %v", got)
	}
	if !config.MatchPackage("lint-*", "lint-tool") || config.MatchPackage("lint-*", "@x/lint-tool") {
		t.Error("MatchPackage matches the whole name")
	}
}

func TestDepsDBPathIsSharedByWorkspaces(t *testing.T) {
	t.Setenv("CODEDOC_DATA", "/data")
	cfg, err := config.Load(writeConfig(t, t.TempDir(), ""))
	if err != nil || cfg.DepsDBPath() != "/data/deps.db" {
		t.Fatalf("%v %v", cfg.DepsDBPath(), err)
	}
}
