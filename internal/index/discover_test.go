package index_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/testutil"
)

func discoverMap(t *testing.T, root string, exclude []string) map[string]index.Discovered {
	t.Helper()
	files, _, err := index.Discover(root, exclude, nil)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]index.Discovered{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	return byPath
}

func writeFile(t *testing.T, root, rel string, body []byte) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverListsSourceFilesWithLanguageAndHash(t *testing.T) {
	byPath := discoverMap(t, testutil.CopyFixture(t, "go-lib"), nil)
	f := byPath["store/store.go"]
	if f.Lang != "go" || f.Hash == "" || f.Lines != 38 || f.Size == 0 || f.Mtime == 0 || f.Generated {
		t.Fatalf("%+v", f)
	}
	if !byPath["service/gen.go"].Generated {
		t.Fatal("generated file not flagged")
	}
	if m, ok := byPath["go.mod"]; !ok || m.Lang != "" {
		t.Fatal("go.mod must be discovered (lang empty, used by identity map)")
	}
}

func TestDiscoverDetectsTypeScriptLanguages(t *testing.T) {
	root := testutil.CopyFixture(t, "ts-domain")
	for _, n := range []string{"a.tsx", "b.js", "c.jsx", "d.mjs", "e.cjs"} {
		writeFile(t, root, "src/"+n, []byte("export {};\n"))
	}
	byPath := discoverMap(t, root, nil)
	want := map[string]string{"src/records.ts": "typescript", "src/a.tsx": "tsx", "src/b.js": "javascript",
		"src/c.jsx": "javascript", "src/d.mjs": "javascript", "src/e.cjs": "javascript", "package.json": ""}
	for p, lang := range want {
		if f, ok := byPath[p]; !ok || f.Lang != lang {
			t.Errorf("%s: %+v", p, f)
		}
	}
}

func TestDiscoverDetectsConfigFormats(t *testing.T) {
	root := testutil.CopyFixture(t, "cfg-repo")
	writeFile(t, root, "bin/run", []byte("#!/bin/sh\necho $HOME\n"))
	writeFile(t, root, "bin/data", []byte("plain text\n"))
	byPath := discoverMap(t, root, nil)
	want := map[string]string{".env": "env", "config/application.yaml": "yaml", "config/app.properties": "properties",
		"config/settings.json": "json", "infra/main.tf": "hcl", "pom.xml": "xml", "scripts/setup.sh": "shell",
		"web/index.jsp": "template", "bin/run": "shell", "bin/data": "", "README.md": "markdown"}
	for p, lang := range want {
		if f, ok := byPath[p]; !ok || f.Lang != lang {
			t.Errorf("%s: %+v", p, f)
		}
	}
}

func TestDiscoverHonoursExcludeGlobs(t *testing.T) {
	files, _, err := index.Discover(testutil.CopyFixture(t, "go-lib"), []string{"service/**"}, nil)
	if err != nil || len(files) == 0 {
		t.Fatalf("%v %v", files, err)
	}
	for _, f := range files {
		if strings.HasPrefix(f.Path, "service/") {
			t.Fatalf("excluded file present: %s", f.Path)
		}
	}
}

func TestDiscoverSkipsBuiltinIgnoredBinaryAndLargeFiles(t *testing.T) {
	root := testutil.CopyFixture(t, "go-lib")
	writeFile(t, root, ".gitignore", []byte("*.log\n"))
	writeFile(t, root, "debug.log", []byte("x\n"))
	writeFile(t, root, "vendor/x/x.go", []byte("package x\n"))
	writeFile(t, root, "web/node_modules/p/i.js", []byte("x\n"))
	writeFile(t, root, "dist/out.js", []byte("x\n"))
	writeFile(t, root, "img.png", []byte("\x89PNG\x00\x00"))
	writeFile(t, root, "big.go", []byte(strings.Repeat("// pad\n", 400000)))
	byPath := discoverMap(t, root, nil)
	for _, p := range []string{"debug.log", "vendor/x/x.go", "web/node_modules/p/i.js", "dist/out.js", "img.png", "big.go"} {
		if _, ok := byPath[p]; ok {
			t.Errorf("%s should be skipped", p)
		}
	}
	if _, ok := byPath[".gitignore"]; !ok {
		t.Error(".gitignore should be listed")
	}
}

func TestDiscoverSkipsTrackedFileDeletedFromDisk(t *testing.T) {
	root := testutil.CopyFixture(t, "go-lib")
	if err := os.Remove(filepath.Join(root, "store", "null.go")); err != nil {
		t.Fatal(err)
	}
	if _, ok := discoverMap(t, root, nil)["store/null.go"]; ok {
		t.Fatal("deleted file listed")
	}
}

func TestDiscoverWalksDirectoryThatIsNotARepo(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", []byte("package a"))
	writeFile(t, root, "sub/b.ts", []byte("// @generated\nexport {};\n"))
	writeFile(t, root, "node_modules/m/i.js", []byte("x\n"))
	writeFile(t, root, ".git/config", []byte("x\n"))
	byPath := discoverMap(t, root, nil)
	if len(byPath) != 2 || byPath["a.go"].Lines != 1 || !byPath["sub/b.ts"].Generated {
		t.Fatalf("%+v", byPath)
	}
}

func TestDiscoverRejectsBadGlob(t *testing.T) {
	if _, _, err := index.Discover(testutil.CopyFixture(t, "go-lib"), []string{"[unclosed"}, nil); err == nil {
		t.Fatal("expected error for malformed glob")
	}
}

func TestDiscoverSkipsUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads files regardless of permissions")
	}
	root := testutil.CopyFixture(t, "go-lib")
	p := filepath.Join(root, "store", "null.go")
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o644) })
	files, skipped, err := index.Discover(root, nil, nil)
	if err != nil || skipped != 1 {
		t.Fatalf("skipped %d err %v", skipped, err)
	}
	for _, f := range files {
		if f.Path == "store/null.go" {
			t.Fatal("unreadable file listed")
		}
	}
}

// TestDiscoverExcludesTestdataAndBuildOutputOnlyNextToManifests checks
// testdata is excluded at any depth and build, dist and target only at
// the root or next to a build manifest, in a git repo and in a walk.
func TestDiscoverExcludesTestdataAndBuildOutputOnlyNextToManifests(t *testing.T) {
	files := map[string]string{
		"go.mod": "module example.com/m\n", "a.go": "package m\n",
		"pkg/testdata/in.go": "package x\n", "build/out.go": "package out\n",
		"internal/build/build.go": "package build\n", "internal/dist/dist.go": "package dist\n",
		"web/package.json": "{}\n", "web/dist/app.js": "x\n", "web/src/target/t.ts": "export {};\n",
	}
	want := map[string]bool{"go.mod": true, "a.go": true, "internal/build/build.go": true,
		"internal/dist/dist.go": true, "web/package.json": true, "web/src/target/t.ts": true}
	walked := t.TempDir()
	for p, body := range files {
		writeFile(t, walked, p, []byte(body))
	}
	for name, root := range map[string]string{"git": testutil.SourceRepo(t, "m", files), "walk": walked} {
		byPath := discoverMap(t, root, nil)
		for p := range files {
			if _, ok := byPath[p]; ok != want[p] {
				t.Errorf("%s: %s listed %v, want %v", name, p, ok, want[p])
			}
		}
	}
}
