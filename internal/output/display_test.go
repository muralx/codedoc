package output_test

import (
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/output"
)

// quiet is the header of a fresh, clean answer of a one-repo workspace:
// it prints no line.
func quiet() output.Header { return output.Header{Repo: "r", Fresh: true} }

// text renders sections without a budget under the quiet header.
func text(t *testing.T, r *output.Response) string {
	t.Helper()
	r.Header = quiet()
	got, _ := render(t, r, "text", 0)
	return got
}

func TestTextPrintsNamesAndKeysOnRequest(t *testing.T) {
	sec := output.Section{Title: "callers of Store.Get", KeyTitle: "callers of r:go:m:Store.Get", Entries: []output.Entry{
		{Key: "r:go:m:Service.Lookup", Name: "Service.Lookup", Loc: "svc.go:7", Text: "func Lookup()"}}}
	if got := text(t, &output.Response{Sections: []output.Section{sec}}); got !=
		"== callers of Store.Get (1)\nService.Lookup  svc.go:7  func Lookup()\n" {
		t.Fatalf("%q", got)
	}
	if got := text(t, &output.Response{Sections: []output.Section{sec}, Keys: true}); got !=
		"== callers of r:go:m:Store.Get (1)\nr:go:m:Service.Lookup  svc.go:7  func Lookup()\n" {
		t.Fatalf("%q", got)
	}
	js, _ := render(t, &output.Response{Header: quiet(), Sections: []output.Section{sec}}, "json", 0)
	if !strings.Contains(js, `"title":"callers of r:go:m:Store.Get"`) ||
		!strings.Contains(js, `"key":"r:go:m:Service.Lookup","name":"Service.Lookup"`) {
		t.Fatal(js)
	}
}

func TestTextTellsApartEntriesThatReadTheSame(t *testing.T) {
	same := func(keys ...string) string {
		var es []output.Entry
		for _, k := range keys {
			es = append(es, output.Entry{Key: k, Name: "Get", Text: "func Get()"})
		}
		return text(t, &output.Response{Sections: []output.Section{{Title: "s", NoTitle: true, Entries: es}}})
	}
	if got := same("a:go:m:Get", "b:go:m:Get"); got != "a:Get  func Get()\nb:Get  func Get()\n" {
		t.Fatalf("repos: %q", got)
	}
	if got := same("a:go:m/x:Get", "a:go:m/y:Get"); got != "m/x:Get  func Get()\nm/y:Get  func Get()\n" {
		t.Fatalf("modules: %q", got)
	}
	if got := same("a:go:m:x/Get", "a:go:m:y/Get"); got != "a:go:m:x/Get  func Get()\na:go:m:y/Get  func Get()\n" {
		t.Fatalf("keys: %q", got)
	}
}

func TestTextPrintsEssentialTagsOnly(t *testing.T) {
	tags := []string{"method", "fan-in:3", "doc:missing", "exact", "test", "stale", "cycle", "+2 members", "1 of 2",
		"dep left-pad@1.0.0"}
	sec := output.Section{Title: "s", NoTitle: true, Entries: []output.Entry{{Key: "k", Tags: tags}}}
	if got := text(t, &output.Response{Sections: []output.Section{sec}}); got !=
		"k  [test] [stale] [cycle] [+2 members] [1 of 2] [dep left-pad@1.0.0]\n" {
		t.Fatalf("%q", got)
	}
	for name, r := range map[string]*output.Response{"detail": {Sections: []output.Section{sec}, Detail: true},
		"all tags": {Sections: []output.Section{{Title: "s", NoTitle: true, AllTags: true, Entries: sec.Entries}}}} {
		if got := text(t, r); !strings.Contains(got, "[method] [fan-in:3] [doc:missing] [exact] [test]") {
			t.Fatalf("%s: %q", name, got)
		}
	}
}

func TestTextNamesADependencyOncePerGroup(t *testing.T) {
	dep := []string{"dep sdk@1.2.0"}
	sec := output.Section{Title: "s", NoTitle: true, Entries: []output.Entry{{Key: "A", Tags: dep}, {Key: "B", Tags: dep},
		{Key: "C", Tags: []string{"dep sdk@2.0.0"}}}}
	if got := text(t, &output.Response{Sections: []output.Section{sec}}); got != "A  [dep sdk@1.2.0]\nB\nC  [dep sdk@2.0.0]\n" {
		t.Fatalf("%q", got)
	}
}

func TestTextHidesWhatSectionAndEntryHide(t *testing.T) {
	es := []output.Entry{{Key: "A", Text: "func A()", Doc: "Does A."},
		{Key: "B", Text: "func B()", Doc: "Does B.", Hide: output.HideText}}
	r := &output.Response{Sections: []output.Section{{Title: "s", NoTitle: true, Hide: output.HideDoc, Entries: es}}}
	if got := text(t, r); got != "A  func A()\nB\n" {
		t.Fatalf("%q", got)
	}
	r.Detail = true
	if got := text(t, r); got != "A  func A()  Does A.\nB  func B()  Does B.\n" {
		t.Fatalf("detail: %q", got)
	}
}

