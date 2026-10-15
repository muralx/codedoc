package resolve_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/resolve"
)

// nsPackage is an npm package shaped like zod: the entry binds a
// namespace to a file that re-exports others, and exports it by name;
// a second entry point declares functions of the same names.
var nsPackage = map[string]string{
	"node_modules/nslib/package.json": `{"name": "nslib", "version": "2.0.0",
  "exports": {".": {"types": "./index.d.ts"}, "./mini": {"types": "./mini/index.d.ts"}}}`,
	"node_modules/nslib/index.d.ts": `import * as z from "./classic/external.js";
export * from "./classic/external.js";
export { z, z as default };
export declare function top(): void;
`,
	"node_modules/nslib/classic/external.d.ts": `export * as core from "../core/index.js";
export * from "./schemas.js";
export { parse, renamed as alias } from "./parse.js";
export * as iso from "./iso.js";
`,
	"node_modules/nslib/classic/schemas.d.ts": `export declare function object(shape: unknown): Box;
export declare class Box { open(): void; }
`,
	"node_modules/nslib/classic/parse.d.ts": `export declare function parse(x: unknown): void;
export declare function renamed(): void;
export declare function hidden(): void;
`,
	"node_modules/nslib/classic/iso.d.ts": "export declare function date(): void;\n",
	"node_modules/nslib/core/index.d.ts":  "export * from \"./api.js\";\n",
	"node_modules/nslib/core/api.d.ts":    "export declare function object(): void;\nexport declare function clone(): void;\n",
	"node_modules/nslib/mini/index.d.ts":  "export declare function object(): void;\nexport declare function parse(): void;\n",
}

const nsApp = `import { z } from "nslib";
import * as all from "nslib";
import { z as zz, top as first } from "nslib";
import * as mini from "nslib/mini";

export function schemas(): void {
  z.object({});
  zz.object({});
  all.object({});
  mini.object();
  z.parse(1);
  z.alias();
  z.hidden();
  all.top();
  first();
  z.missing();
  const made = z.object({});
}
`

func TestCallsThroughANamespaceExportLandInItsFiles(t *testing.T) {
	files := map[string]string{
		"package.json": `{"name": "app", "version": "0.0.0", "dependencies": {"nslib": "^2.0.0"}}`,
		"src/ns.ts":    nsApp,
	}
	for p, body := range nsPackage {
		files[p] = body
	}
	s, _, _ := depWS(t, files)
	sameLines(t, "edges of src/ns.ts", depEdges(t, s, "src/ns.ts"), []string{
		"schemas -> ts:nslib/mini:mini/index.d.ts/object [import]",
		"schemas -> ts:nslib:classic/parse.d.ts/parse [import]",
		"schemas -> ts:nslib:classic/parse.d.ts/renamed [import]",
		"schemas -> ts:nslib:classic/schemas.d.ts/object [import]",
		"schemas -> ts:nslib:classic/schemas.d.ts/object [import]",
		"schemas -> ts:nslib:classic/schemas.d.ts/object [import]",
		"schemas -> ts:nslib:classic/schemas.d.ts/object [import]",
		"schemas -> ts:nslib:index.d.ts/top [import]",
		"schemas -> ts:nslib:index.d.ts/top [import]",
	})
}

func TestDepExportsFollowStarsNamesAndNamespaces(t *testing.T) {
	x := &resolve.DepExports{
		Files:   []string{"index.d.ts", "ext.d.ts", "schemas.d.ts", "parse.d.ts", "loop.d.ts"},
		Entries: map[string]int{"lib": 0},
		Stars:   map[int][]int{0: {1}, 1: {2, 4}, 4: {1}},
		Spaces:  map[int]map[string]int{0: {"z": 1}, 1: {"deep": 3}},
		Named:   map[int]map[string]resolve.DepExport{1: {"alias": {File: 3, Name: "renamed"}, "parse": {File: 3}}},
	}
	for _, c := range []struct {
		spec, ns, name string
		files          []string
		as             string
	}{
		{"lib", "", "object", []string{"index.d.ts", "ext.d.ts", "schemas.d.ts", "loop.d.ts"}, "object"},
		{"lib", "z", "object", []string{"ext.d.ts", "schemas.d.ts", "loop.d.ts"}, "object"},
		{"lib", "z", "alias", []string{"parse.d.ts"}, "renamed"},
		{"lib", "", "parse", []string{"parse.d.ts"}, "parse"},
		{"lib", "deep", "x", []string{"parse.d.ts"}, "x"},
		{"lib", "nope", "x", nil, ""},
		{"other", "", "x", nil, ""},
	} {
		files, as := x.Lookup(c.spec, c.ns, c.name)
		if len(files) != len(c.files) || as != c.as {
			t.Errorf("Lookup(%q, %q, %q) = %v as %q, want %v as %q", c.spec, c.ns, c.name, files, as, c.files, c.as)
			continue
		}
		for i := range files {
			if files[i] != c.files[i] {
				t.Errorf("Lookup(%q, %q, %q) = %v, want %v", c.spec, c.ns, c.name, files, c.files)
			}
		}
	}
}

func TestDepExportsRoundTrip(t *testing.T) {
	x := &resolve.DepExports{Files: []string{"a.d.ts", "b.d.ts"}, Entries: map[string]int{"lib": 0},
		Named: map[int]map[string]resolve.DepExport{0: {"alias": {File: 1, Name: "renamed"}}}}
	var none *resolve.DepExports
	if files, _ := none.Lookup("lib", "", "x"); files != nil {
		t.Error("a package without an export graph exports nothing through it")
	}
	back, err := resolve.DecodeDepExports(x.Encode())
	if err != nil || len(back.Files) != 2 || back.Named[0]["alias"].Name != "renamed" {
		t.Errorf("round trip: %+v %v", back, err)
	}
	if _, err := resolve.DecodeDepExports("{"); err == nil {
		t.Error("garbled JSON decodes")
	}
}

// An npm alias is the import specifier of the workspace, while deps.db
// knows the package by its own name: edges resolve through the alias.
func TestImportsThroughAnNpmAliasLandInTheRealPackage(t *testing.T) {
	s, _, _ := depWS(t, map[string]string{
		"package.json": `{"name": "app", "version": "0.0.0", "dependencies": {"lodash": "npm:lodash-es@^4.0.0"}}`,
		"src/a.ts": `import { chunk } from "lodash";
import { flow } from "lodash/fp";

export function use(): void {
  chunk();
  flow();
}
`,
		"node_modules/lodash/package.json": `{"name": "lodash-es", "version": "4.17.21",
  "exports": {".": {"types": "./index.d.ts"}, "./fp": {"types": "./fp.d.ts"}}}`,
		"node_modules/lodash/index.d.ts": "export declare function chunk(): void;\n",
		"node_modules/lodash/fp.d.ts":    "export declare function flow(): void;\n",
	})
	sameLines(t, "edges of src/a.ts", depEdges(t, s, "src/a.ts"), []string{
		"use -> ts:lodash-es/fp:fp.d.ts/flow [import]",
		"use -> ts:lodash-es:index.d.ts/chunk [import]",
	})
}
