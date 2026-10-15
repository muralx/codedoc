package resolve_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/index"
)

// The fixtures testdata/typed hold one probe per shape of a receiver
// typed through declared types: a field of an interface, of an object
// type alias, of a class or of a struct, one or two hops, this and the
// method receiver, embedded structs, a destructured parameter, a type
// of a dependency, the element of a loop, the declared result of a
// method, and the hops that must leave a reference where it was.

func TestTypeScriptReceiversResolveThroughDeclaredTypes(t *testing.T) {
	files := scopeFiles(t, "typed/ts")
	for p, body := range libPackage {
		files[p] = body
	}
	s, _, _ := depWS(t, files)
	checkProbes(t, s, "src/a.ts", files["src/a.ts"])
}

func TestGoReceiversResolveThroughDeclaredTypes(t *testing.T) {
	kitModule(t)
	files := scopeFiles(t, "typed/go")
	s, _, _ := depWS(t, files)
	checkProbes(t, s, "main.go", files["main.go"])
}

// A field whose declared type changes re-resolves the references that
// reach it through a variable: its name is recorded in their type path.
func TestTypeScriptFieldTypeChangeReresolvesTypePaths(t *testing.T) {
	files := map[string]string{
		"package.json": `{"name": "app", "version": "0.0.0"}`,
		"src/kit.ts":   "export class A { run(): void {} }\nexport class B { run(): void {} }\n",
		"src/opts.ts":  "import { A, B } from \"./kit\";\nexport interface Options { target: A; }\n",
		"src/use.ts": "import { Options } from \"./opts\";\n" +
			"export function use({ target }: Options, o: Options): void {\n  target.run();\n  o.target.run();\n}\n",
	}
	s, ws, _ := depWS(t, files)
	sameLines(t, "before", callsOf(t, s, "src/use.ts"), []string{
		"use -> src/kit.ts/A.run [import]", "use -> src/kit.ts/A.run [import]",
	})
	write(t, ws.Repos[0].Path, "src/opts.ts", "import { A, B } from \"./kit\";\nexport interface Options { target: B; }\n")
	if _, err := index.Repo(s, ws.Repos[0], index.Options{Workspace: ws.Name}); err != nil {
		t.Fatal(err)
	}
	if err := index.ResolvePending(s, index.Options{Workspace: ws.Name}); err != nil {
		t.Fatal(err)
	}
	sameLines(t, "after", callsOf(t, s, "src/use.ts"), []string{
		"use -> src/kit.ts/B.run [import]", "use -> src/kit.ts/B.run [import]",
	})
}
