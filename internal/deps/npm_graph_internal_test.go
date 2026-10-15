package deps

import (
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/resolve"
)

func TestExportGraphRecordsReexportsByFile(t *testing.T) {
	var g exportGraph
	g.entry("lib", "index")
	g.entry("lib/gone", "gone")
	g.file("index", []extract.Import{
		{Raw: "./ext.js", Alias: "z"},
		{Raw: "./ext.js", Reexport: true},
		{Raw: "./parse.js", Names: []string{"parse", "renamed=alias"}, Reexport: true},
		{Raw: "./parse.js", Names: []string{"imported"}},
		{Raw: "./side.js"},
		{Raw: "./unread.js", Reexport: true},
		{Raw: "bare", Reexport: true},
	}, map[string]string{"./ext.js": "ext.d.cts", "./parse.js": "parse.d.ts", "./side.js": "side.d.ts", "./unread.js": "unread.d.ts"})
	g.file("ext", []extract.Import{{Raw: "./parse.js", Alias: "deep", Reexport: true}}, map[string]string{"./parse.js": "parse.d.ts"})
	x := g.build([]File{{Path: "index.d.ts"}, {Path: "ext.d.ts"}, {Path: "ext.d.cts"}, {Path: "parse.d.ts"}, {Path: "side.d.ts"}})
	want := &resolve.DepExports{
		Files:   []string{"index.d.ts", "ext.d.ts", "parse.d.ts", "side.d.ts"},
		Entries: map[string]int{"lib": 0},
		Stars:   map[int][]int{0: {1}},
		Named:   map[int]map[string]resolve.DepExport{0: {"parse": {File: 2}, "alias": {File: 2, Name: "renamed"}}},
		Spaces:  map[int]map[string]int{0: {"z": 1}, 1: {"deep": 2}},
	}
	if !reflect.DeepEqual(x, want) {
		t.Errorf("graph\n got %+v\nwant %+v", x, want)
	}
	if files, as := x.Lookup("lib", "z", "anything"); !reflect.DeepEqual(files, []string{"ext.d.ts"}) || as != "anything" {
		t.Errorf("namespace z: %v %q", files, as)
	}
	var none exportGraph
	if none.build(nil) != nil {
		t.Error("a package without entry files has no graph")
	}
}
