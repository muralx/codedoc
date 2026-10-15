package query_test

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

// titleTotal matches the count of the first section title of an answer.
var titleTotal = regexp.MustCompile(`(?m)^== .* \((\d+)\)$`)

// The workspace is asked first for a target that names a dependency;
// what that failed attempt noted (an offset past the end of nothing)
// must not reach the answer.
func TestDependencyOutlineKeepsNoNoteOfTheWorkspaceAttempt(t *testing.T) {
	c := depsWorkspace(t)
	c.Repo, c.Cwd = c.Workspace.Repos[0], c.Workspace.Repos[0].Path
	paged := 0
	for _, target := range []string{"@acme/sdk", "@acme/sdk/server", "github.com/acme/kit", "github.com/acme/kit/transport",
		sdkServerFile, "deps-app:" + sdkServerFile, "node_modules/@acme/sdk/dist/shared/protocol.d.ts"} {
		c.Offset = 0
		whole := out(t, must(query.Outline(c, target, query.OutlineOpts{})))
		m := titleTotal.FindStringSubmatch(whole)
		if m == nil {
			t.Fatalf("%s: no title in\n%s", target, whole)
		}
		total, _ := strconv.Atoi(m[1])
		c.Offset = 1
		resp, err := query.Outline(c, target, query.OutlineOpts{})
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		want := ""
		if total <= 1 {
			want = "offset 1 is past the end (total " + m[1] + ")"
		} else {
			paged++
		}
		if resp.Note != want {
			t.Errorf("%s (%d entries) --offset 1: note %q, want %q", target, total, resp.Note, want)
		}
		lines := strings.Split(strings.TrimSuffix(whole, "\n"), "\n")
		got := strings.Split(strings.TrimSuffix(render(t, resp), "\n"), "\n")
		if total > 1 && (lines[1] != got[1] || strings.Join(lines[3:], "\n") != strings.Join(got[2:], "\n")) {
			t.Errorf("%s --offset 1 of\n%s\nis\n%s", target, whole, render(t, resp))
		}
	}
	if paged < 2 {
		t.Fatalf("only %d targets have a second entry to page to", paged)
	}
}
