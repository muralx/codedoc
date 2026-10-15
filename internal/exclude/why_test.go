package exclude_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/exclude"
)

func TestWhyNamesTheRule(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "web/package.json")
	m := exclude.New(root, []string{"gen/**", "**/*.pb.go"})
	const builtin = " is excluded (dependencies, fixtures and build output are)"
	for rel, want := range map[string]string{
		"main.go":                   "",
		"pnpm-lock.yaml":            "dependency lock files are never indexed",
		"web/go.sum":                "dependency lock files are never indexed",
		"a/node_modules/x/index.js": "the directory a/node_modules/" + builtin,
		"web/dist/app.js":           "the directory web/dist/" + builtin,
		"dist/app.js":               "the directory dist/" + builtin,
		"gen/a.go":                  "it matches the exclude glob gen/**",
		"x/y.pb.go":                 "it matches the exclude glob **/*.pb.go",
		"internal/build/build.go":   "",
	} {
		if got := m.Why(rel, false); got != want || m.Path(rel, false) != (want != "") {
			t.Errorf("%s: %q, want %q", rel, got, want)
		}
	}
	for rel, want := range map[string]string{
		"vendor":   "the directory vendor/" + builtin,
		"gen":      "it matches the exclude glob gen/**",
		"src":      "",
		"web/dist": "the directory web/dist/" + builtin,
	} {
		if got := m.Why(rel, true); got != want {
			t.Errorf("dir %s: %q, want %q", rel, got, want)
		}
	}
}
