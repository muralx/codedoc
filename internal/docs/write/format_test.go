package write

import (
	"strings"
	"testing"
)

func TestWrap(t *testing.T) {
	for _, c := range []struct {
		text  string
		width int
		want  string
	}{
		{"a b c d", 40, "a b c d"},
		{strings.Repeat("ab ", 20), 40, strings.TrimSpace(strings.Repeat("ab ", 13)) + "\n" + strings.TrimSpace(strings.Repeat("ab ", 7))},
		{"intro:\n- one\n- two\n1. three\n2) four", 40, "intro:\n- one\n- two\n1. three\n2) four"},
		{"para one\n\n\n  code\n  block\n\nend", 40, "para one\n\n  code\n  block\n\nend"},
		{"@param a x\ny", 10, "@param a x y"},
		{strings.Repeat("x", 50), 40, strings.Repeat("x", 50)},
	} {
		if got := strings.Join(wrap(c.text, c.width), "\n"); got != c.want {
			t.Errorf("wrap(%q):\n%s\nwant:\n%s", c.text, got, c.want)
		}
	}
}

func TestNormalizeAndClean(t *testing.T) {
	if got := normalize("a\n  b\n\n\nc  d"); got != "a b\n\nc d" {
		t.Fatalf("%q", got)
	}
	if got := cleanText("\r\n a  \r\n\r\nb\t\n\n"); got != " a\n\nb" {
		t.Fatalf("%q", got)
	}
}

func TestPrefixWord(t *testing.T) {
	for in, want := range map[string]string{
		"Returns x.":         "F returns x.",
		"F returns x.":       "F returns x.",
		"f returns x.":       "f returns x.",
		"Foo is x.":          "F Foo is x.",
		"URLs are x.":        "F URLs are x.",
		"Kafka consumer.":    "F Kafka consumer.",
		"The value of x.":    "F the value of x.",
		"This holds.":        "F this holds.",
		"Is set when ready.": "F is set when ready.",
		"x":                  "F x",
		"Creates a new one.": "F creates a new one.",
		"Returned values.":   "F Returned values.",
	} {
		if got := prefixWord(in, "F"); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
	if got := prefixWord("package p does x.", "Package p"); got != "package p does x." {
		t.Errorf("case-insensitive package clause: %q", got)
	}
	if !startsWithWord("F", "F") || startsWithWord("Foo", "F") {
		t.Fatal("startsWithWord")
	}
}

func TestReplaceSpanAroundCode(t *testing.T) {
	src := []byte("x = 1; /* a */ y = 2\n")
	start := uint(strings.Index(string(src), "/*"))
	end := start + uint(len("/* a */"))
	got := string(replaceSpan(src, start, end, []string{"/* b */"}, "  "))
	if got != "x = 1;\n  /* b */\n  y = 2\n" {
		t.Fatalf("%q", got)
	}
	got = string(replaceSpan([]byte("/* a */"), 0, 7, []string{"/* b */"}, ""))
	if got != "/* b */\n" {
		t.Fatalf("at EOF: %q", got)
	}
}

func TestDeclarationNotStartingItsLine(t *testing.T) {
	for lang, src := range map[string]string{
		"go":         "package a\n\nvar x = 1; func F() {}\n",
		"typescript": "let x = 1; function f() {}\n",
	} {
		root := repo(t, map[string]string{"a": src})
		q := "F"
		if lang != "go" {
			q = "a/f"
		}
		if _, err := Plan(Target{Root: root, Path: "a", Lang: lang, Qualified: q}, "Doc."); err == nil {
			t.Errorf("%s: accepted", lang)
		}
	}
}