func TestTextPrintsACallSiteInPlaceOfTheSignature(t *testing.T) {
	line := []output.Fragment{{Start: 12, End: 12, Text: "\t\tv := s.Get(key)\n"}}
	wide := []output.Fragment{{Start: 11, End: 13, Text: "\tfor {\n\t\tv := s.Get(key)\n\t}\n"}}
	entry := func(f []output.Fragment) output.Entry {
		return output.Entry{Key: "Lookup", Loc: "svc.go:12", Text: "func Lookup()", Doc: "Looks up.", Site: true, Fragments: f}
	}
	one := &output.Response{Sections: []output.Section{{Title: "s", NoTitle: true, Entries: []output.Entry{entry(line)}}}}
	if got := text(t, one); got != "Lookup  svc.go:12  v := s.Get(key)\n" {
		t.Fatalf("%q", got)
	}
	more := &output.Response{Sections: []output.Section{{Title: "s", NoTitle: true, Entries: []output.Entry{entry(wide)}}}}
	if got := text(t, more); got != "Lookup  svc.go:12\n11\t\tfor {\n12\t\t\tv := s.Get(key)\n13\t\t}\n" {
		t.Fatalf("%q", got)
	}
	one.Detail = true
	if got := text(t, one); got != "Lookup  svc.go:12  func Lookup()  Looks up.\n12\t\t\tv := s.Get(key)\n" {
		t.Fatalf("detail: %q", got)
	}
	js, _ := render(t, one, "json", 0)
	if !strings.Contains(js, `"text":"func Lookup()","doc":"Looks up.","fragments":[{"start":12,"end":12,"text":"\t\tv := s.Get(key)\n"}]`) {
		t.Fatal(js)
	}
}

func TestTextMarksAnAmbiguousEntryAtItsStart(t *testing.T) {
	sec := output.Section{Title: "possibly callers of Get", Entries: []output.Entry{
		{Key: "r:go:m:Run", Name: "Run", Mark: "?", Loc: "run.go:4", Tags: []string{"ambiguous", "test", "1 of 2"}}}}
	if got := text(t, &output.Response{Sections: []output.Section{sec}}); got !=
		"== possibly callers of Get (1)\n? Run  run.go:4  [test] [1 of 2]\n" {
		t.Fatalf("%q", got)
	}
	if got := text(t, &output.Response{Sections: []output.Section{sec}, Keys: true}); !strings.Contains(got, "\n? r:go:m:Run  ") {
		t.Fatalf("keys: %q", got)
	}
	js, _ := render(t, &output.Response{Header: quiet(), Sections: []output.Section{sec}}, "json", 0)
	if !strings.Contains(js, `"tags":["ambiguous","test","1 of 2"]`) || strings.Contains(js, `"?`) {
		t.Fatal(js)
	}
}

func TestTextPrintsAPathOncePerFile(t *testing.T) {
	es := []output.Entry{{Key: "A", Loc: "a.go:1-4"}, {Key: "B", Loc: "a.go:6"}, {Key: "C", Loc: "b.go:2"},
		{Key: "D", Loc: "a.go:9"}}
	if got := text(t, &output.Response{Sections: []output.Section{{Title: "s", NoTitle: true, Entries: es}}}); got !=
		"a.go\n  A  :1-4\n  B  :6\nC  b.go:2\nD  a.go:9\n" {
		t.Fatalf("%q", got)
	}
	// The file of the section is never repeated, and a tree outside it
	// keeps its locations whole.
	in := &output.Response{Sections: []output.Section{{Title: "a.go", Path: "a.go", Entries: es[:2]}}}
	if got := text(t, in); got != "== a.go (2)\nA  :1-4\nB  :6\n" {
		t.Fatalf("path: %q", got)
	}
	tree := []output.Entry{{Key: "A", Loc: "a.go:1"}, {Key: "B", Loc: "a.go:6", Depth: 1}}
	if got := text(t, &output.Response{Sections: []output.Section{{Title: "s", NoTitle: true, Entries: tree}}}); got !=
		"A  a.go:1\n  B  a.go:6\n" {
		t.Fatalf("tree: %q", got)
	}
}

func TestTextPrintsADirectoryOncePerRunOfFiles(t *testing.T) {
	es := []output.Entry{{Loc: "src/store/a.go", Text: "12 lines"}, {Loc: "src/store/b.go", Text: "3 lines"},
		{Loc: "src/main.go", Text: "40 lines"}, {Loc: "README.md", Text: "9 lines"}, {Loc: "go.mod", Text: "3 lines"}}
	r := &output.Response{Sections: []output.Section{{Title: "ls", Dirs: true, Entries: es}}}
	if got := text(t, r); got != "== ls (5)\nsrc/store/\n  a.go  12 lines\n  b.go  3 lines\nsrc/main.go  40 lines\n"+
		"README.md  9 lines\ngo.mod  3 lines\n" {
		t.Fatalf("%q", got)
	}
}

func TestTextNamesTheExactCommandForTheRest(t *testing.T) {
	r := tenEntries()
	r.Command = "codedoc find 'Get*' --kind method"
	got, _ := render(t, r, "text", 300)
	if !strings.Contains(got, " more: codedoc find 'Get*' --kind method --offset 2") {
		t.Fatal(got)
	}
}
