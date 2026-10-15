package mcpserver

import (
	"os"

	"github.com/muralx/codedoc/internal/session"
)

// sessionLocks holds the locks a server took for the repos of its
// workspace.
type sessionLocks []*os.File

// takeLocks locks every repo of a workspace, logging (not failing) for a
// repo another session holds or whose lock cannot be created.
func takeLocks(dataDir, workspace string, repos []string, log Logger) sessionLocks {
	var held sessionLocks
	for _, r := range repos {
		f, err := session.Lock(dataDir, workspace, r)
		if err != nil {
			log.Printf("session lock %s/%s not taken: %v", workspace, r, err)
			continue
		}
		held = append(held, f)
	}
	return held
}

// release closes the lock files, which releases the locks.
func (l sessionLocks) release() {
	for _, f := range l {
		_ = f.Close()
	}
}
