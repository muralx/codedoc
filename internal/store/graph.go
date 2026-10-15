package store

import "strings"

const edgeColumns = `e.id, e.src_symbol_id, COALESCE(e.dst_symbol_id,0), e.kind, e.ref_kind, e.confidence, e.file_id,
  e.line, COALESCE(e.reference_id,0), COALESCE(e.dst_dep_key,'')`

// EdgesTo returns the edges of the given kinds that end at a symbol,
// including the ambiguous edges listing it as a candidate, ordered by id.
func (s *Store) EdgesTo(id int64, kinds ...string) ([]Edge, error) {
	in := placeholders(len(kinds))
	args := append(append(append([]any{id}, anys(kinds)...), id), anys(kinds)...)
	return s.queryEdges(`SELECT `+edgeColumns+` FROM edge e WHERE e.dst_symbol_id = ? AND e.kind IN (`+in+`)
  UNION SELECT `+edgeColumns+` FROM edge_candidate c JOIN edge e ON e.id = c.edge_id
  WHERE c.symbol_id = ? AND e.kind IN (`+in+`) ORDER BY 1`, args...)
}

// EdgesToDep returns the edges of the given kinds whose target is the
// dependency symbol with a stable key (see Edge.DepKey), ordered by id.
func (s *Store) EdgesToDep(key string, kinds ...string) ([]Edge, error) {
	return s.queryEdges(`SELECT `+edgeColumns+` FROM edge e WHERE e.dst_dep_key = ? AND e.kind IN (`+
		placeholders(len(kinds))+`) ORDER BY e.id`, append([]any{key}, anys(kinds)...)...)
}

// DepFanIns counts, per dependency symbol key, the edges of the
// workspace that end at it; keys without edges are absent.
func (s *Store) DepFanIns(keys []string) (map[string]int, error) {
	out := map[string]int{}
	err := inChunks(keys, func(chunk []any) error {
		rows, err := s.db.Query(`SELECT dst_dep_key, COUNT(*) FROM edge WHERE dst_dep_key IN (`+
			placeholders(len(chunk))+`) GROUP BY dst_dep_key`, chunk...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				_ = rows.Close()
				return err
			}
			out[k] = n
		}
		return closeRows(rows)
	})
	return out, err
}

// DepUseCount counts the edges of the workspace that end at one
// dependency symbol.
type DepUseCount struct {
	Key string
	N   int
}

// DepKeysUsed returns the dependency symbols of one package version (the
// keys starting with its package id) that edges of the workspace end at,
// most used first, at most limit.
func (s *Store) DepKeysUsed(packageID string, limit int) ([]DepUseCount, error) {
	rows, err := s.db.Query(`SELECT dst_dep_key, COUNT(*) FROM edge WHERE dst_dep_key >= ? AND dst_dep_key < ?
  GROUP BY dst_dep_key ORDER BY 2 DESC, 1 LIMIT ?`, packageID+":", packageID+";", limit)
	if err != nil {
		return nil, err
	}
	var out []DepUseCount
	for rows.Next() {
		var u DepUseCount
		if err := rows.Scan(&u.Key, &u.N); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, u)
	}
	return out, closeRows(rows)
}

// EdgesFrom returns the edges of the given kinds that leave a symbol,
// ordered by line and id.
func (s *Store) EdgesFrom(id int64, kinds ...string) ([]Edge, error) {
	return s.queryEdges(`SELECT `+edgeColumns+` FROM edge e WHERE e.src_symbol_id = ? AND e.kind IN (`+
		placeholders(len(kinds))+`) ORDER BY e.line, e.id`, append([]any{id}, anys(kinds)...)...)
}

// EdgesByID returns the edges with the given ids, in id order.
func (s *Store) EdgesByID(ids []int64) ([]Edge, error) {
	var out []Edge
	err := inChunks(ids, func(chunk []any) error {
		es, err := s.queryEdges(`SELECT `+edgeColumns+` FROM edge e WHERE e.id IN (`+placeholders(len(chunk))+
			`) ORDER BY e.id`, chunk...)
		out = append(out, es...)
		return err
	})
	return out, err
}

// EdgeCounts returns how many edges end at a symbol (as target or as a
// candidate of an ambiguous edge) and how many leave it.
func (s *Store) EdgeCounts(id int64) (in, out int, err error) {
	err = s.db.QueryRow(`SELECT
  (SELECT COUNT(*) FROM edge WHERE dst_symbol_id = ?) + (SELECT COUNT(*) FROM edge_candidate WHERE symbol_id = ?),
  (SELECT COUNT(*) FROM edge WHERE src_symbol_id = ?)`, id, id, id).Scan(&in, &out)
	return in, out, err
}

// FanIns returns the fan-in (see EdgeCounts) of each of ids in one
// query; symbols without inbound edges are absent.
func (s *Store) FanIns(ids []int64) (map[int64]int, error) {
	out := map[int64]int{}
	if len(ids) == 0 {
		return out, nil
	}
	list := idList(ids)
	rows, err := s.db.Query(`SELECT id, SUM(n) FROM (
  SELECT dst_symbol_id AS id, COUNT(*) AS n FROM edge WHERE dst_symbol_id IN (SELECT value FROM json_each(?)) GROUP BY dst_symbol_id
  UNION ALL
  SELECT symbol_id, COUNT(*) FROM edge_candidate WHERE symbol_id IN (SELECT value FROM json_each(?)) GROUP BY symbol_id
) GROUP BY id`, list, list)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out[id] = n
	}
	return out, closeRows(rows)
}

