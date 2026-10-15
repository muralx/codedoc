package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

// SCIP schedules precise indexing for repos with a usable toolchain.
type SCIP struct {
	// Enabled names the workspace repos that SCIP indexers run on.
	Enabled []string `yaml:"enabled"`
	// Every is the interval between runs; zero disables scheduling.
	Every Duration `yaml:"every"`
}

// Autodoc schedules unattended documentation of gaps (spec 11.2).
type Autodoc struct {
	// Every is the interval between runs; zero disables scheduling.
	Every Duration `yaml:"every"`
	// At is the time of day (HH:MM) daily or longer runs start.
	At string `yaml:"at"`
	// MaxItems caps the gaps documented per run; zero (or unset) means
	// DefaultAutodocMaxItems.
	MaxItems int `yaml:"max_items"`
	// Commit makes a run commit the documentation it writes.
	Commit bool `yaml:"commit"`
	// GroupSize caps the keys one claude run documents; zero means 5.
	GroupSize int `yaml:"group_size"`
	// MaxCostUSD caps what one claude run may spend; zero means 1.00.
	MaxCostUSD float64 `yaml:"max_cost_usd"`
	// Model is the model the claude runs use (--model); empty means
	// DefaultDocModel.
	Model string `yaml:"model"`
}

// DefaultDocModel is the model autodoc and upkeep run claude with when
// the config names none: writing doc comments does not need the
// session's own, larger model.
const DefaultDocModel = "sonnet"

// Deps selects the third-party packages indexed for a workspace (deps
// spec section 10).
type Deps struct {
	// Enabled turns the dependency index on or off; unset means on.
	Enabled *bool `yaml:"enabled"`
	// Include holds package names and doublestar patterns on the name. A
	// declared package it matches is indexed although no source file
	// imports it; a plain name no manifest declares (a transitive
	// package) is resolved like a declared one and indexed.
	Include []string `yaml:"include"`
	// Exclude holds doublestar patterns on the package name; a matching
	// package is not indexed.
	Exclude []string `yaml:"exclude"`
}

// On reports whether the dependency index is enabled.
func (d Deps) On() bool { return d.Enabled == nil || *d.Enabled }

// Excluded reports whether a package name matches an exclude pattern.
func (d Deps) Excluded(name string) bool { return matchesAny(d.Exclude, name) }

// Included reports whether a package name is an entry of deps.include
// or matches one: the package is indexed whether or not the code
// imports it.
func (d Deps) Included(name string) bool { return matchesAny(d.Include, name) }

// IncludeNames returns the entries of deps.include that are plain
// package names, not patterns: the ones discovery looks up among the
// transitive packages.
func (d Deps) IncludeNames() []string {
	var out []string
	for _, n := range d.Include {
		if !strings.ContainsAny(n, "*?[{") {
			out = append(out, n)
		}
	}
	return out
}

// MatchPackage reports whether a package name is the doublestar pattern
// or matches it.
func MatchPackage(pattern, name string) bool {
	ok, _ := doublestar.Match(pattern, name)
	return ok
}

// matchesAny reports whether name matches one of the patterns.
func matchesAny(patterns []string, name string) bool {
	for _, g := range patterns {
		if MatchPackage(g, name) {
			return true
		}
	}
	return false
}

// validate rejects an empty include name and a malformed include or
// exclude pattern.
func (d Deps) validate() error {
	for _, n := range d.Include {
		if strings.TrimSpace(n) == "" {
			return errors.New("deps.include: empty package name")
		}
		if !doublestar.ValidatePattern(n) {
			return fmt.Errorf("deps.include: invalid pattern %q", n)
		}
	}
	for _, g := range d.Exclude {
		if !doublestar.ValidatePattern(g) {
			return fmt.Errorf("deps.exclude: invalid pattern %q", g)
		}
	}
	return nil
}

// DefaultAutodocMaxItems is the gap cap of an autodoc run when neither
// --max-items nor autodoc.max_items sets one.
const DefaultAutodocMaxItems = 40

// Embed configures the embedding model used for document search.
type Embed struct {
	// ModelDir holds nomic-embed-text-v1.5.onnx and tokenizer.json; empty
	// means no embeddings. CODEDOC_MODEL_DIR overrides it.
	ModelDir string `yaml:"model_dir"`
}

// Duration is a time.Duration that also accepts whole days (2d) and weeks
// (1w) when read from YAML.
type Duration time.Duration

// D returns the duration as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// String formats the duration like time.Duration.
func (d Duration) String() string { return time.Duration(d).String() }

var dayUnits = map[byte]time.Duration{'d': 24 * time.Hour, 'w': 7 * 24 * time.Hour}

// ParseDuration parses a non-negative duration: anything time.ParseDuration
// accepts, or a whole number followed by d (days) or w (weeks).
func ParseDuration(s string) (Duration, error) {
	if unit, ok := dayUnits[lastByte(s)]; ok {
		n, err := strconv.ParseUint(s[:len(s)-1], 10, 32)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return Duration(time.Duration(n) * unit), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return Duration(d), nil
}

func lastByte(s string) byte {
	if s == "" {
		return 0
	}
	return s[len(s)-1]
}

// UnmarshalYAML reads a duration from a scalar such as 6h or 2d.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: duration must be a scalar like 6h or 2d", n.Line)
	}
	v, err := ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	*d = v
	return nil
}

var clock = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// validateSections checks the scip, autodoc, upkeep and deps sections of a
// workspace whose repos are already normalized, and defaults
// autodoc.max_items and the upkeep section.
func validateSections(ws *Workspace) error {
	names := ws.RepoNames()
	for _, n := range ws.SCIP.Enabled {
		if !slices.Contains(names, n) {
			return fmt.Errorf("workspace %s: scip.enabled: no repo named %q", ws.Name, n)
		}
	}
	if ws.Autodoc.At != "" && !clock.MatchString(ws.Autodoc.At) {
		return fmt.Errorf("workspace %s: autodoc.at %q is not HH:MM", ws.Name, ws.Autodoc.At)
	}
	if ws.Autodoc.MaxItems < 0 {
		return fmt.Errorf("workspace %s: autodoc.max_items must be a positive number", ws.Name)
	}
	if ws.Autodoc.MaxItems == 0 {
		ws.Autodoc.MaxItems = DefaultAutodocMaxItems
	}
	if ws.Autodoc.GroupSize < 0 {
		return fmt.Errorf("workspace %s: autodoc.group_size must be >= 0", ws.Name)
	}
	if ws.Autodoc.MaxCostUSD < 0 {
		return fmt.Errorf("workspace %s: autodoc.max_cost_usd must be >= 0", ws.Name)
	}
	if ws.Autodoc.Model == "" {
		ws.Autodoc.Model = DefaultDocModel
	}
	if err := ws.Upkeep.validate(); err != nil {
		return fmt.Errorf("workspace %s: %w", ws.Name, err)
	}
	if err := ws.Deps.validate(); err != nil {
		return fmt.Errorf("workspace %s: %w", ws.Name, err)
	}
	return nil
}

// normalizeEmbed applies CODEDOC_MODEL_DIR and resolves a relative model
// directory against the configuration file's directory.
func (c *Config) normalizeEmbed() {
	if p := os.Getenv("CODEDOC_MODEL_DIR"); p != "" {
		c.Embed.ModelDir = p
		return
	}
	p := strings.TrimSpace(c.Embed.ModelDir)
	if p == "" {
		return
	}
	p = expandHome(p)
	if !filepath.IsAbs(p) {
		p = filepath.Join(c.dir, p)
	}
	c.Embed.ModelDir = filepath.Clean(p)
}
