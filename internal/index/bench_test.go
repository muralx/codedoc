package index_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

const (
	benchPackages = 100
	benchFilesPer = 50 // 5,000 files
	benchImports  = 10 // 50,000 imports
)

// largeRepo writes a Go module of benchPackages packages, each file
// importing benchImports sibling packages and reading one of
// benchFilesPer config keys (5,000 reads, 100 per key) that config/app.env
// defines, indexes and resolves it.
func largeRepo(tb testing.TB) (*store.Store, *config.Repo, index.Options) {
	tb.Helper()
	root := tb.TempDir()
	writeBench(tb, root, "go.mod", "module example.com/big\n")
	writeBench(tb, root, "config/app.env", benchEnv(-1))
	for p := 0; p < benchPackages; p++ {
		for f := 0; f < benchFilesPer; f++ {
			writeBench(tb, root, fmt.Sprintf("p%d/f%d.go", p, f), benchSource(p, f))
		}
	}
	s, err := store.Open(filepath.Join(tb.TempDir(), "ws.db"))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = s.Close() })
	rep := &config.Repo{Name: "big", Path: root}
	opts := index.Options{Workspace: "bench", MaxFiles: 200, RepoOrder: []string{"big"}}
	if _, err := index.Repo(s, rep, opts); err != nil {
		tb.Fatal(err)
	}
	if err := index.ResolvePending(s, opts); err != nil {
		tb.Fatal(err)
	}
	return s, rep, opts
}

// benchSource is file f of package p: benchImports imports, all used.
func benchSource(p, f int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package p%d\n\nimport (\n\t\"os\"\n", p)
	for i := 1; i <= benchImports; i++ {
		fmt.Fprintf(&b, "\tq%d \"example.com/big/p%d\"\n", i, (p+i)%benchPackages)
	}
	fmt.Fprintf(&b, ")\n\nfunc F%d() { q1.F0(); q2.F0() }\n", f)
	fmt.Fprintf(&b, "func C%d() string { return os.Getenv(\"BENCH_KEY_%d\") }\n", f, f)
	for i := 3; i <= benchImports; i++ {
		fmt.Fprintf(&b, "var _ = q%d.F0\n", i)
	}
	return b.String()
}

// benchEnv defines the config keys BENCH_KEY_0 to BENCH_KEY_49 but skip.
func benchEnv(skip int) string {
	var b strings.Builder
	for i := 0; i < benchFilesPer; i++ {
		if i != skip {
			fmt.Fprintf(&b, "BENCH_KEY_%d=value%d\n", i, i)
		}
	}
	return b.String()
}

func writeBench(tb testing.TB, root, rel, text string) {
	tb.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		tb.Fatal(err)
	}
}

// BenchmarkEnsureOneFileEdit measures a freshness check that re-extracts
// one edited file of a 5,000-file, 50,000-import repo and resolves it.
func BenchmarkEnsureOneFileEdit(b *testing.B) {
	s, rep, opts := largeRepo(b)
	target := filepath.Join(rep.Path, "p7", "f3.go")
	n := 0
	for b.Loop() {
		b.StopTimer()
		n++
		f, err := os.OpenFile(target, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			b.Fatal(err)
		}
		_, err = fmt.Fprintf(f, "\nfunc Edit%d() {}\n", n)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		fr, err := index.Ensure(s, rep, opts)
		if err != nil || fr.Changed != 1 {
			b.Fatalf("%+v %v", fr, err)
		}
	}
}

// BenchmarkEnsureOneFileBodyEdit measures a freshness check after a body
// edit of one function: the file is re-extracted, its references and the
// references named like its symbols resolved again.
func BenchmarkEnsureOneFileBodyEdit(b *testing.B) {
	s, rep, opts := largeRepo(b)
	target := filepath.Join(rep.Path, "p7", "f3.go")
	src := benchSource(7, 3)
	edited := strings.Replace(src, "q1.F0(); q2.F0()", "q2.F0(); q1.F0(); q3.F0()", 1)
	n := 0
	for b.Loop() {
		b.StopTimer()
		n++
		text := edited
		if n%2 == 0 {
			text = src
		}
		writeBench(b, rep.Path, "p7/f3.go", text)
		b.StartTimer()
		fr, err := index.Ensure(s, rep, opts)
		if err != nil || fr.Changed != 1 {
			b.Fatalf("%s: %+v %v", target, fr, err)
		}
	}
}

// BenchmarkEnsurePopularNameBodyEdit measures a body edit of the file
// defining F0 in package p7, a name every file of the repo references
// ten times (50,000 references): the edit keeps F0's kind and arity, so
// none of those references is resolved again.
func BenchmarkEnsurePopularNameBodyEdit(b *testing.B) {
	s, rep, opts := largeRepo(b)
	src := benchSource(7, 0)
	edited := strings.Replace(src, "q1.F0(); q2.F0()", "q2.F0(); q1.F0()", 1)
	n := 0
	for b.Loop() {
		b.StopTimer()
		n++
		text := edited
		if n%2 == 0 {
			text = src
		}
		writeBench(b, rep.Path, "p7/f0.go", text)
		b.StartTimer()
		fr, err := index.Ensure(s, rep, opts)
		if err != nil || fr.Changed != 1 {
			b.Fatalf("%+v %v", fr, err)
		}
	}
}