// queryEdges runs an edge query and fills the candidates of ambiguous
// edges.
func (s *Store) queryEdges(q string, args ...any) ([]Edge, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	var out []Edge
	var ambiguous []int64
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.ID, &e.Src, &e.Dst, &e.Kind, &e.RefKind, &e.Confidence, &e.FileID, &e.Line,
			&e.ReferenceID, &e.DepKey); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if e.Dst == 0 && e.DepKey == "" {
			ambiguous = append(ambiguous, e.ID)
		}
		out = append(out, e)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	cands, err := s.candidates(ambiguous)
	for i := range out {
		out[i].Candidates = cands[out[i].ID]
	}
	return out, err
}

// candidates returns the candidate symbols of ambiguous edges by edge id,
// in insertion order.
func (s *Store) candidates(edgeIDs []int64) (map[int64][]int64, error) {
	out := map[int64][]int64{}
	err := inChunks(edgeIDs, func(chunk []any) error {
		rows, err := s.db.Query(`SELECT edge_id, symbol_id FROM edge_candidate WHERE edge_id IN (`+
			placeholders(len(chunk))+`) ORDER BY rowid`, chunk...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var e, c int64
			if err := rows.Scan(&e, &c); err != nil {
				_ = rows.Close()
				return err
			}
			out[e] = append(out[e], c)
		}
		return closeRows(rows)
	})
	return out, err
}

// Step is one row of a graph walk: Node was reached from Parent over
// edge EdgeID at Depth. Node is zero for an ambiguous outbound edge,
// which a walk does not follow.
type Step struct {
	Node, Parent, EdgeID int64
	Depth                int
}

// WalkOpts control a graph walk.
type WalkOpts struct {
	// Inbound walks from targets to sources (callers); otherwise from
	// sources to targets (callees).
	Inbound bool
	Kinds   []string
	// Depth is the most edges from the start; Limit caps the rows read.
	Depth, Limit int
	// NoTests skips the steps reaching a test symbol: one with is_test or
	// one in TestFiles, the ids of the test files (the caller decides
	// which paths are test code).
	NoTests   bool
	TestFiles []int64
}

// Walk follows edges from start breadth first with a recursive CTE. Each
// row carries its path, so a node is never revisited on its own path
// (the visited set), and the walk stops after o.Limit rows (the node
// cap). Inbound walks also follow ambiguous edges listing a node as a
// candidate. A node can appear several times, reached over different
// paths; the shallowest comes first.
func (s *Store) Walk(start int64, o WalkOpts) ([]Step, error) {
	rows, err := s.db.Query(walkSQL(o), walkArgs(start, o)...)
	if err != nil {
		return nil, err
	}
	var out []Step
	for rows.Next() {
		var st Step
		if err := rows.Scan(&st.Node, &st.Depth, &st.Parent, &st.EdgeID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, st)
	}
	return out, closeRows(rows)
}

func walkSQL(o WalkOpts) string {
	in := placeholders(len(o.Kinds))
	cond := ` AND e.kind IN (` + in + `) AND up.depth < ?`
	next := `e.src_symbol_id`
	if !o.Inbound {
		next = `COALESCE(e.dst_symbol_id, 0)`
	}
	if o.NoTests {
		cond += ` AND NOT EXISTS (SELECT 1 FROM symbol t WHERE t.id = ` + next +
			` AND (t.is_test OR t.file_id IN (SELECT value FROM json_each(?))))`
	}
	step := `SELECT ` + next + `, up.depth + 1, up.id, e.id, up.path || ` + next + ` || ','
  FROM up JOIN `
	visited := ` AND instr(up.path, ',' || ` + next + ` || ',') = 0`
	var b strings.Builder
	b.WriteString(`WITH RECURSIVE up(id, depth, parent, edge_id, path) AS (
  SELECT ?, 0, 0, 0, ',' || ? || ','
  UNION ALL `)
	if o.Inbound {
		b.WriteString(step + `edge e ON e.dst_symbol_id = up.id WHERE 1` + cond + visited + `
  UNION ALL ` + step + `edge_candidate c ON c.symbol_id = up.id JOIN edge e ON e.id = c.edge_id WHERE 1` +
			cond + visited)
	} else {
		b.WriteString(step + `edge e ON e.src_symbol_id = up.id WHERE 1` + cond + visited)
	}
	b.WriteString(`
  LIMIT ?)
SELECT id, depth, parent, edge_id FROM up WHERE depth > 0`)
	return b.String()
}

func walkArgs(start int64, o WalkOpts) []any {
	args := []any{start, start}
	branch := append(anys(o.Kinds), o.Depth)
	if o.NoTests {
		branch = append(branch, idList(o.TestFiles))
	}
	args = append(args, branch...)
	if o.Inbound {
		args = append(args, branch...)
	}
	return append(args, o.Limit+1) // the start row counts against the limit
}
