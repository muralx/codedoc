package embed

// #include <malloc.h>
import "C"

// releaseFreed returns the memory glibc keeps after a session is
// destroyed to the system: about 550 MB of the model's arenas would
// otherwise stay in the process's resident set.
func releaseFreed() { C.malloc_trim(0) }
