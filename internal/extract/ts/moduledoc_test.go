package ts_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/extract/ts"
)

func TestModuleDocNeedsBlankLineBeforeDeclaration(t *testing.T) {
	for _, c := range []struct {
		src             string
		module, declDoc string
	}{
		{"/** Mod. */\n\nexport function f() {}\n", "Mod.", ""},
		{"/** Fn. */\nexport function f() {}\n", "", "Fn."},
		{"/** Mod. */\nexport * from './a.js';\n", "Mod.", ""},
		{"/** Mod. */\nimport { a } from './a.js';\nexport function f() {}\n", "Mod.", ""},
		{"/** Mod. */\n", "Mod.", ""},
	} {
		res, err := ts.New("typescript").Extract("src/index.ts", []byte(c.src))
		if err != nil {
			t.Fatal(err)
		}
		module := ""
		if res.ModuleDoc != nil {
			module = res.ModuleDoc.Text
		}
		declDoc := ""
		if len(res.Symbols) > 0 {
			declDoc = docText(res.Symbols[0])
		}
		if module != c.module || declDoc != c.declDoc {
			t.Errorf("%q: module %q decl %q", c.src, module, declDoc)
		}
	}
}
