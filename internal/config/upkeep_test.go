package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/config"
)

const upkeepHead = "workspaces:\n  w:\n    repos:\n      - path: a\n"

func loadUpkeep(t *testing.T, section string) (config.Upkeep, error) {
	t.Helper()
	cfg, err := config.Load(writeConfig(t, t.TempDir(), upkeepHead+section))
	if err != nil {
		return config.Upkeep{}, err
	}
	return cfg.Workspaces["w"].Upkeep, nil
}

func TestUpkeepIsOffWithItsDefaults(t *testing.T) {
	for _, section := range []string{"", "    upkeep: {}\n", "    upkeep: {quiet: 0s, max_items: 0, max_cost_usd: 0}\n"} {
		u, err := loadUpkeep(t, section)
		if err != nil {
			t.Fatal(err)
		}
		if u.Enabled || u.Quiet.D() != 5*time.Minute || u.MaxItems != 20 || !u.Commits() || u.FirstRunCommits != 20 ||
			u.MaxCostUSD != 1 || u.DailyCostUSD != 5 || u.Model != "sonnet" {
			t.Fatalf("%q: %+v", section, u)
		}
	}
}

func TestUpkeepReadsEveryKey(t *testing.T) {
	u, err := loadUpkeep(t, `    upkeep:
      enabled: true
      quiet: 90s
      max_items: 7
      commit: false
      first_run_commits: 3
      max_cost_usd: 0.5
      daily_cost_usd: 2.25
`)
	if err != nil {
		t.Fatal(err)
	}
	if !u.Enabled || u.Quiet.D() != 90*time.Second || u.MaxItems != 7 || u.Commits() || u.FirstRunCommits != 3 ||
		u.MaxCostUSD != 0.5 || u.DailyCostUSD != 2.25 {
		t.Fatalf("%+v", u)
	}
}

func TestUpkeepRejectsBadValues(t *testing.T) {
	for section, want := range map[string]string{
		"    upkeep: {max_items: -1}\n":         "workspace w: upkeep.max_items must be >= 0",
		"    upkeep: {first_run_commits: -1}\n": "workspace w: upkeep.first_run_commits must be >= 0",
		"    upkeep: {max_cost_usd: -1}\n":      "workspace w: upkeep.max_cost_usd must be >= 0",
		"    upkeep: {daily_cost_usd: -0.5}\n":  "workspace w: upkeep.daily_cost_usd must be >= 0",
		"    upkeep: {quiet: soon}\n":           `invalid duration "soon"`,
		"    upkeep: {quite: 5m}\n":             `unknown key "quite" in upkeep`,
	} {
		if _, err := loadUpkeep(t, section); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", section, err, want)
		}
	}
}
