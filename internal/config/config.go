// Package config loads the codedoc configuration file and resolves which
// workspace and repository a directory belongs to.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/muralx/codedoc/internal/exclude"
)

// Config is the parsed configuration file.
type Config struct {
	Workspaces map[string]*Workspace `yaml:"workspaces"`
	Embed      Embed                 `yaml:"embed"`
	dataDir    string
	dir        string // absolute directory of the configuration file
}

// Workspace is a named set of repositories that may reference each other.
type Workspace struct {
	Name    string   `yaml:"-"`
	Repos   []*Repo  `yaml:"repos"`
	Exclude []string `yaml:"exclude"`
	SCIP    SCIP     `yaml:"scip"`
	Autodoc Autodoc  `yaml:"autodoc"`
	Upkeep  Upkeep   `yaml:"upkeep"`
	Deps    Deps     `yaml:"deps"`
}

// Repo is one working tree inside a workspace.
type Repo struct {
	Path string `yaml:"path"`
	Name string `yaml:"name"`
	// Languages, when set, limits the files indexed to these languages
	// and config formats (see FileLanguages); files of no language, such
	// as manifests, are indexed still.
	Languages []string `yaml:"languages"`
}

// DefaultPath returns the configuration file location.
func DefaultPath() string {
	if p := os.Getenv("CODEDOC_CONFIG"); p != "" {
		return p
	}
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "codedoc", "config.yaml")
}

// Load reads and validates a configuration file. Relative repo paths are
// resolved against the directory that contains the configuration file.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := decodeStrict(raw, &cfg); err != nil {
		return nil, parseError(path, err)
	}
	absCfg, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	cfg.dataDir = dataDir()
	cfg.dir = filepath.Dir(absCfg)
	return &cfg, cfg.normalize(cfg.dir)
}

// decodeStrict decodes a configuration file, rejecting keys no field
// takes; an empty file is an empty configuration.
func decodeStrict(raw []byte, cfg *Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// unknownField matches yaml's report of a key no field takes.
var unknownField = regexp.MustCompile(`line (\d+): field (\S+) not found in type config\.(\w+)`)

// parseError reports a decoding error of the file at path: each key no
// field takes as "<path>:<line>: unknown key "<key>" in <section>",
// anything else as "parse <path>: <error>".
func parseError(path string, err error) error {
	all := unknownField.FindAllStringSubmatch(err.Error(), -1)
	if len(all) == 0 {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	var errs []error
	for _, m := range all {
		errs = append(errs, fmt.Errorf("%s:%s: unknown key %q in %s", path, m[1], m[2], sectionName(m[3])))
	}
	return errors.Join(errs...)
}

// sectionName names the configuration section a Go type decodes.
func sectionName(typ string) string {
	switch typ {
	case "Config":
		return "the top level"
	case "SCIP":
		return "scip"
	}
	return strings.ToLower(typ)
}

// dataDir is where workspace databases live: CODEDOC_DATA, else
// ${XDG_DATA_HOME:-~/.local/share}/codedoc; a relative XDG_DATA_HOME is
// ignored, as the XDG spec requires.
func dataDir() string {
	if p := os.Getenv("CODEDOC_DATA"); p != "" {
		return p
	}
	if x := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "codedoc")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "codedoc")
}

// DBPath returns the SQLite file for a workspace.
func (c *Config) DBPath(workspace string) string {
	return filepath.Join(c.dataDir, workspace+".db")
}

// DepsWorkspace is the workspace name the shared dependency database
// takes; no workspace may use it, since both would be <data dir>/deps.db.
const DepsWorkspace = "deps"

// DepsDBPath returns the SQLite file every workspace shares for the
// packages it depends on (deps spec section 2).
func (c *Config) DepsDBPath() string {
	return filepath.Join(c.dataDir, DepsWorkspace+".db")
}

func (c *Config) normalize(baseDir string) error {
	seen := map[string]string{}
	for name, ws := range c.Workspaces {
		if ws == nil {
			return fmt.Errorf("workspace %s: empty definition", name)
		}
		if name == DepsWorkspace {
			return fmt.Errorf("workspace %s: the name is reserved for the dependency database", name)
		}
		ws.Name = name
		if err := normalizeRepos(ws, baseDir, seen); err != nil {
			return err
		}
		if err := uniqueNames(name, ws.Repos); err != nil {
			return err
		}
		if err := exclude.ValidateGlobs(ws.Exclude); err != nil {
			return fmt.Errorf("workspace %s: %w", name, err)
		}
		if err := validateSections(ws); err != nil {
			return err
		}
	}
	c.normalizeEmbed()
	return nil
}

// normalizeRepos normalizes every repo of a workspace and records its path
// in seen, rejecting empty entries and paths listed twice anywhere.
func normalizeRepos(ws *Workspace, baseDir string, seen map[string]string) error {
	for i, r := range ws.Repos {
		if r == nil || r.Path == "" {
			return fmt.Errorf("workspace %s: repo %d has no path", ws.Name, i+1)
		}
		normalizeRepo(r, baseDir)
		if err := validateLanguages(r); err != nil {
			return fmt.Errorf("workspace %s: %w", ws.Name, err)
		}
		if other, dup := seen[r.Path]; dup {
			if other == ws.Name {
				return fmt.Errorf("workspace %s: repo %s listed twice", ws.Name, r.Path)
			}
			return fmt.Errorf("repo %s is in workspaces %s and %s", r.Path, other, ws.Name)
		}
		seen[r.Path] = ws.Name
	}
	return nil
}

// normalizeRepo makes the repo path absolute and clean, and defaults the
// name to the last path element.
func normalizeRepo(r *Repo, baseDir string) {
	p := expandHome(r.Path)
	if !filepath.IsAbs(p) {
		p = filepath.Join(baseDir, p)
	}
	r.Path = filepath.Clean(p)
	if r.Name == "" {
		r.Name = filepath.Base(r.Path)
	}
}

func uniqueNames(ws string, repos []*Repo) error {
	names := map[string]bool{}
	for _, r := range repos {
		if names[r.Name] {
			return fmt.Errorf("workspace %s: two repos named %q; set name explicitly", ws, r.Name)
		}
		names[r.Name] = true
	}
	return nil
}

func expandHome(p string) string {
	if len(p) > 1 && p[:2] == "~/" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

// Names returns workspace names in sorted order.
func (c *Config) Names() []string {
	out := make([]string, 0, len(c.Workspaces))
	for n := range c.Workspaces {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// RepoNames returns the names of the workspace's repos in config order.
func (w *Workspace) RepoNames() []string {
	out := make([]string, len(w.Repos))
	for i, r := range w.Repos {
		out[i] = r.Name
	}
	return out
}
