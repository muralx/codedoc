package embed

import (
	"os"
	"runtime"
	"strconv"

	ort "github.com/yalue/onnxruntime_go"
)

// ThreadsEnv names the environment variable that sets how many threads
// one inference call uses; unset or invalid, DefaultThreads decides.
const ThreadsEnv = "CODEDOC_EMBED_THREADS"

// maxDefaultThreads bounds DefaultThreads. ONNX Runtime's own default is
// one thread per core, each spinning while it waits. Measured on a
// machine of 28 logical CPUs embedding 1,000 texts: 20 threads take 64 s
// and 1,246 CPU seconds, 12 threads the same 64 s and 758 CPU seconds, 8
// threads 80 s, 4 threads 115 s. Past 12 a thread adds heat, not speed.
const maxDefaultThreads = 12

// DefaultThreads returns the threads of one inference call: the value of
// ThreadsEnv when it is a positive number, else half the logical CPUs,
// at least one and at most maxDefaultThreads.
func DefaultThreads() int {
	if n, err := strconv.Atoi(os.Getenv(ThreadsEnv)); err == nil && n > 0 {
		return n
	}
	return max(1, min(runtime.NumCPU()/2, maxDefaultThreads))
}

// WithThreads sets the number of threads one inference call uses
// (default DefaultThreads).
func WithThreads(n int) Option {
	return func(c *onnxConfig) {
		if n > 0 {
			c.threads = n
		}
	}
}

// sessionOptions returns the ONNX Runtime options of a session that
// computes with threads threads; the caller destroys them.
func sessionOptions(threads int) (*ort.SessionOptions, error) {
	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	if err := opts.SetIntraOpNumThreads(threads); err != nil {
		_ = opts.Destroy()
		return nil, err
	}
	return opts, nil
}
