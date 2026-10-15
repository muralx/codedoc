package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrOutsideRepos reports a directory outside every configured repo.
var ErrOutsideRepos = errors.New("not inside any configured repository")

// Resolve finds the workspace and repository containing dir. The longest
// matching repo path wins, so nested checkouts resolve to the inner one.
func (c *Config) Resolve(dir string) (*Workspace, *Repo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, err
	}
	var bestWS *Workspace
	var bestRepo *Repo
	for _, ws := range c.Workspaces {
		for _, r := range ws.Repos {
			if !within(abs, r.Path) {
				continue
			}
			if bestRepo == nil || len(r.Path) > len(bestRepo.Path) {
				bestWS, bestRepo = ws, r
			}
		}
	}
	if bestRepo == nil {
		return nil, nil, fmt.Errorf("%s is %w", abs, ErrOutsideRepos)
	}
	return bestWS, bestRepo, nil
}

func within(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// WorkspaceFor returns the workspace a directory belongs to: the one of
// the repo containing it (see Resolve), else, for a directory inside the
// configuration file's directory, the only workspace with repos below
// it. Several or no such workspaces keep Resolve's error.
func (c *Config) WorkspaceFor(dir string) (*Workspace, error) {
	ws, _, err := c.Resolve(dir)
	if err == nil {
		return ws, nil
	}
	abs, absErr := filepath.Abs(dir)
	if absErr != nil || c.dir == "" || !within(abs, c.dir) {
		return nil, err
	}
	var found *Workspace
	for _, name := range c.Names() {
		w := c.Workspaces[name]
		for _, r := range w.Repos {
			if within(r.Path, abs) && w != found {
				if found != nil {
					return nil, fmt.Errorf("%w; repos of workspaces %s and %s lie below it, use --workspace",
						err, found.Name, w.Name)
				}
				found = w
			}
		}
	}
	if found == nil {
		return nil, err
	}
	return found, nil
}
