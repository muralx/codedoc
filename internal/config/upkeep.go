package config

import (
	"errors"
	"time"
)

// Upkeep makes the daemon document, outside any session, the symbols a
// branch changed whose doc is stale or missing (efficiency spec section
// 7). After Load every field holds its effective value: an absent or
// zero quiet, max_items, first_run_commits, max_cost_usd,
// daily_cost_usd or model takes its default.
type Upkeep struct {
	// Enabled opts the workspace in; the default is off.
	Enabled bool `yaml:"enabled"`
	// Quiet is how long a repo must see no file event before a run.
	Quiet Duration `yaml:"quiet"`
	// MaxItems caps the symbols one run documents.
	MaxItems int `yaml:"max_items"`
	// Commit makes a run commit its docs as one commit; unset means
	// true (see Commits).
	Commit *bool `yaml:"commit"`
	// FirstRunCommits is how many commits the first run on a branch
	// looks back.
	FirstRunCommits int `yaml:"first_run_commits"`
	// MaxCostUSD caps what one run may spend.
	MaxCostUSD float64 `yaml:"max_cost_usd"`
	// DailyCostUSD caps what the workspace's runs may spend per day;
	// once reached, upkeep stops until the next day.
	DailyCostUSD float64 `yaml:"daily_cost_usd"`
	// Model is the model the claude runs use (--model).
	Model string `yaml:"model"`
}

// The defaults of the upkeep section.
const (
	DefaultUpkeepQuiet           = 5 * time.Minute
	DefaultUpkeepMaxItems        = 20
	DefaultUpkeepFirstRunCommits = 20
	DefaultUpkeepMaxCostUSD      = 1.00
	DefaultUpkeepDailyCostUSD    = 5.00
)

// Commits reports whether a run commits what it wrote: true unless
// commit is set to false.
func (u Upkeep) Commits() bool { return u.Commit == nil || *u.Commit }

// validate rejects negative numbers and applies the defaults.
func (u *Upkeep) validate() error {
	switch {
	case u.MaxItems < 0:
		return errors.New("upkeep.max_items must be >= 0")
	case u.FirstRunCommits < 0:
		return errors.New("upkeep.first_run_commits must be >= 0")
	case u.MaxCostUSD < 0:
		return errors.New("upkeep.max_cost_usd must be >= 0")
	case u.DailyCostUSD < 0:
		return errors.New("upkeep.daily_cost_usd must be >= 0")
	}
	if u.Quiet == 0 {
		u.Quiet = Duration(DefaultUpkeepQuiet)
	}
	if u.MaxItems == 0 {
		u.MaxItems = DefaultUpkeepMaxItems
	}
	if u.FirstRunCommits == 0 {
		u.FirstRunCommits = DefaultUpkeepFirstRunCommits
	}
	if u.MaxCostUSD == 0 {
		u.MaxCostUSD = DefaultUpkeepMaxCostUSD
	}
	if u.DailyCostUSD == 0 {
		u.DailyCostUSD = DefaultUpkeepDailyCostUSD
	}
	if u.Model == "" {
		u.Model = DefaultDocModel
	}
	return nil
}
