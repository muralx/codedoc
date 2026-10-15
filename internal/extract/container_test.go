package extract_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/extract"
)

func containerSyms(memberSig, memberBody string) []extract.Symbol {
	return []extract.Symbol{
		{Kind: extract.KindStruct, Qualified: "T", SigText: "type T struct", BodyHash: "whole",
			Span: extract.Span{StartLine: 1, EndLine: 10}},
		{Kind: extract.KindField, Qualified: "T.a", Container: "T", SigText: memberSig,
			Span: extract.Span{StartLine: 2, EndLine: 2}, BodyHash: extract.BodyHash(memberSig + memberBody)},
		{Kind: extract.KindMethod, Qualified: "T.m", Container: "T", SigText: "func (T) m()",
			Span: extract.Span{StartLine: 12, EndLine: 14}},
		{Kind: extract.KindFunction, Qualified: "f", SigText: "func f()", BodyHash: "fn"},
	}
}

func TestHashContainersCoversSignaturesOnly(t *testing.T) {
	a := containerSyms("a int", "")
	extract.HashContainers(a, nil)
	b := containerSyms("a int", "a body edit")
	extract.HashContainers(b, nil)
	if a[0].BodyHash == "whole" || a[0].BodyHash != b[0].BodyHash {
		t.Fatalf("member bodies must not move the container hash: %q %q", a[0].BodyHash, b[0].BodyHash)
	}
	if a[3].BodyHash != "fn" {
		t.Fatal("functions keep their hash")
	}
	c := containerSyms("a string", "")
	extract.HashContainers(c, nil)
	if c[0].BodyHash == a[0].BodyHash {
		t.Fatal("a member signature change must move the container hash")
	}
	// A method declared outside the container's span (Go) is no member.
	d := containerSyms("a int", "")
	d[2].SigText = "func (T) m(x int)"
	extract.HashContainers(d, nil)
	if d[0].BodyHash != a[0].BodyHash {
		t.Fatal("declarations outside the container span are not members")
	}
	e := containerSyms("a int", "")
	extract.HashContainers(e, map[string]string{"T": "io.Reader"})
	if e[0].BodyHash == a[0].BodyHash {
		t.Fatal("extra declaration text counts")
	}
}

func TestIsContainer(t *testing.T) {
	for _, k := range []extract.Kind{extract.KindClass, extract.KindStruct, extract.KindInterface, extract.KindTrait, extract.KindEnum} {
		if !extract.IsContainer(k) {
			t.Fatalf("%s is a container", k)
		}
	}
	if extract.IsContainer(extract.KindFunction) || extract.IsContainer(extract.KindTypeAlias) {
		t.Fatal("functions and aliases are not containers")
	}
}
