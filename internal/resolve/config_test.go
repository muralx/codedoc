package resolve_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

func allWS(t *testing.T) *ws {
	w := newWS(t, [2]string{"go-lib", "go-lib"}, [2]string{"go-app", "go-app"},
		[2]string{"ts-domain", "ts-domain"}, [2]string{"ts-app", "ts-app"}, [2]string{"py-lib", "py-lib"},
		[2]string{"java-lib", "java-lib"}, [2]string{"cfg-repo", "cfg-repo"})
	w.indexAll()
	return w
}

// uses describes the uses of the key rows named key as
// "kind repo:path:line format" (reads: the reading symbol's key).
func (w *ws) uses(key string) []string {
	w.t.Helper()
	keys, err := w.s.ConfigKeysByName(key, 0)
	if err != nil {
		w.t.Fatal(err)
	}
	ids := make([]int64, len(keys))
	for i, k := range keys {
		ids[i] = k.ID
	}
	us, err := w.s.ConfigUses(ids)
	if err != nil {
		w.t.Fatal(err)
	}
	var out []string
	for _, u := range us {
		s := fmt.Sprintf("%s %s:%s:%d %s", u.Kind, u.Repo, u.Path, u.Line, u.Format)
		if u.Kind == store.ConfigReads {
			s += " " + w.keyOf(u.SymbolID)
		} else if u.Preview != "" {
			s += " " + u.Preview
		}
		out = append(out, s)
	}
	return out
}

func TestConfigKeysAndReads(t *testing.T) {
	w := allWS(t)
	w.want(w.uses("LIB_MODE"),
		"defines cfg-repo:.env:2 env dev",
		"defines cfg-repo:config/settings.json:2 json prod",
		"references cfg-repo:scripts/setup.sh:5 shell",
		"reads go-lib:service/service.go:28 go "+lib+"service:Mode")
	w.want(w.uses("API_URL"),
		"references cfg-repo:web/index.jsp:4 template",
		"reads ts-domain:src/records.ts:32 typescript "+domain+"apiUrl")
	w.want(w.uses("PYLIB_MODE"), "reads py-lib:src/pylib/store.py:52 python "+pyStore+"mode")
	w.want(w.uses("JAVALIB_HOME"), "defines cfg-repo:scripts/setup.sh:4 shell /opt/javalib")
	got := w.uses("lib.mode")
	defines := 0
	for _, u := range got {
		if u[:8] == "defines " {
			defines++
		}
	}
	if keys, _ := w.s.ConfigKeysByName("lib.mode", w.repo("cfg-repo")); len(keys) != 1 || defines < 3 {
		t.Errorf("lib.mode: one key row in cfg-repo, a define per format: %v %v", keys, got)
	}
	if !slices.ContainsFunc(got, func(s string) bool { return len(s) > 6 && s[:6] == "reads " }) {
		t.Errorf("@Value(\"${lib.mode}\") reads lib.mode: %v", got)
	}
	if keys, err := w.s.ConfigKeysMatching("*_MODE", 0); err != nil || len(keys) != 2 {
		t.Errorf("glob *_MODE: %+v %v", keys, err)
	}
}

func TestConfigKeyAddedLaterTurnsACandidateIntoARead(t *testing.T) {
	w := allWS(t)
	unresolved := func() bool {
		f, err := w.s.FileByPath(w.repo("java-lib"), "src/main/java/com/fixture/service/Service.java")
		if err != nil {
			t.Fatal(err)
		}
		refs, err := w.s.UnresolvedReferences(f.ID)
		if err != nil {
			t.Fatal(err)
		}
		return slices.ContainsFunc(refs, func(r store.Reference) bool { return r.Kind == "config" && r.Name == "lib.timeout" })
	}
	if !unresolved() || len(w.uses("lib.timeout")) != 0 {
		t.Fatalf("lib.timeout is defined nowhere yet: %v", w.uses("lib.timeout"))
	}
	cfgRepo := w.repos[6]
	env := filepath.Join(cfgRepo.Path, ".env")
	w.rewrite(cfgRepo, env, func(old string) string { return old + "lib.timeout=45\n" })
	if unresolved() {
		t.Fatal("lib.timeout still unresolved")
	}
	w.want(w.uses("lib.timeout"), "defines cfg-repo:.env:6 env 45",
		"reads java-lib:src/main/java/com/fixture/service/Service.java:16 java "+jService+"Service.timeout")
	w.rewrite(cfgRepo, env, func(string) string { return "LIB_MODE=dev\n" })
	if !unresolved() || len(w.uses("lib.timeout")) != 0 {
		t.Fatalf("a key removed again leaves the read unresolved: %v", w.uses("lib.timeout"))
	}
}

// rewrite replaces the content of file p of repo rep by edit(old) and
// runs the freshness check of the repo.
func (w *ws) rewrite(rep *config.Repo, p string, edit func(string) string) {
	w.t.Helper()
	old, err := os.ReadFile(p)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(edit(string(old))), 0o644); err != nil {
		w.t.Fatal(err)
	}
	if _, err := index.Ensure(w.s, rep, w.opts()); err != nil {
		w.t.Fatal(err)
	}
}
