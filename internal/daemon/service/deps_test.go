package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

// depStates returns "name version state" of the installed dependencies
// the daemon recorded for workspace fx.
func (h *harness) depStates() string {
	h.t.Helper()
	s, err := store.Open(h.cfg.DBPath("fx"))
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	uses, err := s.DepUses(0)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, u := range uses {
		if u.Version != "" && u.Manifest == "package.json" {
			out = append(out, u.Name+" "+u.Version+" "+u.State)
		}
	}
	return strings.Join(out, ", ")
}

// The daemon indexes the dependencies the code imports at catch-up,
// again when a lock file changes, and a package that was unused once a
// source file starts importing it (deps spec section 9).
func TestDaemonIndexesDependenciesAndFollowsLockFiles(t *testing.T) {
	h := newHarness(t, "", "deps-app")
	h.start(h.options())
	h.eventually("the dependencies of the catch-up", waitLimit, func() bool {
		return h.depStates() == "@acme/sdk 1.2.0 indexed, @types/node 22.1.0 indexed, dup 2.0.0 indexed, "+
			"linked-lib 0.3.0 indexed, lint-tool 9.4.0 unused, plain-js 2.0.1 unused"
	})
	h.write("deps-app", "src/plain.ts", "import { shout } from \"plain-js\";\n\nexport const loud = shout(\"x\");\n")
	h.eventually("the package a new source file imports", waitLimit, func() bool {
		return strings.Contains(h.depStates(), "plain-js 2.0.1 indexed")
	})
	h.write("deps-app", "node_modules/dup/package.json", `{"name": "dup", "version": "2.5.0", "typings": "dup.d.ts"}`)
	h.write("deps-app", "pnpm-lock.yaml", "lockfileVersion: '9.0'\n# dup 2.5.0\n")
	h.eventually("the new version after the lock file changed", waitLimit, func() bool {
		return strings.Contains(h.depStates(), "dup 2.5.0 indexed")
	})
	if !strings.Contains(h.log.String(), "deps: indexed") {
		t.Errorf("the daemon logs what it indexed:\n%s", h.log.String())
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(h.cfg.DepsDBPath()), "deps.lock")); err != nil {
		t.Fatalf("packages are written under deps.lock: %v", err)
	}
}
