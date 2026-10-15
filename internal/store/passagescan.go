package store

import (
	"database/sql"
	"sort"
)

// MaxHitPassages is the most passages a scan reports for one symbol.
const MaxHitPassages = 3

// ScoredPassage is a passage of a symbol's embedded text with its
// score: the 1-based lines of the file it covers.
type ScoredPassage struct {
	StartLine, EndLine int
	Score              float64
}

// passageLinesSQL are the first and last file line of passage v of
// symbol s.
const passageLinesSQL = `v.start_line + CASE WHEN v.from_doc = 1 THEN COALESCE(s.doc_start_line, s.start_line)
    ELSE s.start_line END,
  v.end_line + CASE WHEN v.from_doc = 1 THEN COALESCE(s.doc_start_line, s.start_line) ELSE s.start_line END`

// NearestSymbols returns the k symbols whose passage vectors are closest
// to q by cosine similarity, best first: vectors are unit length, so the
// score is the dot product, and a symbol scores as its best passage.
// Each symbol is returned once, with its MaxHitPassages best passages,
// best first. Vectors of another size are skipped, as are those of
// parked symbols.
func (s *Store) NearestSymbols(q []float32, f SearchFilter, k int) ([]Scored, error) {
	if k <= 0 {
		return nil, nil
	}
	where, args := symbolFilter(f)
	from := `symbol_vec v JOIN symbol s ON s.id = v.symbol_id`
	if where != "" {
		from += ` JOIN file f ON f.id = s.file_id`
	}
	rows, err := s.scan(`SELECT v.symbol_id, `+passageLinesSQL+`, v.vec FROM `+from+` WHERE `+unparkedSQL+where, args)
	if err != nil {
		return nil, err
	}
	out, err := scoreSymbols(rows, q)
	if err != nil {
		return nil, err
	}
	sortScored(out)
	return out[:min(k, len(out))], nil
}

// scoreSymbols reads the passage rows of a scan and returns one row per
// symbol with its best passages, in the order symbols are first met.
func scoreSymbols(rows *sql.Rows, q []float32) ([]Scored, error) {
	var out []Scored
	at := map[int64]int{}
	var id int64
	var p ScoredPassage
	var blob sql.RawBytes
	for rows.Next() {
		if err := rows.Scan(&id, &p.StartLine, &p.EndLine, &blob); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if len(blob) != 4*len(q) {
			continue
		}
		p.Score = dot(q, blob)
		i, seen := at[id]
		if !seen {
			i = len(out)
			at[id] = i
			out = append(out, Scored{ID: id, Score: p.Score})
		}
		out[i].add(p)
	}
	return out, closeRows(rows)
}

// add records a passage of the symbol, keeping the MaxHitPassages best
// in order.
func (sc *Scored) add(p ScoredPassage) {
	sc.Score = max(sc.Score, p.Score)
	at := sort.Search(len(sc.Passages), func(i int) bool { return sc.Passages[i].Score < p.Score })
	if at >= MaxHitPassages {
		return
	}
	if len(sc.Passages) < MaxHitPassages {
		sc.Passages = append(sc.Passages, ScoredPassage{})
	}
	copy(sc.Passages[at+1:], sc.Passages[at:])
	sc.Passages[at] = p
}

// sortScored orders rows best first, the lower id first on a tie.
func sortScored(rows []Scored) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Score != rows[j].Score {
			return rows[i].Score > rows[j].Score
		}
		return rows[i].ID < rows[j].ID
	})
}

// scan runs a vector scan query. Memory-mapped reads skip a copy per
// page; a scan of 100k vectors takes about half the time. The pragma is
// per connection, so it is set before every scan.
func (s *Store) scan(query string, args []any) (*sql.Rows, error) {
	if _, err := s.db.Exec(`PRAGMA mmap_size = ` + mmapSize); err != nil {
		return nil, err
	}
	return s.db.Query(query, args...)
}
