package resolve

import (
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// javaModule adds the Java package of dir. Its identity is the package
// its files declare (declared), else the dotted path below the source
// root, so the main and test directories of one package share it: the
// deepest src/<set>/java or src/<set>/kotlin directory above dir, else
// the deepest src directory, else the repo root. A directory that is a
// source root itself (the default package) is identified by its path.
func (b *builder) javaModule(dir, declared string) {
	id := dir
	if pkg := dotted(rel(javaSourceRoot(dir), dir)); pkg != "" {
		id = pkg
	}
	if declared != "" {
		id = declared
	}
	b.add(Module{Path: dir, Lang: "java", Kind: "java-package", Identity: id, Root: ".", Package: id})
}

// javaSourceRoot returns the source root of a directory holding Java
// files (see javaModule).
func javaSourceRoot(dir string) string {
	segs := splitRel(rel(".", dir))
	for i := len(segs) - 1; i >= 2; i-- {
		if (segs[i] == "java" || segs[i] == "kotlin") && segs[i-2] == "src" {
			return strings.Join(segs[:i+1], "/")
		}
	}
	for i := len(segs) - 1; i >= 0; i-- {
		if segs[i] == "src" {
			return strings.Join(segs[:i+1], "/")
		}
	}
	return "."
}

// javaPackageDecl matches a package declaration after the comments and
// annotations that may precede it.
var javaPackageDecl = regexp.MustCompile(`(?m)^\s*package\s+([A-Za-z_$][\w$]*(?:\s*\.\s*[A-Za-z_$][\w$]*)*)\s*;`)

// javaHead is how much of a Java file is read to find its package.
const javaHead = 8 << 10

// javaPackages returns, by directory, the package the first Java file
// (in path order) of each directory declares; directories whose files
// declare none, or cannot be read, are left out.
func javaPackages(root string, files []string) map[string]string {
	out := map[string]string{}
	tried := map[string]bool{}
	for _, f := range files {
		dir := path.Dir(f)
		if !strings.HasSuffix(f, ".java") || tried[dir] {
			continue
		}
		if pkg := declaredPackage(filepath.Join(root, filepath.FromSlash(f))); pkg != "" {
			out[dir], tried[dir] = pkg, true
		}
	}
	return out
}

// declaredPackage returns the package a Java file declares, "" for none.
func declaredPackage(file string) string {
	fh, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer func() { _ = fh.Close() }()
	head, err := io.ReadAll(io.LimitReader(fh, javaHead))
	if err != nil {
		return ""
	}
	m := javaPackageDecl.FindSubmatch(stripJavaComments(head))
	if m == nil {
		return ""
	}
	return strings.Join(strings.Fields(strings.ReplaceAll(string(m[1]), ".", " ")), ".")
}

// javaComment matches a Java block or line comment.
var javaComment = regexp.MustCompile(`(?s)/\*.*?\*/|//[^\n]*`)

func stripJavaComments(src []byte) []byte { return javaComment.ReplaceAll(src, nil) }
