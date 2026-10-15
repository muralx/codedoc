package scip

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

const replySrc = `package service

type failure struct{}

// Error describes the failure.
func (failure) Error() string { return "failed" }

type errorReply struct{ Error string }

func reply(f failure) errorReply { return errorReply{Error: "x" + f.Error()} }
`

// scipSym is a scip-go symbol of the fixture's service package.
func scipSym(desc string) string {
	return "scip-go gomod github.com/fixture/lib v0 `github.com/fixture/lib/service`/" + desc
}

// occAt is an occurrence of the n-th (from 0) ident on a 1-based line of
// src.
func occAt(t *testing.T, src string, line int, ident string, n int, sym string, def bool) Occurrence {
	t.Helper()
	l := strings.Split(src, "\n")[line-1]
	col := -1
	for i := 0; i <= n; i++ {
		next := strings.Index(l[col+1:], ident)
		if next < 0 {
			t.Fatalf("line %d has no %q #%d", line, ident, n)
		}
		col += next + 1
	}
	return Occurrence{Path: "service/reply.go", Line: line, Col: col, EndLine: line, EndCol: col + len(ident),
		Symbol: sym, Definition: def, Ref: !def}
}

// replyIndex indexes reply.go: the definitions, then the uses on the
// keyed literal line, the field key before the method call.
func replyIndex(t *testing.T) *Index {
	t.Helper()
	failure, failErr := scipSym("failure#"), scipSym("failure#Error().")
	reply, replyErr, fn := scipSym("errorReply#"), scipSym("errorReply#Error."), scipSym("reply().")
	occ := []Occurrence{
		occAt(t, replySrc, 3, "failure", 0, failure, true),
		occAt(t, replySrc, 6, "Error", 0, failErr, true),
		occAt(t, replySrc, 8, "errorReply", 0, reply, true),
		occAt(t, replySrc, 8, "Error", 0, replyErr, true),
		occAt(t, replySrc, 10, "reply", 0, fn, true),
		occAt(t, replySrc, 10, "failure", 0, failure, false),
		occAt(t, replySrc, 10, "errorReply", 1, reply, false),
		occAt(t, replySrc, 10, "Error", 0, replyErr, false),
		occAt(t, replySrc, 10, "Error", 1, failErr, false),
	}
	return &Index{Occurrences: occ, Symbols: map[string]SymbolInfo{}}
}

func TestMergeMatchesReferencesByColumn(t *testing.T) {
	f := newLibFixture(t)
	f.write(t, "service/reply.go", replySrc)
	gitAddCommit(t, f.cfg.Path)
	f.ensure(t)
	f.merge(t, replyIndex(t))
	src := f.sym(t, "go-lib:go:github.com/fixture/lib/service:reply")
	for _, c := range []struct{ kind, refKind, dst string }{
		{"calls", "call", "go-lib:go:github.com/fixture/lib/service:failure.Error"},
		{"references", "field", "go-lib:go:github.com/fixture/lib/service:errorReply.Error"},
	} {
		dst := f.sym(t, c.dst)
		es, err := f.s.EdgesFrom(src.ID, c.kind)
		if err != nil {
			t.Fatal(err)
		}
		if !hasPrecise(es, dst.ID, c.refKind) {
			t.Fatalf("%s: no precise %s edge to %s in %+v", c.kind, c.refKind, c.dst, es)
		}
	}
}

// hasPrecise reports whether es holds a precise edge to dst from a
// reference of kind refKind.
func hasPrecise(es []store.Edge, dst int64, refKind string) bool {
	for _, e := range es {
		if e.Dst == dst && e.Confidence == store.ConfidencePrecise && e.RefKind == refKind {
			return true
		}
	}
	return false
}

func gitAddCommit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.email=t@x", "-c", "user.name=t", "commit", "-qm", "add"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
}
