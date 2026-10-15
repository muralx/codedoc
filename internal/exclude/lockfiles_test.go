package exclude_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/exclude"
)

// TestLockFilesExcluded checks that dependency lock files are excluded
// at any depth, by exact name only, and never as directories.
func TestLockFilesExcluded(t *testing.T) {
	m := exclude.New(t.TempDir(), nil)
	for _, name := range []string{"pnpm-lock.yaml", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "bun.lockb",
		"bun.lock", "go.sum", "go.work.sum", "poetry.lock", "uv.lock", "Pipfile.lock", "Cargo.lock", "composer.lock",
		"Gemfile.lock", "gradle.lockfile"} {
		for _, rel := range []string{name, "packages/web/" + name} {
			if !m.Path(rel, false) {
				t.Errorf("%s: not excluded", rel)
			}
		}
		if m.Path(name, true) {
			t.Errorf("directory %s: excluded", name)
		}
	}
	for _, rel := range []string{"go.mod", "package.json", "lock.yaml", "my-pnpm-lock.yaml", "yarn.lock/a.go", "src/lock.go"} {
		if m.Path(rel, false) {
			t.Errorf("%s: excluded", rel)
		}
	}
}
