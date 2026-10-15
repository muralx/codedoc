// Package exclude holds the rules that keep repo paths out of the index
// and out of the daemon's watches (spec 7.1): the built-in directory
// exclusions, the dependency lock files and the workspace exclude globs.
// Discovery and the watcher share it, so both see the same tree.
package exclude

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// anyDepth are directory names excluded wherever they appear.
var anyDepth = map[string]bool{"vendor": true, "node_modules": true, ".git": true, "testdata": true}

// buildOutput are directory names excluded at the repo root or next to a
// build manifest, where a build tool writes its output; elsewhere they
// may be real packages.
var buildOutput = map[string]bool{"build": true, "dist": true, "target": true}

// manifests are the build files that make a sibling build output
// directory excluded.
var manifests = []string{"go.mod", "package.json", "pom.xml", "build.gradle", "build.gradle.kts",
	"pyproject.toml", "setup.py", "Cargo.toml"}

// lockFiles are the dependency lock files, excluded by name wherever
// they appear: a package manager writes them, nothing a reader looks up
// is defined there, and one can hold more keys than the rest of a repo.
var lockFiles = map[string]bool{"pnpm-lock.yaml": true, "package-lock.json": true, "npm-shrinkwrap.json": true,
	"yarn.lock": true, "bun.lockb": true, "bun.lock": true, "go.sum": true, "go.work.sum": true, "poetry.lock": true,
	"uv.lock": true, "Pipfile.lock": true, "Cargo.lock": true, "composer.lock": true, "Gemfile.lock": true,
	"gradle.lockfile": true}

// ValidateGlobs returns an error naming the first exclude glob that is
// not a valid doublestar pattern.
func ValidateGlobs(globs []string) error {
	for _, g := range globs {
		if !doublestar.ValidatePattern(g) {
			return fmt.Errorf("invalid exclude glob %q", g)
		}
	}
	return nil
}

// Matcher decides which paths of one repo are excluded.
type Matcher struct {
	root  string
	globs []string
}

// New returns the matcher of the repo at root with the workspace's
// exclude globs; an invalid glob matches nothing (see ValidateGlobs).
func New(root string, globs []string) Matcher { return Matcher{root: root, globs: globs} }

// Path reports whether a repo-relative slash path is excluded: it lies
// in a built-in excluded directory (or, when isDir, is one), is a
// dependency lock file, or matches an exclude glob; a directory also
// when a glob matches everything below it.
func (m Matcher) Path(rel string, isDir bool) bool { return m.Why(rel, isDir) != "" }

// Why names the rule that excludes a repo-relative slash path (see
// Path), in words that finish "it is not indexed: ..."; "" when no rule
// excludes it.
func (m Matcher) Why(rel string, isDir bool) string {
	dirs := rel
	if !isDir {
		if lockFiles[path.Base(rel)] {
			return "dependency lock files are never indexed"
		}
		dirs = path.Dir(rel)
	}
	if dirs != "." {
		if dir := m.builtin(strings.Split(dirs, "/")); dir != "" {
			return "the directory " + dir + "/ is excluded (dependencies, fixtures and build output are)"
		}
	}
	for _, g := range m.globs {
		if ok, _ := doublestar.Match(g, rel); ok {
			return "it matches the exclude glob " + g
		}
		if ok, _ := doublestar.Match(g, rel+"/"); isDir && ok {
			return "it matches the exclude glob " + g
		}
	}
	return ""
}

// builtin returns the first directory of a path, given as its
// components, that a built-in rule excludes; "" when none does.
func (m Matcher) builtin(parts []string) string {
	for i, name := range parts {
		switch {
		case anyDepth[name]:
			return path.Join(parts[:i+1]...)
		case !buildOutput[name]:
		case i == 0, m.hasManifest(path.Join(parts[:i]...)):
			return path.Join(parts[:i+1]...)
		}
	}
	return ""
}

// hasManifest reports whether the repo-relative directory dir holds a
// build manifest.
func (m Matcher) hasManifest(dir string) bool {
	for _, f := range manifests {
		info, err := os.Stat(filepath.Join(m.root, filepath.FromSlash(dir), f))
		if err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}
