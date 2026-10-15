package deps

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/muralx/codedoc/internal/store"
)

// candidates returns one use per package version the repos use that may
// need work: the pending ones, the indexed mutable sources (to check
// their files did not change), the indexed ambient ones (to check
// deps.db holds the modules wanted, see DB.covers), and with full also the
// failed ones (to retry) and the other indexed ones (to check they are
// still in deps.db with the rows of this extraction version).
func candidates(s *store.Store, ids map[string]int64, full bool) ([]store.DepUse, error) {
	byID := map[string]store.DepUse{}
	for _, repoID := range ids {
		uses, err := s.DepUses(repoID)
		if err != nil {
			return nil, err
		}
		for _, u := range uses {
			indexed := u.State == store.DepIndexed
			if u.State == store.DepPending || indexed && (u.Mutable() || ambient(u)) ||
				full && (u.State == store.DepFailed || indexed) {
				byID[u.PackageID()] = u
			}
		}
	}
	out := make([]store.DepUse, 0, len(byID))
	for _, id := range sortedKeys(byID) {
		out = append(out, byID[id])
	}
	return out, nil
}

// outcome is what became of one package version in a Sync.
type outcome struct {
	use   store.DepUse
	state string
	err   string
	// wrote is set when this Sync wrote the package to deps.db.
	wrote bool
}

// indexPackages makes sure every package version of todo is in deps.db,
// extracting the missing ones on a few goroutines and writing them one
// at a time. A package not started when the deadline passes or ctx ends
// has no outcome and keeps its state: a pending one still waits, and an
// indexed one that was only to be checked still answers. only names the
// modules wanted of the packages indexed partially.
func indexPackages(ctx context.Context, todo []store.DepUse, opts Options, only map[string][]string) ([]outcome, error) {
	db, err := Open(opts.DB)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	lim := opts.Limits
	if lim == (Limits{}) {
		lim = DefaultLimits
	}
	out := make([]outcome, len(todo))
	jobs := make(chan int)
	var wg sync.WaitGroup
	var write sync.Mutex
	for range min(len(todo), max(1, runtime.GOMAXPROCS(0)/2)) {
		wg.Go(func() {
			for i := range jobs {
				out[i] = indexOne(db, todo[i], only[todo[i].PackageID()], lim, &write)
			}
		})
	}
	for i := range todo {
		if ctx.Err() != nil || !opts.Deadline.IsZero() && time.Now().After(opts.Deadline) {
			break
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	out = slices.DeleteFunc(out, func(o outcome) bool { return o.state == "" })
	sort.SliceStable(out, func(i, j int) bool { return out[i].use.PackageID() < out[j].use.PackageID() })
	return out, nil
}

// indexOne indexes one package version unless deps.db holds it already,
// written by this extraction version; a mutable source is read again
// when the stamp of its files differs from the one stored with its rows
// (see Ecosystem.Fingerprint), and a package wanted partially when its
// rows lack a module of only, together with what they hold already (see
// DB.widen). A failure, a panic of an extractor included, is its
// outcome, not an error of the Sync.
func indexOne(db *DB, u store.DepUse, only []string, lim Limits, write *sync.Mutex) (o outcome) {
	o = outcome{use: u, state: store.DepIndexed}
	defer func() {
		if v := recover(); v != nil {
			o.state, o.err = store.DepFailed, fmt.Sprintf("panic: %v", v)
		}
	}()
	fail := func(err error) outcome {
		o.state, o.err = store.DepFailed, err.Error()
		return o
	}
	eco, ok := ecosystems[u.Eco]
	if !ok {
		return fail(errors.New("unknown ecosystem " + u.Eco))
	}
	pkg := Package{ID: idOf(u), Dir: u.Dir, TypesDir: u.TypesDir, Only: db.widen(u.PackageID(), only)}
	current, err := db.holds(u.PackageID(), only)
	if err != nil {
		return fail(err)
	}
	stamp := ""
	if u.Mutable() {
		stamp = eco.Fingerprint(pkg)
		current = current && db.fingerprint(u.PackageID()) == stamp
	}
	if current {
		return o
	}
	ex, err := eco.Extract(pkg, lim)
	if err != nil {
		return fail(err)
	}
	if u.Mutable() {
		ex.Fingerprint, ex.Digest = stamp, digest(ex)
	}
	write.Lock()
	defer write.Unlock()
	if o.wrote, err = db.Write(idOf(u), ex); err != nil {
		return fail(err)
	}
	return o
}

// record stores the outcomes on the uses of the workspace, under its
// write lock, counts them in sum and names the packages written in
// again.
func record(s *store.Store, outcomes []outcome, sum *Summary, again *relinks) (err error) {
	unlock, err := s.WriteLock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return s.Batch(func(b *store.Batch) error {
		for _, o := range outcomes {
			switch {
			case o.state == store.DepFailed:
				sum.Failed++
			case o.wrote:
				sum.Indexed++
				again.rewritten[o.use.PackageID()] = true
			}
			if err := b.SetDepState(o.use.Eco, o.use.PackageName(), o.use.Version, o.state, o.err); err != nil {
				return err
			}
		}
		return nil
	})
}
