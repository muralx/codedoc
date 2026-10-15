package store

import (
	"container/heap"
	"database/sql"
	"encoding/binary"
	"unsafe"
)

// SearchFilter limits a vector scan or a text search; zero fields match
// everything.
type SearchFilter struct {
	// Kind is a symbol kind.
	Kind string
	// Langs are the precise file languages of symbols (go, typescript,
	// tsx, ...); Family is the module language (go, ts, py, java).
	Langs  []string
	Family string
	// RepoID limits to one repo; RepoIDs, when not nil, to those repos
	// (the package versions of deps.db a workspace uses), none when empty.
	RepoID  int64
	RepoIDs []int64
}

// Scored is a row id with its score, higher is better. For a symbol the
// score is that of its best passage, and Passages are its best ones.
type Scored struct {
	ID       int64
	Score    float64
	Passages []ScoredPassage
}

// NearestModules returns the k module vectors closest to q by cosine
// similarity, best first: vectors are unit length, so the score is the
// dot product. Rows stream through a bounded min-heap; vectors of another
// size are skipped. Only documented modules are scanned; f.Kind is
// ignored.
func (s *Store) NearestModules(q []float32, f SearchFilter, k int) ([]Scored, error) {
	where, args := moduleFilter(f)
	return s.nearest(`SELECT v.module_id, v.vec FROM module_vec v JOIN module m ON m.id = v.module_id
  WHERE `+moduleDocumentedSQL+where, args, q, k)
}

func (s *Store) nearest(query string, args []any, q []float32, k int) ([]Scored, error) {
	if k <= 0 {
		return nil, nil
	}
	rows, err := s.scan(query, args)
	if err != nil {
		return nil, err
	}
	h := &minHeap{}
	var id int64
	var blob sql.RawBytes
	for rows.Next() {
		if err := rows.Scan(&id, &blob); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if len(blob) != 4*len(q) {
			continue
		}
		score := dot(q, blob)
		if h.Len() < k {
			heap.Push(h, Scored{ID: id, Score: score})
		} else if score > (*h)[0].Score {
			(*h)[0] = Scored{ID: id, Score: score}
			heap.Fix(h, 0)
		}
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	out := []Scored(*h)
	sortScored(out)
	return out, nil
}

// mmapSize is the most bytes of the database a scan maps into memory.
const mmapSize = "4294967296"

// dot is the dot product of q and a little-endian float32 BLOB of the
// same length, in four partial sums.
func dot(q []float32, b []byte) float64 {
	v := floats(b, len(q))
	var s0, s1, s2, s3 float32
	i := 0
	for ; i+4 <= len(q); i += 4 {
		s0 += q[i] * v[i]
		s1 += q[i+1] * v[i+1]
		s2 += q[i+2] * v[i+2]
		s3 += q[i+3] * v[i+3]
	}
	for ; i < len(q); i++ {
		s0 += q[i] * v[i]
	}
	return float64(s0 + s1 + s2 + s3)
}

// floats views a little-endian float32 BLOB as n floats: in place on a
// little-endian machine when b is 4-byte aligned, else decoded.
func floats(b []byte, n int) []float32 {
	if littleEndian && uintptr(unsafe.Pointer(unsafe.SliceData(b)))%4 == 0 {
		return unsafe.Slice((*float32)(unsafe.Pointer(unsafe.SliceData(b))), n)
	}
	return DecodeVec(b[:4*n])
}

// littleEndian reports whether the machine stores integers little-endian,
// the BLOB layout.
var littleEndian = binary.NativeEndian.Uint16([]byte{1, 0}) == 1

// minHeap keeps the best k scores with the worst on top.
type minHeap []Scored

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return h[i].Score < h[j].Score }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

// Push adds an element; container/heap calls it.
func (h *minHeap) Push(x any) { *h = append(*h, x.(Scored)) }

// Pop removes the last element; container/heap calls it.
func (h *minHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// symbolFilter is the condition (starting with AND, empty without
// filters) and arguments limiting symbol s of file f by a filter.
func symbolFilter(f SearchFilter) (string, []any) {
	where, args := "", []any{}
	if f.Kind != "" {
		where += ` AND s.kind = ?`
		args = append(args, f.Kind)
	}
	if len(f.Langs) > 0 {
		where += ` AND f.lang IN (` + placeholders(len(f.Langs)) + `)`
		args = append(args, anys(f.Langs)...)
	}
	if f.RepoID != 0 {
		where += ` AND f.repo_id = ?`
		args = append(args, f.RepoID)
	}
	if f.RepoIDs != nil {
		where += ` AND f.repo_id IN (SELECT value FROM json_each(?))`
		args = append(args, idList(f.RepoIDs))
	}
	return where, args
}

// moduleFilter limits module m by the family and repo of a filter.
func moduleFilter(f SearchFilter) (string, []any) {
	where, args := "", []any{}
	if f.Family != "" {
		where += ` AND m.lang = ?`
		args = append(args, f.Family)
	}
	if f.RepoID != 0 {
		where += ` AND m.repo_id = ?`
		args = append(args, f.RepoID)
	}
	if f.RepoIDs != nil {
		where += ` AND m.repo_id IN (SELECT value FROM json_each(?))`
		args = append(args, idList(f.RepoIDs))
	}
	return where, args
}

// Checkpoint copies the write-ahead log into the database file and
// truncates it. Reads of pages still in the log bypass the memory map, so
// a bulk write (an embedding pass) is followed by a checkpoint.
func (s *Store) Checkpoint() error {
	_, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}
