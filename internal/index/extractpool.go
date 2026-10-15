package index

import (
	"runtime"
	gosync "sync"

	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/store"
)

// extractAhead extracts the files of write in chunks of batchSize, in
// order, on a goroutine of its own: the returned channel yields each
// chunk's extractions (see preExtract) and is closed after the last one.
// It holds at most one chunk beyond the one being received, so
// extraction runs one chunk ahead of the writes. Closing stop makes it
// give up before the next chunk; drain waits for it to finish.
func (sy *syncer) extractAhead(write []Discovered, existing map[string]store.File,
	stop <-chan struct{}) <-chan map[string]preExtracted {
	out := make(chan map[string]preExtracted, 1)
	go func() {
		defer close(out)
		for lo := 0; lo < len(write); lo += batchSize {
			select {
			case <-stop:
				return
			default:
			}
			pre := sy.preExtract(write[lo:min(lo+batchSize, len(write))], existing)
			select {
			case out <- pre:
			case <-stop:
				return
			}
		}
	}()
	return out
}

// drain stops an extractAhead goroutine and waits until it is done, so
// no extraction outlives the sync that started it.
func drain(stop chan struct{}, ahead <-chan map[string]preExtracted) {
	close(stop)
	for range ahead {
	}
}

// unchanged reports whether a file to write keeps its stored content,
// language and module identity: its write only refreshes metadata.
func (sy *syncer) unchanged(d Discovered, existing map[string]store.File) bool {
	old, known := existing[d.Path]
	return known && old.Hash == d.Hash && old.Lang == d.Lang && !sy.force[d.Path]
}

// preExtracted is the extraction of one file done ahead of its write.
type preExtracted struct {
	ex  extracted
	err error
}

// preExtract extracts in parallel the files of chunk that write will
// re-extract (extraction is pure), so the write transaction only stores
// results. For new files holding documented symbols it also reads git
// blame, which seeds their doc states (see docStates).
func (sy *syncer) preExtract(chunk []Discovered, existing map[string]store.File) map[string]preExtracted {
	type job struct {
		d   Discovered
		out *preExtracted
	}
	out := map[string]preExtracted{}
	var jobs []job
	results := make([]preExtracted, len(chunk))
	for i, d := range chunk {
		if sy.unchanged(d, existing) {
			continue
		}
		jobs = append(jobs, job{d, &results[i]})
	}
	work := make(chan job)
	var wg gosync.WaitGroup
	for w := 0; w < min(runtime.GOMAXPROCS(0), len(jobs)); w++ {
		wg.Go(func() {
			for j := range work {
				mod, _, _ := sy.mods.moduleOf(parse.Family(j.d.Lang), j.d.Path)
				j.out.ex, j.out.err = extractFile(sy.root, sy.name, j.d, mod)
				old, known := existing[j.d.Path]
				if j.out.err == nil && needsSeed(j.out.ex, old, known) {
					j.out.ex.blame, j.out.ex.blamed = sy.blame(j.d.Path), true
				}
			}
		})
	}
	for _, j := range jobs {
		work <- j
	}
	close(work)
	wg.Wait()
	for _, j := range jobs {
		out[j.d.Path] = *j.out
	}
	return out
}
