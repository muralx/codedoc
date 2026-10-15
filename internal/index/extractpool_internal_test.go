package index

import (
	"fmt"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

// unchangedChunks returns n chunks of discovered files whose stored rows
// match, so preExtract has nothing to extract.
func unchangedChunks(n int) ([]Discovered, map[string]store.File) {
	write := make([]Discovered, n*batchSize)
	existing := make(map[string]store.File, len(write))
	for i := range write {
		write[i] = Discovered{Path: fmt.Sprintf("f%d.go", i), Lang: "go", Hash: "h"}
		existing[write[i].Path] = store.File{ID: int64(i + 1), Path: write[i].Path, Lang: "go", Hash: "h"}
	}
	return write, existing
}

func TestExtractAheadYieldsEveryChunkInOrder(t *testing.T) {
	write, existing := unchangedChunks(3)
	stop := make(chan struct{})
	ahead := (&syncer{}).extractAhead(write, existing, stop)
	got := 0
	for range ahead {
		got++
	}
	drain(stop, ahead)
	if got != 3 {
		t.Fatalf("chunks = %d, want 3", got)
	}
}

func TestDrainStopsExtractAheadEarly(t *testing.T) {
	write, existing := unchangedChunks(50)
	stop := make(chan struct{})
	ahead := (&syncer{}).extractAhead(write, existing, stop)
	<-ahead
	drain(stop, ahead)
	if _, open := <-ahead; open {
		t.Fatal("extractAhead still running after drain")
	}
}
