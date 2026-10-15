//go:build integration

package scip

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestIntegrationScipGo runs the real scip-go on a copy of the Go fixture.
// It fails when scip-go is missing: the Docker image must provide it.
func TestIntegrationScipGo(t *testing.T) {
	if _, err := exec.LookPath("scip-go"); err != nil {
		t.Fatalf("scip-go not on PATH: %v", err)
	}
	root := filepath.Join(t.TempDir(), "go-lib")
	if err := os.CopyFS(root, os.DirFS("../../testdata/ws/go-lib")); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "go.scip")
	if err := (Runner{}).RunLang(context.Background(), root, "go", file); err != nil {
		t.Fatal(err)
	}
	ix, err := ParseFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if def, ok := findOcc(ix, "store/store.go", 26, memGet); !ok || !def.Definition {
		t.Fatalf("MemStore.Get definition missing: %+v", def)
	}
	refFromService := slices.ContainsFunc(ix.Occurrences, func(o Occurrence) bool {
		return strings.HasPrefix(o.Path, "service/") && o.Symbol == storeGet && o.Ref
	})
	if !refFromService {
		t.Fatal("no reference to Store.Get from the service package")
	}
	if !slices.Contains(ix.Symbols[memGet].Implements, storeGet) {
		t.Fatalf("MemStore.Get implements: %+v", ix.Symbols[memGet])
	}
}
