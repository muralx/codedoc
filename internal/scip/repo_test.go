package scip

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

func TestRunRepoMergesEachLanguage(t *testing.T) {
	f := newLibFixture(t)
	fixture, err := filepath.Abs("testdata/go-lib.scip")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEDOC_SCIP_GO", stub(t, `cp '`+fixture+`' "$2"`))
	out, err := Runner{}.RunRepo(context.Background(), f.s, f.repo(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Lang != "go" || out[0].Failed || out[0].Missing != nil ||
		!strings.HasPrefix(out[0].Text, "ran precise:+") {
		t.Fatalf("%+v", out)
	}
	if f.repo(t).SCIPCommit == "" {
		t.Fatal("scip commit not recorded")
	}
}

func TestRunRepoReportsMissingIndexers(t *testing.T) {
	f := newLibFixture(t)
	t.Setenv("CODEDOC_SCIP_GO", filepath.Join(t.TempDir(), "no-scip-go"))
	out, err := Runner{}.RunRepo(context.Background(), f.s, f.repo(t))
	if err != nil || len(out) != 1 || out[0].Missing == nil || out[0].Text != "missing indexer "+out[0].Missing.Bin {
		t.Fatalf("%v %+v", err, out)
	}
	if rec, _, _ := LastRun(f.s, f.cfg.Path); rec.Failed || len(rec.Missing) != 1 || rec.Missing[0] != "no-scip-go" {
		t.Fatalf("%+v", rec)
	}
}

func TestRunRepoReportsFailedIndexers(t *testing.T) {
	f := newLibFixture(t)
	t.Setenv("CODEDOC_SCIP_GO", stub(t, "echo boom >&2; exit 1"))
	out, err := Runner{}.RunRepo(context.Background(), f.s, f.repo(t))
	if err != nil || len(out) != 1 || !out[0].Failed || !strings.HasPrefix(out[0].Text, "failed: scip: go indexer") {
		t.Fatalf("%v %+v", err, out)
	}
	if rec, ok, err := LastRun(f.s, f.cfg.Path); err != nil || !ok || !rec.Failed || rec.At.IsZero() {
		t.Fatalf("%+v %v %v", rec, ok, err)
	}
}

func TestRunRepoWithoutIndexableLanguages(t *testing.T) {
	f := newLibFixture(t)
	out, err := Runner{}.RunRepo(context.Background(), f.s, store.Repo{ID: -1, Path: t.TempDir()})
	if err != nil || len(out) != 0 {
		t.Fatalf("%v %+v", err, out)
	}
}
