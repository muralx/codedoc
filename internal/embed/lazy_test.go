package embed

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLazyIsColdBeforeUse(t *testing.T) {
	l := NewLazy(8, func() (Embedder, error) { t.Fatal("loaded"); return nil, nil }, nil)
	if l.Loaded() || Warm(l) || l.Dimensions() != 8 || l.Close() != nil {
		t.Fatal("loaded before use")
	}
}

func TestLazyLoadsOnFirstUse(t *testing.T) {
	calls := 0
	l := NewLazy(8, func() (Embedder, error) { calls++; return NewFake(8), nil }, nil)
	v, err := l.Embed("alpha beta")
	vs, berr := l.EmbedBatch([]string{"a", "b"})
	if err != nil || berr != nil || len(v) != 8 || len(vs) != 2 {
		t.Fatalf("%v %v %v %v", v, err, vs, berr)
	}
	if !l.Loaded() || !Warm(l) || calls != 1 || l.Close() != nil {
		t.Fatalf("loaded=%v calls=%d", l.Loaded(), calls)
	}
}

func TestLazyReportsFailedLoadOnce(t *testing.T) {
	var warn bytes.Buffer
	l := NewLazy(8, func() (Embedder, error) { return nil, errors.New("no runtime") }, &warn)
	for range 2 {
		if _, err := l.Embed("x"); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	}
	if _, err := l.EmbedBatch([]string{"x"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if strings.Count(warn.String(), "no runtime") != 1 || l.Loaded() {
		t.Fatalf("%q", warn.String())
	}
}

func TestConfigured(t *testing.T) {
	t.Setenv(EmbedderEnv, "")
	if Configured("", nil) != nil {
		t.Fatal("embedder without a model dir")
	}
	t.Setenv(EmbedderEnv, "fake")
	if e := Configured("", nil); e == nil || e.Dimensions() != FakeDims || !Warm(e) {
		t.Fatalf("%v", e)
	}
	t.Setenv(EmbedderEnv, "off")
	if Configured("/models", nil) != nil {
		t.Fatal("off still configured")
	}
	t.Setenv(EmbedderEnv, "")
	var warn bytes.Buffer
	e := Configured(t.TempDir(), &warn)
	if e == nil || e.Dimensions() != ModelDims {
		t.Fatalf("%v", e)
	}
	if _, err := e.Embed("x"); !errors.Is(err, ErrUnavailable) || !strings.Contains(warn.String(), ModelFile) {
		t.Fatalf("%v %q", err, warn.String())
	}
}

func TestConfiguredUnloadableModel(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{ModelFile, TokenizerFile} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(EmbedderEnv, "")
	if _, err := Configured(dir, nil).Embed("x"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
