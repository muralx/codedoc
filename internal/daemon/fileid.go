package daemon

import (
	"fmt"
	"os"
	"syscall"

	"github.com/muralx/codedoc/internal/buildinfo"
)

// FileID identifies a file by its device and inode, which tell a
// database file apart from one recreated at the same path.
type FileID struct {
	Dev uint64 `json:"dev"`
	Ino uint64 `json:"ino"`
}

// StatFileID returns the FileID of the file at path.
func StatFileID(path string) (FileID, error) {
	info, err := os.Stat(path)
	if err != nil {
		return FileID{}, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return FileID{}, fmt.Errorf("daemon: no device and inode for %s", path)
	}
	return FileID{Dev: uint64(st.Dev), Ino: st.Ino}, nil
}

// NewFreshQuery returns the fresh query a client sends about a repo it
// reads from database db: it carries the database's FileID, zero when
// the file cannot be read, so the daemon trusts only the file it has
// open, and the client's build version. The caller sets Extractor.
func NewFreshQuery(workspace, repo, root, db string) FreshQuery {
	id, _ := StatFileID(db)
	return FreshQuery{Workspace: workspace, Repo: repo, Root: root, DB: db, DBID: id, Version: buildinfo.Version}
}
