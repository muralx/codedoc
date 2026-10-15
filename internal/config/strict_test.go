package config_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
)

func TestLoadRejectsUnknownKeysNamingThem(t *testing.T) {
	dir := t.TempDir()
	body := "workspaces:\n  demo:\n    repos:\n      - path: " + dir + "/a\n        langauges: [go]\n"
	p := writeConfig(t, dir, body)
	_, err := config.Load(p)
	if err == nil || !strings.Contains(err.Error(), p+":5: unknown key \"langauges\"") {
		t.Fatalf("got %v", err)
	}
}

func TestLoadAcceptsAnEmptyFile(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, t.TempDir(), ""))
	if err != nil || len(cfg.Workspaces) != 0 {
		t.Fatalf("%+v %v", cfg, err)
	}
}

func TestLoadValidatesLanguagesAndExcludeGlobs(t *testing.T) {
	dir := t.TempDir()
	for body, want := range map[string]string{
		"workspaces:\n  demo:\n    repos:\n      - path: " + dir + "/a\n        languages: [go, cobol]\n": `workspace demo: repo a: unknown language "cobol"`,
		"workspaces:\n  demo:\n    repos:\n      - path: " + dir + "/a\n    exclude: [\"gen/[\"]\n":       `workspace demo: invalid exclude glob "gen/["`,
	} {
		if _, err := config.Load(writeConfig(t, dir, body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("got %v, want %q", err, want)
		}
	}
}

func TestFileLanguages(t *testing.T) {
	r := &config.Repo{Languages: []string{"ts", "py", "yaml", "shell"}}
	want := map[string]bool{"typescript": true, "tsx": true, "javascript": true, "python": true, "yaml": true, "shell": true}
	if got := r.FileLanguages(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if (&config.Repo{}).FileLanguages() != nil {
		t.Fatal("no languages selects every language")
	}
	r = &config.Repo{Languages: []string{"go", "typescript", "javascript", "python", "java", "json", "xml", "properties", "env", "hcl", "template"}}
	if got := len(r.FileLanguages()); got != 12 {
		t.Fatalf("%d languages: %v", got, r.FileLanguages())
	}
}
