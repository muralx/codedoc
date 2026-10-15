package testutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// isolatedDirs are the variables that place codedoc's default
// directories: configuration, databases, the daemon log and socket, the
// write journal and the per-file locks, and the Go module cache the
// dependency index reads (CODEDOC_GOMODCACHE, which wins over the go
// command's own GOMODCACHE, left alone). IsolateEnv points each at its
// own directory under one temporary root.
var isolatedDirs = []string{
	"HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME",
	"XDG_RUNTIME_DIR", "CODEDOC_DATA", "CODEDOC_CONFIG", "CODEDOC_GOMODCACHE",
}

// toolHomes are caches of build tools (Go, Gradle, Cargo, coursier) that
// default to a directory under HOME. IsolateEnv keeps them where HOME had
// them, so moving HOME costs no rebuild or download; codedoc writes none
// of them.
var toolHomes = map[string]string{
	"XDG_CACHE_HOME":   ".cache",
	"GOPATH":           "go",
	"GRADLE_USER_HOME": ".gradle",
	"CARGO_HOME":       ".cargo",
	"RUSTUP_HOME":      ".rustup",
}

// IsolateEnv points HOME, the XDG base directories and codedoc's own
// CODEDOC_DATA, CODEDOC_CONFIG and CODEDOC_GOMODCACHE at fresh
// directories under a new temporary root, so no test writes to the
// user's real state, data or configuration, reads the user's Go module
// cache, or meets a daemon the user runs. It also makes UTC the local
// time zone of the process and of the commands it starts. It returns the root;
// the caller removes it. The root is short (os.TempDir), so a Unix socket
// under XDG_RUNTIME_DIR fits the path limit.
func IsolateEnv() (string, error) {
	root, err := os.MkdirTemp("", "cdenv")
	if err != nil {
		return "", err
	}
	// Answers that print local times (codedoc ls) are the same everywhere.
	time.Local = time.UTC
	if err := os.Setenv("TZ", "UTC"); err != nil {
		return root, err
	}
	if home, err := os.UserHomeDir(); err == nil {
		for name, rel := range toolHomes {
			if os.Getenv(name) == "" {
				if err := os.Setenv(name, filepath.Join(home, rel)); err != nil {
					return root, err
				}
			}
		}
	}
	for _, name := range isolatedDirs {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			return root, err
		}
		if name == "CODEDOC_CONFIG" {
			dir = filepath.Join(dir, "config.yaml")
		}
		if err := os.Setenv(name, dir); err != nil {
			return root, err
		}
	}
	return root, nil
}

// MainIsolated runs a package's tests under IsolateEnv and returns their
// exit code; call it from TestMain as os.Exit(testutil.MainIsolated(m)).
func MainIsolated(m *testing.M) int {
	root, err := IsolateEnv()
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	return m.Run()
}
