package index_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

// slowEmbedder blocks each batch until released, after reporting that
// it started.
type slowEmbedder struct {
	*embed.Fake
	started chan struct{}
	release chan struct{}
}

func (e *slowEmbedder) EmbedBatch(texts []string) ([][]float32, error) {
	e.started <- struct{}{}
	<-e.release
	return e.Fake.EmbedBatch(texts)
}

func TestEnsureEmbedsWithoutTheWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	first, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	rep := repoFixture(t, "go-lib")
	indexRepo(t, first, rep)
	slow := &slowEmbedder{Fake: embed.NewFake(16), started: make(chan struct{}), release: make(chan struct{})}
	appendFile(t, rep.Path, "store/null.go", "\n// Fresh is new.\nfunc Fresh() {}\n")
	done := make(chan error, 1)
	go func() {
		_, err := index.Ensure(first, rep, index.Options{Workspace: "fixture", MaxFiles: 200, Embedder: slow})
		done <- err
	}()
	<-slow.started
	second := ensureElsewhere(t, path, rep)
	close(slow.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got, err := second.SymbolsByName("Later", "", 0); err != nil || len(got) != 1 {
		t.Fatalf("second Ensure did not index Later: %+v %v", got, err)
	}
}

// ensureElsewhere changes a file and runs Ensure on a second store handle
// of the database at path, failing when it does not finish in time.
func ensureElsewhere(t *testing.T, path string, rep *config.Repo) *store.Store {
	t.Helper()
	second, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	appendFile(t, rep.Path, "store/store.go", "\n// Later is new.\nfunc Later() {}\n")
	ensured := make(chan error, 1)
	go func() {
		_, err := index.Ensure(second, rep, index.Options{Workspace: "fixture", MaxFiles: 200})
		ensured <- err
	}()
	select {
	case err := <-ensured:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("a second Ensure waited for the embedding pass")
	}
	return second
}
