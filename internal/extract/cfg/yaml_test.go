package cfg

import (
	"reflect"
	"testing"
)

// extractCase is one table-test input for a format and the expected output.
type extractCase struct {
	name    string
	src     string
	defs    []KeyDef
	refs    []KeyRef
	wantErr bool
}

// runCases extracts every case with the format's extractor and compares
// definitions and references.
func runCases(t *testing.T, format string, cases []extractCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := For(format).Extract("f", []byte(tc.src))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if got == nil || got.Format != format {
				t.Fatalf("result %+v", got)
			}
			if !reflect.DeepEqual(got.Defs, tc.defs) {
				t.Errorf("defs:\n got %+v\nwant %+v", got.Defs, tc.defs)
			}
			if !reflect.DeepEqual(got.Refs, tc.refs) {
				t.Errorf("refs:\n got %+v\nwant %+v", got.Refs, tc.refs)
			}
		})
	}
}

func TestYAML(t *testing.T) {
	runCases(t, YAML, []extractCase{
		{name: "nested", src: "lib:\n  mode: dev\n  url: ${API_URL:http://x}\ndb:\n  password: hunter2\n",
			defs: []KeyDef{{"lib.mode", 2, "dev"}, {"lib.url", 3, "${API_URL:http://x}"}, {"db.password", 5, "***"}},
			refs: []KeyRef{{"API_URL", 3}}},
		{name: "sequences and empties", src: "hosts:\n  - a\n  - name: b\nnone: []\nobj: {}\nnil:\n",
			defs: []KeyDef{{"hosts.0", 2, "a"}, {"hosts.1.name", 3, "b"}, {"none", 4, "[]"}, {"obj", 5, "{}"}, {"nil", 6, ""}}},
		{name: "multi document and alias", src: "a: &x 1\nb: *x\n---\nc: 2\n",
			defs: []KeyDef{{"a", 1, "1"}, {"b", 2, "*x"}, {"c", 4, "2"}}},
		{name: "merge key", src: "base: &b\n  k1: v\nchild:\n  <<: *b\n  k2: w\n",
			defs: []KeyDef{{"base.k1", 2, "v"}, {"child.k1", 4, "v"}, {"child.k2", 5, "w"}}},
		{name: "literal block refs by line", src: "s: |\n  one\n  ${X}\n",
			defs: []KeyDef{{"s", 1, "one ${X}"}}, refs: []KeyRef{{"X", 3}}},
		{name: "bare scalar document", src: "hello\n"},
		{name: "invalid", src: "a: [1\n", wantErr: true},
	})
}

func TestYAMLMerge(t *testing.T) {
	runCases(t, YAML, []extractCase{
		{name: "merge key", src: "base: &b\n  k1: v\n  k2: ${X}\nchild:\n  <<: *b\n  k2: own\n",
			defs: []KeyDef{{"base.k1", 2, "v"}, {"base.k2", 3, "${X}"}, {"child.k1", 5, "v"}, {"child.k2", 6, "own"}},
			refs: []KeyRef{{"X", 3}}},
		{name: "merge list first wins", src: "a: &a\n  x: 1\nb: &b\n  x: 2\n  y: 3\nc:\n  <<: [*a, *b]\n",
			defs: []KeyDef{{"a.x", 2, "1"}, {"b.x", 4, "2"}, {"b.y", 5, "3"}, {"c.x", 7, "1"}, {"c.y", 7, "3"}}},
		{name: "nested merge and mapping alias", src: "a: &a\n  x:\n    deep: 1\nb: &b\n  <<: *a\n  y: 2\nc: *b\nd: *a\n",
			defs: []KeyDef{{"a.x.deep", 3, "1"}, {"b.x.deep", 5, "1"}, {"b.y", 6, "2"},
				{"c.x.deep", 7, "1"}, {"c.y", 7, "2"}, {"d.x.deep", 8, "1"}}},
		{name: "secret through merge", src: "s: &s\n  password: p\nt:\n  <<: *s\n",
			defs: []KeyDef{{"s.password", 2, "***"}, {"t.password", 4, "***"}}},
	})
}
