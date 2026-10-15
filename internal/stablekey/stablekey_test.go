package stablekey_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/stablekey"
)

func TestStableKeyRoundTrip(t *testing.T) {
	k := stablekey.Key{Repo: "auth", Lang: "go", Module: "github.com/acme/auth/internal/services", Qualified: "TokenService.Issue", SigHash: ""}
	s := k.String()
	if s != "auth:go:github.com/acme/auth/internal/services:TokenService.Issue" {
		t.Fatal(s)
	}
	back, err := stablekey.Parse(s)
	if err != nil || back != k {
		t.Fatalf("%+v %v", back, err)
	}
	withHash := stablekey.Key{Repo: "p", Lang: "java", Module: "com/acme/ptp", Qualified: "A.get", SigHash: "7f3a"}
	if back, err := stablekey.Parse(withHash.String()); err != nil || back != withHash {
		t.Fatal("hash round trip")
	}
	if _, err := stablekey.Parse("only:two"); err == nil {
		t.Fatal("expected error")
	}
}

func TestStableKeyQualifiedMayHoldColonsAndHashes(t *testing.T) {
	k := stablekey.Key{Repo: "d", Lang: "ts", Module: "src", Qualified: "src/a.test.ts/it:fixes #12"}
	if back, err := stablekey.Parse(k.String()); err != nil || back != k {
		t.Fatalf("%+v", back)
	}
}

func TestStableKeyPrivateMembersAreNotHashes(t *testing.T) {
	for in, want := range map[string]stablekey.Key{
		"r:ts:@x/y:src/a.ts/Base.#secret": {Repo: "r", Lang: "ts", Module: "@x/y", Qualified: "src/a.ts/Base.#secret"},
		"r:ts:@x/y:src/a.ts/Base.#cafe":   {Repo: "r", Lang: "ts", Module: "@x/y", Qualified: "src/a.ts/Base.#cafe"},
		"r:java:com/x:A.get#7f3a":         {Repo: "r", Lang: "java", Module: "com/x", Qualified: "A.get", SigHash: "7f3a"},
		"r:ts:m:a.ts/Base.#cafe#00ff":     {Repo: "r", Lang: "ts", Module: "m", Qualified: "a.ts/Base.#cafe", SigHash: "00ff"},
		"r:java:p:X.m#7f3a#00ff":          {Repo: "r", Lang: "java", Module: "p", Qualified: "X.m", SigHash: "7f3a#00ff"},
		"r:java:p:X.m#7f3a#0123abcd-2":    {Repo: "r", Lang: "java", Module: "p", Qualified: "X.m", SigHash: "7f3a#0123abcd-2"},
		"r:go:p:Open#0123abcd":            {Repo: "r", Lang: "go", Module: "p", Qualified: "Open", SigHash: "0123abcd"},
	} {
		got, err := stablekey.Parse(in)
		if err != nil || got != want {
			t.Errorf("%s: %+v %v", in, got, err)
		}
		if got.String() != in {
			t.Errorf("%s: round trip %s", in, got.String())
		}
	}
}

func TestNewStableKeyReplacesColonsInIdentities(t *testing.T) {
	k := stablekey.New("r", "java", "com.acme:ptp", "A.get", "")
	if k.Module != "com.acme/ptp" || k.String() != "r:java:com.acme/ptp:A.get" {
		t.Fatalf("%+v", k)
	}
}

func TestKnownLang(t *testing.T) {
	for _, l := range []string{"go", "ts", "py", "java"} {
		if !stablekey.KnownLang(l) {
			t.Fatal(l)
		}
	}
	if stablekey.KnownLang("typescript") || stablekey.KnownLang("") {
		t.Fatal("precise names are not key segments")
	}
}

func TestIsKeyRepoAndModule(t *testing.T) {
	if !stablekey.IsKey("r:go:p:Open#00ff") || stablekey.IsKey("r:rs:p:Open") || stablekey.IsKey("a:b") ||
		stablekey.IsKey("r:go:p:") {
		t.Fatal("IsKey")
	}
	if stablekey.Repo("r:go:a/b:Open") != "r" || stablekey.Module("r:go:a/b:Open") != "a/b" {
		t.Fatal("segments")
	}
	if stablekey.Repo("bare") != "" || stablekey.Module("x:y") != "" {
		t.Fatal("not a key")
	}
}

