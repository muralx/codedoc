package embed

import (
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	ort "github.com/yalue/onnxruntime_go"
)

func TestLibraryPath(t *testing.T) {
	t.Setenv(LibraryEnv, "")
	if got := LibraryPath(); got != DefaultLibraryPath {
		t.Fatalf("default = %q", got)
	}
	t.Setenv(LibraryEnv, "/opt/ort/libonnxruntime.so")
	if got := LibraryPath(); got != "/opt/ort/libonnxruntime.so" {
		t.Fatalf("env override = %q", got)
	}
}

func TestOptions(t *testing.T) {
	cfg := defaultConfig()
	for _, o := range []Option{
		WithMaxLength(128), WithBatchSize(4), WithDimensions(256), WithLibraryPath("/x.so"), WithThreads(3),
		WithMaxLength(0), WithBatchSize(-1), WithDimensions(0), WithLibraryPath(""), WithThreads(0),
	} {
		o(&cfg)
	}
	want := onnxConfig{maxLength: 128, batchSize: 4, dims: 256, threads: 3, libraryPath: "/x.so"}
	if cfg != want {
		t.Fatalf("cfg = %+v, want %+v", cfg, want)
	}
}

func TestDefaultThreads(t *testing.T) {
	t.Setenv(ThreadsEnv, "")
	if n := DefaultThreads(); n < 1 || n > maxDefaultThreads || n > max(1, runtime.NumCPU()/2) {
		t.Fatalf("default threads %d on %d CPUs", n, runtime.NumCPU())
	}
	t.Setenv(ThreadsEnv, "13")
	if n := DefaultThreads(); n != 13 {
		t.Fatalf("%s=13 gives %d", ThreadsEnv, n)
	}
	t.Setenv(ThreadsEnv, "many")
	if n := DefaultThreads(); n > maxDefaultThreads {
		t.Fatalf("an invalid value gives %d", n)
	}
}

func TestMissingRuntimeIsReported(t *testing.T) {
	runtimeState.mu.Lock()
	initialized := runtimeState.initialized
	runtimeState.mu.Unlock()
	if initialized {
		t.Skip("ONNX Runtime already initialized in this process")
	}
	tokPath := writeFile(t, tinyTokenizer)
	missing := filepath.Join(t.TempDir(), "libonnxruntime.so")
	_, err := NewEmbedderWithOptions("model.onnx", tokPath, WithLibraryPath(missing))
	if !errors.Is(err, ErrONNXRuntimeNotAvailable) {
		t.Fatalf("err = %v, want ErrONNXRuntimeNotAvailable", err)
	}
	if _, err := NewEmbedder("model.onnx", filepath.Join(t.TempDir(), "none.json")); err == nil {
		t.Fatal("expected tokenizer error")
	}
}

func TestClosedEmbedder(t *testing.T) {
	e := &ONNXEmbedder{cfg: defaultConfig()}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
	if _, err := e.Embed("x"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Embed after Close = %v", err)
	}
	if _, err := e.EmbedBatch([]string{"x"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("EmbedBatch after Close = %v", err)
	}
	if out, err := e.EmbedBatch(nil); out != nil || err != nil {
		t.Fatalf("empty batch = %v, %v", out, err)
	}
	if e.Dimensions() != 768 {
		t.Fatalf("dims = %d", e.Dimensions())
	}
}

func TestMeanPool(t *testing.T) {
	// batch 2, seq 3, hidden 2; second row's last token is padding.
	data := []float32{
		1, 0, 3, 0, 5, 0,
		0, 2, 0, 4, 9, 9,
	}
	in := batchInput{size: 2, seqLen: 3, mask: []int64{1, 1, 1, 1, 1, 0}}
	got := meanPool(data, ort.NewShape(2, 3, 2), in)
	want := [][]float32{{1, 0}, {0, 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("meanPool = %v, want %v", got, want)
	}
}

func TestLengthOrderAndBatchLayout(t *testing.T) {
	seqs := [][]int32{{2, 4, 5, 3}, {2, 3}, {2, 4, 3}, {2, 5, 3}}
	order := lengthOrder(seqs)
	if !reflect.DeepEqual(order, []int{1, 2, 3, 0}) {
		t.Fatalf("order = %v", order)
	}
	e := &ONNXEmbedder{tokenizer: tinyTok(t, 8), cfg: defaultConfig()}
	in := e.tokenizeBatch(seqs, []int{1, 2}, 0)
	if in.size != 2 || in.seqLen != 3 {
		t.Fatalf("layout %d x %d", in.size, in.seqLen)
	}
	if !reflect.DeepEqual(in.ids, []int64{2, 3, 0, 2, 4, 3}) || !reflect.DeepEqual(in.mask, []int64{1, 1, 0, 1, 1, 1}) {
		t.Fatalf("ids %v mask %v", in.ids, in.mask)
	}
	if in := e.tokenizeBatch(seqs, []int{1}, 5); in.seqLen != 5 || len(in.ids) != 5 {
		t.Fatalf("minimum length: %+v", in)
	}
}
