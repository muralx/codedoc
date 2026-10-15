package resolve_test

import (
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/store"
)

const specifiers = `import a from '@x/web';
import b from '@x/web/src/ui/b.js';
import c from '@x/web/src';
import d from './ui';
import e from './ui/index.js';
import f from '../../../outside.js';
import g from 'lodash';
import h from '.';
import i from './missing/x.js';
export * from './ui/b';
`

func TestTSSpecifiers(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	root := t.TempDir()
	write(t, root, "packages/web/package.json", `{"name": "@x/web"}`)
	write(t, root, "packages/web/src/main.ts", specifiers)
	write(t, root, "packages/web/src/ui/b.ts", "export const b = 1;\n")
	w := &ws{t: t, s: s, repos: []*config.Repo{{Name: "r", Path: root}}}
	w.indexAll()
	pkg, src, ui := w.module("r", "ts", "packages/web"), w.module("r", "ts", "packages/web/src"),
		w.module("r", "ts", "packages/web/src/ui")
	got := w.imports("r", "packages/web/src/main.ts")
	want := map[string]int64{"@x/web": pkg, "@x/web/src/ui/b.js": ui, "@x/web/src": src, "./ui": ui,
		"./ui/index.js": ui, "../../../outside.js": 0, "lodash": 0, ".": src, "./missing/x.js": 0, "./ui/b": ui}
	for raw, id := range want {
		if got[raw] != id {
			t.Errorf("%s resolved to %d, want %d", raw, got[raw], id)
		}
	}
	if e := w.edges(src); len(e) != 2 || !e[pkg] || !e[ui] {
		t.Errorf("edges %v", e)
	}
}

// A specifier naming both a file (utils.ts) and a directory (utils/)
// resolves to the file, as Node and TypeScript try the file first.
func TestTSFileBeforeDirectory(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	root := t.TempDir()
	write(t, root, "package.json", `{"name": "app"}`)
	write(t, root, "src/main.ts", "import a from './utils';\nimport b from './only';\nimport c from 'app/src/utils';\n")
	write(t, root, "src/utils.ts", "export const a = 1;\n")
	write(t, root, "src/utils/x.ts", "export const x = 1;\n")
	write(t, root, "src/only/index.ts", "export const o = 1;\n")
	w := &ws{t: t, s: s, repos: []*config.Repo{{Name: "r", Path: root}}}
	w.indexAll()
	src, only := w.module("r", "ts", "src"), w.module("r", "ts", "src/only")
	got := w.imports("r", "src/main.ts")
	for raw, id := range map[string]int64{"./utils": src, "./only": only, "app/src/utils": src} {
		if got[raw] != id {
			t.Errorf("%s resolved to %d, want %d", raw, got[raw], id)
		}
	}
}
