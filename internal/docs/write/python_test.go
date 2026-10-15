package write

import (
	"strings"
	"testing"
)

func TestPythonSymbolDocs(t *testing.T) {
	src := "class Store:\n    # cache\n    def get(self, k):  # hot\n        return k\n\n    def put(self, k):\n        \"\"\"Old.\"\"\"\n        pass\n\n\ndef one(): return 1\n\n\n@deco\ndef path(p):\n\tpass\n"
	files := map[string]string{"m.py": src}
	tg := func(q string) Target { return Target{Path: "m.py", Lang: "python", Qualified: q} }
	long := strings.Repeat("word ", 20) + "end."
	runRoundTrips(t, []roundTrip{
		{name: "class", files: files, tg: tg("Store"), text: "Stores.", file: "m.py",
			want: "class Store:\n    \"\"\"Stores.\"\"\"\n" + src[len("class Store:\n"):], doc: "Stores."},
		{name: "method after comment line", files: files, tg: tg("Store.get"), text: "Gets.\n\nFast.", file: "m.py",
			want: "class Store:\n    # cache\n    def get(self, k):  # hot\n        \"\"\"Gets.\n\n        Fast.\n        \"\"\"\n        return k\n" + src[len("class Store:\n    # cache\n    def get(self, k):  # hot\n        return k\n"):],
			doc:  "Gets.\n\nFast."},
		{name: "replace", files: files, tg: tg("Store.put"), text: long, file: "m.py",
			want: strings.Replace(src, `"""Old."""`, `"""`+strings.Repeat("word ", 13)+"word\n        word word word word word word end.\n        \"\"\"", 1),
			doc:  strings.Repeat("word ", 13) + "word\nword word word word word word end."},
		{name: "inline body", files: files, tg: tg("one"), text: `Matches \d.`, file: "m.py",
			want: strings.Replace(src, "def one(): return 1", "def one():\n    r\"\"\"Matches \\d.\"\"\"\n    return 1", 1), doc: `Matches \d.`},
		{name: "tabs", files: files, tg: tg("path"), text: `Says "hi"`, file: "m.py",
			want: strings.Replace(src, "def path(p):\n", "def path(p):\n\t\"\"\"Says \"hi\"\n\t\"\"\"\n", 1), doc: `Says "hi"`},
	})
}

func TestPythonInlineDocstring(t *testing.T) {
	runRoundTrips(t, []roundTrip{
		{name: "inline docstring", files: map[string]string{"m.py": "def f(): \"\"\"Old.\"\"\"\n"},
			tg: Target{Path: "m.py", Lang: "python", Qualified: "f"}, text: "New.", file: "m.py",
			want: "def f():\n    \"\"\"New.\"\"\"\n", doc: "New."},
	})
}

func TestPythonRefusals(t *testing.T) {
	root := repo(t, map[string]string{"m.py": "X = 1\n\ndef f():\n    pass\n"})
	if _, err := Plan(Target{Root: root, Path: "m.py", Lang: "python", Qualified: "X"}, "x"); err == nil {
		t.Fatal("docstring on a constant accepted")
	}
	if _, err := Plan(Target{Root: root, Path: "m.py", Lang: "python", Qualified: "f"}, `a """ b`); err == nil {
		t.Fatal("triple quotes accepted")
	}
	if _, err := Plan(Target{Root: root, Path: ".", Lang: "python", ModuleDir: "."}, `a """ b`); err == nil {
		t.Fatal("triple quotes accepted in a module doc")
	}
}

func TestPythonModuleDocs(t *testing.T) {
	runRoundTrips(t, []roundTrip{
		{name: "create __init__", files: map[string]string{"pkg/a.py": ""}, tg: Target{Path: "pkg", Lang: "python", ModuleDir: "pkg"},
			text: "Package.", file: "pkg/__init__.py", want: "\"\"\"Package.\"\"\"\n", doc: "Package."},
		{name: "file module after shebang", files: map[string]string{"tool.py": "#!/usr/bin/env python\n# license\n\nimport os\n"},
			tg: Target{Path: "tool.py", Lang: "python", ModuleDir: "tool.py"}, text: "Tool.", file: "tool.py",
			want: "#!/usr/bin/env python\n# license\n\n\"\"\"Tool.\"\"\"\n\nimport os\n", doc: "Tool."},
		{name: "comments only", files: map[string]string{"p/__init__.py": "# c"}, tg: Target{Path: "p", Lang: "python", ModuleDir: "p"},
			text: "P.", file: "p/__init__.py", want: "# c\n\"\"\"P.\"\"\"\n", doc: "P."},
		{name: "replace", files: map[string]string{"p/__init__.py": "'''Old\n\nmore'''\nimport os\n"}, tg: Target{Path: "p", Lang: "python", ModuleDir: "p"},
			text: "New.", file: "p/__init__.py", want: "\"\"\"New.\"\"\"\nimport os\n", doc: "New."},
	})
}
