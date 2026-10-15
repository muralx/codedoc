package store_test

import (
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

// upkeepOf returns the upkeep state of a branch and the repo's last
// run; a branch without state is the zero value.
func upkeepOf(t *testing.T, s *store.Store, repoID int64, branch string) (store.UpkeepBranch, int64) {
	t.Helper()
	u, ok, err := s.UpkeepBranch(repoID, branch)
	if err != nil || (!ok && u != (store.UpkeepBranch{})) {
		t.Fatalf("%+v %v %v", u, ok, err)
	}
	last, err := s.UpkeepLastRun(repoID)
	if err != nil {
		t.Fatal(err)
	}
	return u, last
}

// setUpkeep records the upkeep state of a branch.
func setUpkeep(t *testing.T, s *store.Store, repoID int64, branch, commit string, at int64) {
	t.Helper()
	if err := s.SetUpkeepBranch(repoID, branch, commit, at); err != nil {
		t.Fatal(err)
	}
}

func TestUpkeepBranchStateIsKeptPerBranchAndSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertRepo(store.Repo{Workspace: "demo", Name: "a", Path: "/src/a"})
	if err != nil {
		t.Fatal(err)
	}
	if u, last := upkeepOf(t, s, id, "main"); u.Commit != "" || last != 0 {
		t.Fatalf("a branch never documented: %+v, last run %d", u, last)
	}
	setUpkeep(t, s, id, "main", "c1", 10)
	setUpkeep(t, s, id, "feature/x", "f1", 30)
	setUpkeep(t, s, id, "main", "c2", 20)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = store.Open(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if u, last := upkeepOf(t, s, id, "main"); u != (store.UpkeepBranch{Commit: "c2", RanAt: 20}) || last != 30 {
		t.Fatalf("main %+v, last run %d", u, last)
	}
	if u, _ := upkeepOf(t, s, id, "feature/x"); u.Commit != "f1" {
		t.Fatalf("feature %+v", u)
	}
	if err := s.Batch(func(b *store.Batch) error { return b.DeleteRepo(id) }); err != nil {
		t.Fatal(err)
	}
	if u, _ := upkeepOf(t, s, id, "main"); u.Commit != "" {
		t.Fatal("the state outlived its repo")
	}
}

func TestUpkeepSpendAddsUpPerDayAndForgetsEarlierDays(t *testing.T) {
	s := openTemp(t)
	for _, usd := range []float64{0.25, 0.5} {
		if err := s.AddUpkeepSpend("2026-10-09", usd); err != nil {
			t.Fatal(err)
		}
	}
	if usd, err := s.UpkeepSpent("2026-10-09"); usd != 0.75 || err != nil {
		t.Fatalf("spent %v %v", usd, err)
	}
	if usd, err := s.UpkeepSpent("2026-10-10"); usd != 0 || err != nil {
		t.Fatalf("the next day starts at %v %v", usd, err)
	}
	if err := s.AddUpkeepSpend("2026-10-10", 1); err != nil {
		t.Fatal(err)
	}
	if usd, _ := s.UpkeepSpent("2026-10-09"); usd != 0 {
		t.Fatalf("yesterday kept: %v", usd)
	}
}

func TestUpkeepOnAClosedStoreFails(t *testing.T) {
	s := openTemp(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpkeepSpent("2026-10-10"); err == nil {
		t.Fatal("a closed store answered")
	}
	if err := s.AddUpkeepSpend("2026-10-10", 1); err == nil {
		t.Fatal("a closed store took a write")
	}
	if _, _, err := s.UpkeepBranch(1, "main"); err == nil {
		t.Fatal("a closed store answered")
	}
}