func TestKeyBase(t *testing.T) {
	for _, c := range []struct {
		key  string
		sig  bool
		want string
	}{
		{"r:go:p:Open", false, "r:go:p:Open"},
		{"r:go:p:Open#00ff", false, "r:go:p:Open"},
		{"r:go:p:Open#0123abcd-2", false, "r:go:p:Open"},
		{"r:ts:m:a.ts/Base.#cafe", false, "r:ts:m:a.ts/Base.#cafe"},
		{"r:java:p:X.m#7f3a", true, "r:java:p:X.m#7f3a"},
		{"r:java:p:X.m#7f3a#00ff", true, "r:java:p:X.m#7f3a"},
		{"r:java:p:X.m#7f3a#0123abcd", true, "r:java:p:X.m#7f3a"},
	} {
		if got := stablekey.Base(c.key, c.sig); got != c.want {
			t.Errorf("%s (sig %v): %s", c.key, c.sig, got)
		}
	}
}

// A dependency symbol's repo segment is a package id, which holds '/' and
// '@' and, for a scoped npm package, starts its name with '@' (deps spec
// section 3).
func TestParseAcceptsPackageIDsAsRepo(t *testing.T) {
	for key, want := range map[string]stablekey.Key{
		"npm/@modelcontextprotocol/sdk@1.20.0:ts:@modelcontextprotocol/sdk/server/index.js:dist/esm/server/index.d.ts/Server.notification": {
			Repo: "npm/@modelcontextprotocol/sdk@1.20.0", Lang: "ts", Module: "@modelcontextprotocol/sdk/server/index.js",
			Qualified: "dist/esm/server/index.d.ts/Server.notification"},
		"go/github.com/spf13/cobra@v1.9.1:go:github.com/spf13/cobra:Command.Execute": {
			Repo: "go/github.com/spf13/cobra@v1.9.1", Lang: "go", Module: "github.com/spf13/cobra", Qualified: "Command.Execute"},
		"go/github.com/x/y@v2.0.0+incompatible:go:github.com/x/y:New#1a2b": {
			Repo: "go/github.com/x/y@v2.0.0+incompatible", Lang: "go", Module: "github.com/x/y", Qualified: "New", SigHash: "1a2b"},
		"npm/zod@3.23.8:ts:zod:index.d.ts/z.#brand": {
			Repo: "npm/zod@3.23.8", Lang: "ts", Module: "zod", Qualified: "index.d.ts/z.#brand"},
	} {
		got, err := stablekey.Parse(key)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", key, got, err, want)
		}
		if got.String() != key || !stablekey.IsKey(key) || stablekey.Repo(key) != want.Repo {
			t.Errorf("%q does not round-trip: %q", key, got.String())
		}
		if built := stablekey.New(want.Repo, want.Lang, want.Module, want.Qualified, want.SigHash); built != want {
			t.Errorf("New changed a package id: %+v", built)
		}
	}
}

func TestDocumentKeys(t *testing.T) {
	for in, want := range map[string]stablekey.Key{
		"r:doc:docs/spec.md:":                    {Repo: "r", Lang: "doc", Module: "docs/spec.md"},
		"r:doc:docs/spec.md:6. Delivery > 6.2 R": {Repo: "r", Lang: "doc", Module: "docs/spec.md", Qualified: "6. Delivery > 6.2 R"},
		"r:doc:a.md:Issue #1234":                 {Repo: "r", Lang: "doc", Module: "a.md", Qualified: "Issue #1234"},
		"r:doc:a.md:Step 2: build":               {Repo: "r", Lang: "doc", Module: "a.md", Qualified: "Step 2: build"},
	} {
		got, err := stablekey.Parse(in)
		if err != nil || got != want || got.String() != in || !stablekey.IsKey(in) {
			t.Errorf("Parse(%q) = %+v, %v", in, got, err)
		}
	}
	// Only a document may have an empty qualified name.
	if _, err := stablekey.Parse("r:go:m:"); err == nil {
		t.Error("an empty qualified name parsed for go")
	}
	if got := stablekey.Module("r:doc:docs/spec.md:Goals"); got != "docs/spec.md" {
		t.Errorf("Module = %q", got)
	}
}
