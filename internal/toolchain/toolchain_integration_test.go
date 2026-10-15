//go:build integration

package toolchain

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/embed"
	ort "github.com/yalue/onnxruntime_go"
)

// TestToolsRun fails when a tool the integration tests shell out to is
// missing from PATH or does not start.
func TestToolsRun(t *testing.T) {
	tools := map[string][]string{
		"git":             {"--version"},
		"scip-go":         {"--version"},
		"scip-typescript": {"--version"},
		"scip-python":     {"--version"},
	}
	for name, args := range tools {
		t.Run(name, func(t *testing.T) {
			path, err := exec.LookPath(name)
			if err != nil {
				t.Fatalf("%s not on PATH: %v", name, err)
			}
			out, err := exec.Command(path, args...).CombinedOutput()
			if err != nil {
				t.Fatalf("%s %v: %v\n%s", name, args, err, out)
			}
			t.Logf("%s: %s", name, firstLine(out))
		})
	}
}

// TestONNXRuntimeLoads fails when the ONNX Runtime shared library named by
// embed.LibraryPath cannot be loaded.
func TestONNXRuntimeLoads(t *testing.T) {
	if ort.IsInitialized() {
		return
	}
	ort.SetSharedLibraryPath(embed.LibraryPath())
	if err := ort.InitializeEnvironment(); err != nil {
		t.Fatalf("load %s: %v", embed.LibraryPath(), err)
	}
	if err := ort.DestroyEnvironment(); err != nil {
		t.Fatalf("destroy environment: %v", err)
	}
}

// TestModelEmbeds embeds one sentence with the real model when
// CODEDOC_MODEL_DIR is set, and fails when the model there is unusable.
func TestModelEmbeds(t *testing.T) {
	dir := os.Getenv("CODEDOC_MODEL_DIR")
	if dir == "" {
		t.Skip("CODEDOC_MODEL_DIR not set")
	}
	e, err := embed.NewEmbedder(filepath.Join(dir, embed.ModelFile), filepath.Join(dir, embed.TokenizerFile))
	if err != nil {
		t.Fatalf("new embedder: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })
	v, err := e.Embed("search_document: codedoc answers structural questions about code")
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(v) != 768 {
		t.Fatalf("dimensions = %d, want 768", len(v))
	}
}

func firstLine(b []byte) string {
	for i, c := range b {
		if c == '\n' {
			return string(b[:i])
		}
	}
	return string(b)
}
