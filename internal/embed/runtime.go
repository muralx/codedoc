package embed

import (
	"os"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// LibraryEnv names the environment variable that overrides the ONNX Runtime
// shared library path.
const LibraryEnv = "CODEDOC_ONNXRUNTIME_LIB"

// DefaultLibraryPath is the ONNX Runtime shared library used when LibraryEnv
// is unset.
const DefaultLibraryPath = "/usr/local/lib/libonnxruntime.so"

// LibraryPath returns the ONNX Runtime shared library path: the value of
// LibraryEnv when set, DefaultLibraryPath otherwise.
func LibraryPath() string {
	if p := os.Getenv(LibraryEnv); p != "" {
		return p
	}
	return DefaultLibraryPath
}

// runtimeState guards the process-wide ONNX Runtime environment. The library
// is loaded with dlopen lazily, on the first embedder construction, so
// binaries start and run without it. A failed load may be retried, for
// instance with a different path.
var runtimeState struct {
	mu          sync.Mutex
	initialized bool
	users       int // embedders holding the environment
}

// initRuntime loads the shared library at path and initializes the ONNX
// Runtime environment when no embedder holds it, and counts the caller
// as holding it until releaseRuntime.
func initRuntime(path string) error {
	runtimeState.mu.Lock()
	defer runtimeState.mu.Unlock()
	if !runtimeState.initialized {
		ort.SetSharedLibraryPath(path)
		if err := ort.InitializeEnvironment(); err != nil {
			return err
		}
		runtimeState.initialized = true
	}
	runtimeState.users++
	return nil
}

// releaseRuntime ends a hold of initRuntime. The last one destroys the
// environment and returns the freed memory to the system: a destroyed
// session alone leaves about 1.2 GB resident, the environment and
// glibc's free lists keeping what the model used.
func releaseRuntime() {
	runtimeState.mu.Lock()
	defer runtimeState.mu.Unlock()
	if runtimeState.users--; runtimeState.users > 0 || !runtimeState.initialized {
		return
	}
	_ = ort.DestroyEnvironment()
	runtimeState.initialized = false
	releaseFreed()
}
