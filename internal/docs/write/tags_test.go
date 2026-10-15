package write

import (
	"strings"
	"testing"
)

func TestJavaKeepsMetadataTags(t *testing.T) {
	src := "class A {\n  /**\n   * Old.\n   *\n   * @since 1.2\n   * @author ann\n   * @deprecated use g\n   * @see B\n   * @custom dropped\n   */\n  void f() {}\n}\n"
	root := repo(t, map[string]string{"A.java": src})
	res, err := Apply(Target{Root: root, Path: "A.java", Lang: "java", Qualified: "A.f"}, "New.")
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"@since 1.2", "@author ann", "@deprecated use g", "@see B"} {
		if !strings.Contains(res.Doc.Text, tag) {
			t.Errorf("%s dropped: %q", tag, res.Doc.Text)
		}
	}
	if strings.Contains(res.Doc.Text, "@custom") {
		t.Errorf("unknown tag kept: %q", res.Doc.Text)
	}
}

func TestTSKeepsJSDocTags(t *testing.T) {
	src := "/**\n * Old.\n * @param a the a\n * @returns the sum\n * @throws Error never\n * @deprecated\n * @see g\n * @example f(1)\n * @internal\n */\nexport function f(a: number): number { return a; }\n"
	root := repo(t, map[string]string{"a.ts": src})
	tg := Target{Root: root, Path: "a.ts", Lang: "typescript", Qualified: "a.ts/f"}
	res, err := Apply(tg, "New.")
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"@param a the a", "@returns the sum", "@throws Error never", "@deprecated", "@see g", "@example f(1)"} {
		if !strings.Contains(res.Doc.Text, tag) {
			t.Errorf("%s dropped: %q", tag, res.Doc.Text)
		}
	}
	if strings.Contains(res.Doc.Text, "@internal") {
		t.Errorf("unknown tag kept: %q", res.Doc.Text)
	}
	res, err = Apply(tg, "Newer.\n@param a only")
	if err != nil || strings.Contains(res.Doc.Text, "@returns") {
		t.Fatalf("new tags replace the old ones: %q %v", res.Doc.Text, err)
	}
}