// BenchmarkEnsureConfigEdit measures a freshness check after a config
// file edit that removes or restores the definition of a key 100 files
// read: the file is re-extracted and the reads of that key matched again.
func BenchmarkEnsureConfigEdit(b *testing.B) {
	s, rep, opts := largeRepo(b)
	n := 0
	for b.Loop() {
		b.StopTimer()
		n++
		skip := -1
		if n%2 == 1 {
			skip = 7
		}
		writeBench(b, rep.Path, "config/app.env", benchEnv(skip))
		b.StartTimer()
		fr, err := index.Ensure(s, rep, opts)
		if err != nil || fr.Changed != 1 {
			b.Fatalf("%+v %v", fr, err)
		}
	}
}

// typesRepo writes a Go module of 100 packages of 25 files, each file
// declaring one interface and one struct implementing it (5,000 types),
// indexes and resolves it.
func typesRepo(tb testing.TB) (*store.Store, *config.Repo, index.Options) {
	tb.Helper()
	root := tb.TempDir()
	writeBench(tb, root, "go.mod", "module example.com/types\n")
	for p := 0; p < benchPackages; p++ {
		for f := 0; f < 25; f++ {
			writeBench(tb, root, fmt.Sprintf("p%d/t%d.go", p, f), typesSource(p, f, "return 1"))
		}
	}
	s, err := store.Open(filepath.Join(tb.TempDir(), "ws.db"))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = s.Close() })
	rep := &config.Repo{Name: "types", Path: root}
	opts := index.Options{Workspace: "bench", MaxFiles: 200, RepoOrder: []string{"types"}}
	if _, err := index.Repo(s, rep, opts); err != nil {
		tb.Fatal(err)
	}
	if err := index.ResolvePending(s, opts); err != nil {
		tb.Fatal(err)
	}
	return s, rep, opts
}

// typesSource is file f of package p: interface I<f> and struct S<f>
// with the methods M<p>_<f> and N; body is the body of S<f>.N.
func typesSource(p, f int, body string) string {
	return fmt.Sprintf("package p%d\n\ntype I%d interface {\n\tM%d_%d() int\n\tN() int\n}\n\n"+
		"type S%d struct{}\n\nfunc (S%d) M%d_%d() int { return %d }\n\nfunc (S%d) N() int { %s }\n",
		p, f, p, f, f, f, p, f, f, f, body)
}

// BenchmarkEnsureTypesBodyEdit measures a body edit of a method in a
// repo of 5,000 interfaces and structs: no implements edge is computed
// again.
func BenchmarkEnsureTypesBodyEdit(b *testing.B) {
	benchTypesEdit(b, "return 2")
}

// BenchmarkEnsureTypesMethodAdd measures adding a method to a struct in
// a repo of 5,000 interfaces and structs: the implements edges of its
// package are computed again.
func BenchmarkEnsureTypesMethodAdd(b *testing.B) {
	benchTypesEdit(b, "return 1 }\n\nfunc (S3) Extra() int { return 0")
}

func benchTypesEdit(b *testing.B, body string) {
	s, rep, opts := typesRepo(b)
	n := 0
	for b.Loop() {
		b.StopTimer()
		n++
		text := typesSource(7, 3, body)
		if n%2 == 0 {
			text = typesSource(7, 3, "return 1")
		}
		writeBench(b, rep.Path, "p7/t3.go", text)
		b.StartTimer()
		fr, err := index.Ensure(s, rep, opts)
		if err != nil || fr.Changed != 1 {
			b.Fatalf("%+v %v", fr, err)
		}
	}
}

// TestEnsureOneFileEditIsFast holds the benchmarks to their budget. It is
// skipped in -short runs and under the race detector, whose slowdown
// makes the timing meaningless. An edit takes 70 to 140 ms on a quiet
// machine, most of it the two freshness scans of the 5,000 files (before
// and under the write lock); the budget leaves three times the slowest
// for a machine running other packages' tests, and still fails an edit
// that resolves the repo again, which takes seconds.
func TestEnsureOneFileEditIsFast(t *testing.T) {
	const editBudget = 450 * time.Millisecond
	if testing.Short() || raceEnabled {
		t.Skip("timing budget needs a full, non-race run")
	}
	for name, bench := range map[string]func(*testing.B){
		"new function": BenchmarkEnsureOneFileEdit, "body edit": BenchmarkEnsureOneFileBodyEdit,
		"config edit": BenchmarkEnsureConfigEdit, "popular name body edit": BenchmarkEnsurePopularNameBodyEdit,
		"types body edit": BenchmarkEnsureTypesBodyEdit, "types method add": BenchmarkEnsureTypesMethodAdd,
	} {
		r := testing.Benchmark(bench)
		if r.N == 0 {
			t.Fatalf("%s: benchmark failed", name)
		}
		d := time.Duration(r.NsPerOp())
		t.Logf("%s: one-file edit + Ensure took %v", name, d)
		if d > editBudget {
			t.Errorf("%s: one-file edit + Ensure took %v, budget %v", name, d, editBudget)
		}
	}
}
