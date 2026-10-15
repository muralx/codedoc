package store

// DocPrior is the doc tracking state of a stored symbol.
type DocPrior struct {
	DocHash, BodyHash, DocBodyHash string
}

// DocPriors returns the doc tracking state of the symbols of the given
// files, parked or not, by stable key.
func (b *Batch) DocPriors(fileIDs ...int64) (map[string]DocPrior, error) {
	out := map[string]DocPrior{}
	return out, inChunks(fileIDs, func(chunk []any) error {
		rows, err := cached{b}.Query(`SELECT stable_key, COALESCE(doc_hash,''), body_hash, COALESCE(doc_body_hash,'')
  FROM symbol WHERE file_id IN (`+placeholders(len(chunk))+`)`, chunk...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var key string
			var p DocPrior
			if err := rows.Scan(&key, &p.DocHash, &p.BodyHash, &p.DocBodyHash); err != nil {
				return err
			}
			out[unparkedKey(key)] = p
		}
		return rows.Err()
	})
}
