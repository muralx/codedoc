package embed

import (
	"strings"
	"testing"
)

func TestSymbolText(t *testing.T) {
	body := strings.Repeat("line\n", 15)
	got := SymbolText("pkg/a.go", "Server", "func (s *Server) Run() error", "Run serves.", body)
	lines := strings.Split(got, "\n")
	if len(lines) != 4+BodyLines {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), 4+BodyLines, got)
	}
	if lines[0] != "pkg/a.go" || lines[1] != "Server" || lines[3] != "Run serves." {
		t.Fatalf("unexpected header lines: %q", lines[:4])
	}
	top := SymbolText("a.go", "", "func F()", "F does.", "")
	if top != "a.go\nfunc F()\nF does." {
		t.Fatalf("empty parts not omitted: %q", top)
	}
}

func TestPassageAndSectionText(t *testing.T) {
	if got := PassageText("a.go", "T", "func (T) F()", "\tx := 1\n\treturn x\n"); got != "a.go\nT\nfunc (T) F()\nx := 1\n\treturn x" {
		t.Fatalf("PassageText = %q", got)
	}
	if got := PassageText("a.go", "", "", "type T struct{}"); got != "a.go\ntype T struct{}" {
		t.Fatalf("empty parts not omitted: %q", got)
	}
	long := strings.Repeat("word ", 400)
	if got := SectionText("d.md", "A > B", "B", long); got != "d.md\nA > B\n"+strings.TrimSpace(long) {
		t.Fatalf("a section text is not cut: %d bytes", len(got))
	}
	if got := SectionText("d.md", "", "Title", ""); got != "d.md\nTitle" {
		t.Fatalf("a document takes its title: %q", got)
	}
}

func TestModuleText(t *testing.T) {
	if got := ModuleText("example.com/m/pkg", "pkg", " Package pkg does. \n"); got != "example.com/m/pkg\npkg\nPackage pkg does." {
		t.Fatalf("ModuleText = %q", got)
	}
}

func TestTextHash(t *testing.T) {
	a, b := TextHash("x"), TextHash("y")
	if len(a) != 16 || a == b || a != TextHash("x") {
		t.Fatalf("TextHash: %q %q", a, b)
	}
}

func TestFirstLines(t *testing.T) {
	cases := map[string]string{"": "", "a": "a", "a\nb\nc": "a\nb"}
	for in, want := range cases {
		if got := firstLines(in, 2); got != want {
			t.Errorf("firstLines(%q) = %q, want %q", in, got, want)
		}
	}
}
