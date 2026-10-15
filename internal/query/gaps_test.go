package query_test

import (
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/query"
)

func TestGapsWorkspace(t *testing.T) {
	c := workspaceOf(t, "go-lib", "go-app", "py-lib")
	c.Now = func() time.Time { return time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC) }
	r, err := query.Gaps(c, "", query.GapsOpts{})
	text := run(t, r, err)
	snapshot(t, "gaps_workspace", text)
	mustContain(t, text, "== gaps (", "[#1] [score ", "changed 19d ago")
	if strings.Contains(text, "store_test.go") || strings.Contains(text, "tests/") {
		t.Fatalf("tests are excluded:\n%s", text)
	}
	r, err = query.Gaps(c, "", query.GapsOpts{Kind: "module", Limit: 1})
	if text = run(t, r, err); !strings.Contains(text, "== gaps (1)") || !strings.Contains(text, "module, fan-in") {
		t.Fatalf("--kind module --limit 1:\n%s", text)
	}
	r, err = query.Gaps(c, "github.com/fixture/lib/store", query.GapsOpts{Kind: "missing"})
	text = run(t, r, err)
	if strings.Contains(text, "service/") || !strings.Contains(text, "store/null.go") {
		t.Fatalf("module scope:\n%s", text)
	}
	if _, err := query.Gaps(c, "", query.GapsOpts{Kind: "bogus"}); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if _, err := query.Gaps(c, "no/such/module", query.GapsOpts{}); err == nil {
		t.Fatal("unknown module accepted")
	}
}
