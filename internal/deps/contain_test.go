package deps_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A package may link its files anywhere on the machine. Nothing outside
// its own directory is read on its behalf: not a declaration file, not
// a directory, not the README, not package.json.
func TestExtractionStaysInsideThePackageDirectory(t *testing.T) {
	secret := t.TempDir()
	write(t, filepath.Join(secret, "secret.d.ts"), "export declare const TOP_SECRET: \"hunter2\";\n")
	write(t, filepath.Join(secret, "README.md"), "SECRET README sk-live-999\n")
	write(t, filepath.Join(secret, "package.json"), `{"name": "stolen", "version": "9.9.9"}`)
	sp := sourceSpace(t, "w", map[string]string{
		"package.json": `{"name": "app", "version": "0.0.0", "dependencies": {"evil": "1.0.0", "forged": "1.0.0", "big": "1.0.0"}}`,
		"src/a.ts":     "import { own } from \"evil\";\nimport { fine } from \"big\";\nexport const used = [own, fine];\n",
	}, "", func(repo string) {
		evil := filepath.Join(repo, "node_modules", "evil")
		write(t, filepath.Join(evil, "package.json"), `{"name": "evil", "version": "1.0.0", "types": "index.d.ts"}`)
		write(t, filepath.Join(evil, "index.d.ts"), "/// <reference path=\"link.d.ts\" />\nexport * from \"./link.js\";\n"+
			"export * from \"./dirlink/secret.js\";\nexport declare function own(): void;\n")
		forged := filepath.Join(repo, "node_modules", "forged")
		big := filepath.Join(repo, "node_modules", "big")
		write(t, filepath.Join(big, "package.json"), `{"name": "big", "version": "1.0.0", "types": "index.d.ts"}`)
		write(t, filepath.Join(big, "index.d.ts"), "export declare function fine(): void;\n")
		write(t, filepath.Join(big, "README.md"), "# Big\n"+strings.Repeat("line of text\n", 20000))
		if err := os.MkdirAll(forged, 0o755); err != nil {
			t.Fatal(err)
		}
		for link, target := range map[string]string{
			filepath.Join(evil, "link.d.ts"):      filepath.Join(secret, "secret.d.ts"),
			filepath.Join(evil, "dirlink"):        secret,
			filepath.Join(evil, "README.md"):      filepath.Join(secret, "README.md"),
			filepath.Join(forged, "package.json"): filepath.Join(secret, "package.json"),
		} {
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
		}
	})
	sp.sync(sp.opts)
	evil := sp.pkg("npm/evil@1.0.0")
	eq(t, "only the package's own declarations", evil.keys(), []string{"npm/evil@1.0.0:ts:evil:index.d.ts/own"})
	eq(t, "no README from outside", evil.module(".").DocText, "")
	eq(t, "a linked package.json is not read", sp.states()["package.json forged"], "missing")
	doc := sp.pkg("npm/big@1.0.0").module(".").DocText
	if len(doc) > 32<<10 || !strings.HasPrefix(doc, "# Big\nline of text") {
		t.Errorf("README of %d bytes kept %d", 6+13*20000, len(doc))
	}
}
