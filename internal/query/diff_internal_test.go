package query

import (
	"strings"
	"testing"
)

func TestUnifiedDiff(t *testing.T) {
	a := strings.Split("a\nb\nc\nd\ne\nf\ng\nh\ni\nj", "\n")
	b := strings.Split("a\nB\nc\nd\ne\nf\ng\nh\ni\nj\nk", "\n")
	got := unifiedDiff("A:f.go", "B:f.go", a, b, 10, 20)
	want := "--- A:f.go\n+++ B:f.go\n@@ -10,5 +20,5 @@\n a\n-b\n+B\n c\n d\n e\n@@ -17,3 +27,4 @@\n h\n i\n j\n+k\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if d := unifiedDiff("A", "B", a, a, 1, 1); d != "" {
		t.Fatalf("equal input: %q", d)
	}
	if d := unifiedDiff("A", "B", nil, []string{"x"}, 1, 1); d != "--- A\n+++ B\n@@ -0,0 +1,1 @@\n+x\n" {
		t.Fatalf("insert into empty: %q", d)
	}
}

func TestEditScriptFallsBackOnHugeInputs(t *testing.T) {
	big := make([]string, 2100)
	for i := range big {
		big[i] = "x"
	}
	ops := editScript(big, append([]string{"y"}, big...))
	if len(ops) != 2*2100+1 || ops[0].kind != '-' {
		t.Fatalf("fallback ops %d", len(ops))
	}
}
