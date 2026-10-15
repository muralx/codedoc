package daemon

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// gitDirs returns the git directory of the working tree at root and the
// common directory holding shared refs. A linked worktree has a .git
// file reading "gitdir: <path>", and its git directory a commondir file.
// Both are "" when root is not a git working tree.
func gitDirs(root string) (gitDir, commonDir string) {
	dot := filepath.Join(root, ".git")
	info, err := os.Stat(dot)
	switch {
	case err != nil:
		return "", ""
	case info.IsDir():
		gitDir = dot
	default:
		raw, err := os.ReadFile(dot)
		target, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
		if err != nil || !ok {
			return "", ""
		}
		gitDir = relTo(root, strings.TrimSpace(target))
	}
	commonDir = gitDir
	if raw, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		commonDir = relTo(gitDir, strings.TrimSpace(string(raw)))
	}
	return gitDir, commonDir
}

func relTo(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

// headState returns what HEAD names and the commit it resolves to, as
// one comparable string such as "ref: refs/heads/main@<hash>". It is ""
// for a directory that is not a git working tree.
func headState(root string) string {
	gitDir, commonDir := gitDirs(root)
	if gitDir == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(raw))
	ref, symbolic := strings.CutPrefix(head, "ref:")
	if !symbolic {
		return head + "@" + head
	}
	ref = strings.TrimSpace(ref)
	return head + "@" + resolveRef(ref, gitDir, commonDir)
}

// resolveRef reads a ref from the worktree's git directory, then the
// common directory, then packed-refs. It returns "" for an unborn branch.
func resolveRef(ref, gitDir, commonDir string) string {
	for _, dir := range []string{gitDir, commonDir} {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(ref)))
		if err == nil {
			return strings.TrimSpace(string(raw))
		}
	}
	return packedRef(filepath.Join(commonDir, "packed-refs"), ref)
}

func packedRef(path, ref string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		hash, name, ok := strings.Cut(sc.Text(), " ")
		if ok && name == ref {
			return hash
		}
	}
	return ""
}
