//go:build integration

package main

import (
	"bufio"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// sourceFiles returns the repo-relative files of the repository at root:
// those git tracks or, when git cannot read the repository (a worktree,
// whose .git file points into the main checkout, mounted alone in a
// container), every file the tree's .gitignore files do not exclude.
func sourceFiles(t *testing.T, root string) []string {
	files, err := gitFiles(root, "ls-files", "-z")
	if err == nil {
		return files
	}
	t.Logf("git ls-files in %s: %v; walking the tree, honoring .gitignore", root, err)
	return walkIgnoring(t, root)
}

// gitFiles runs git with args in root and splits its NUL-separated output.
func gitFiles(root string, args ...string) ([]string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(string(out), "\x00"), "\x00"), nil
}

// ignoreRule is one pattern of a .gitignore file in directory base
// (slash-separated, relative to the walked root). It supports the
// subset of gitignore this repository uses: globs, a leading "/" or an
// inner "/" anchoring the pattern to base, a trailing "/" for
// directories only, and "!" negation; "**" is not supported.
type ignoreRule struct {
	base, pattern             string
	anchored, dirOnly, negate bool
}

// matches reports whether the rule applies to rel, a path under the
// walked root.
func (r ignoreRule) matches(rel string, dir bool) bool {
	if r.dirOnly && !dir {
		return false
	}
	sub := rel
	if r.base != "." {
		var ok bool
		if sub, ok = strings.CutPrefix(rel, r.base+"/"); !ok {
			return false
		}
	}
	if !r.anchored {
		sub = path.Base(sub)
	}
	ok, _ := path.Match(r.pattern, sub)
	return ok
}

// readIgnore returns the rules of dir's .gitignore, if any.
func readIgnore(t *testing.T, root, dir string) []ignoreRule {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(dir), ".gitignore"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var rules []ignoreRule
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := ignoreRule{base: dir}
		line, r.negate = strings.CutPrefix(line, "!")
		line, r.dirOnly = strings.CutSuffix(line, "/")
		r.anchored = strings.Contains(line, "/")
		r.pattern = strings.TrimPrefix(line, "/")
		rules = append(rules, r)
	}
	return rules
}

// ignored applies rules in order; the last matching rule decides.
func ignored(rules []ignoreRule, rel string, dir bool) bool {
	out := false
	for _, r := range rules {
		if r.matches(rel, dir) {
			out = !r.negate
		}
	}
	return out
}

// walkIgnoring lists the regular files under root that no .gitignore
// excludes, skipping .git.
func walkIgnoring(t *testing.T, root string) []string {
	var rules []ignoreRule
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		switch {
		case d.Name() == ".git":
			return skip(d)
		case rel != "." && ignored(rules, rel, d.IsDir()):
			return skip(d)
		case d.IsDir():
			rules = append(rules, readIgnore(t, root, rel)...)
		case d.Type().IsRegular():
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// skip leaves out d: a directory with everything below it.
func skip(d fs.DirEntry) error {
	if d.IsDir() {
		return filepath.SkipDir
	}
	return nil
}

// TestWalkIgnoringAgreesWithGit checks the fallback against git where
// git can read the repository: every tracked file is walked and no
// ignored one is.
func TestWalkIgnoringAgreesWithGit(t *testing.T) {
	root := moduleRoot(t)
	tracked, err := gitFiles(root, "ls-files", "-z")
	if err != nil {
		t.Skipf("git cannot read %s: %v", root, err)
	}
	ignoredFiles, err := gitFiles(root, "ls-files", "-z", "--others", "--ignored", "--exclude-standard")
	if err != nil {
		t.Fatal(err)
	}
	walked := walkIgnoring(t, root)
	for _, f := range tracked {
		if _, err := os.Lstat(filepath.Join(root, f)); err == nil && !slices.Contains(walked, f) && !isLink(root, f) {
			t.Errorf("tracked file %s not walked", f)
		}
	}
	for _, f := range ignoredFiles {
		if f != "" && slices.Contains(walked, f) {
			t.Errorf("ignored file %s walked", f)
		}
	}
}

// isLink reports a symbolic link, which the walk does not list.
func isLink(root, rel string) bool {
	info, err := os.Lstat(filepath.Join(root, rel))
	return err == nil && info.Mode()&os.ModeSymlink != 0
}
