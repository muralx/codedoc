package store

import "strings"

// parkPrefix starts the stable key of a parked symbol: a symbol of a file
// being re-extracted, kept (with its id and the edges into it) until the
// file is written again. The prefix and the file id keep parked keys out
// of every key lookup and unique among themselves.
const parkPrefix = "\x01"

// unparkedKey returns the stable key of a possibly parked key.
func unparkedKey(key string) string {
	if !strings.HasPrefix(key, parkPrefix) {
		return key
	}
	_, k, _ := strings.Cut(key[len(parkPrefix):], parkPrefix)
	return k
}

// ParkFileSymbols parks the symbols of a file about to be re-extracted:
// their stable keys move out of the way of the keys the next writes
// assign, but rows, ids and the edges into them stay until
// SyncFileSymbols matches them by key. The file row is marked never
// hashed (empty hash, zero mtime), so a run that stops before the file is
// written again re-extracts it. It returns the stable keys, names and
// kinds of the parked symbols.
func (b *Batch) ParkFileSymbols(fileID int64) ([]Cleared, error) {
	syms, err := querySymbols(cached{b}, `WHERE s.file_id = ? ORDER BY s.id`, fileID)
	if err != nil {
		return nil, err
	}
	out := make([]Cleared, len(syms))
	for i, sy := range syms {
		out[i] = Cleared{Key: unparkedKey(sy.StableKey), Name: sy.Name, Kind: sy.Kind}
	}
	if _, err := (cached{b}).Exec(`UPDATE symbol SET stable_key = ? || file_id || ? || stable_key
  WHERE file_id = ? AND substr(stable_key, 1, 1) <> ?`, parkPrefix, parkPrefix, fileID, parkPrefix); err != nil {
		return nil, err
	}
	_, err = (cached{b}).Exec(`UPDATE file SET hash = '', mtime = 0 WHERE id = ?`, fileID)
	return out, err
}

// FileSymbols is what SyncFileSymbols did to the symbols of a file.
type FileSymbols struct {
	// IDs maps the stable keys written to their row ids.
	IDs map[string]int64
	// Added are the symbols whose key the file did not hold; Removed the
	// rows whose key it no longer holds (deleted with their edges).
	Added, Removed []Symbol
	// Changed pairs the old and new version of the symbols kept by key
	// whose kind, signature, body hash or value moved.
	Changed [][2]Symbol
}

// SyncFileSymbols makes syms the symbols of a file. A stored row (parked
// or not) whose stable key is among syms is updated in place, so its id
// and the edges into it survive; the other rows are deleted and the new
// keys inserted. moduleID 0 stores NULL. Detected edges leaving an
// updated symbol (Go implements) follow its start line.
func (b *Batch) SyncFileSymbols(fileID, moduleID int64, syms []Symbol) (FileSymbols, error) {
	old, err := querySymbols(cached{b}, `WHERE s.file_id = ? ORDER BY s.id`, fileID)
	if err != nil {
		return FileSymbols{}, err
	}
	byKey := make(map[string]Symbol, len(old))
	for _, o := range old {
		byKey[unparkedKey(o.StableKey)] = o
	}
	res := FileSymbols{IDs: make(map[string]int64, len(syms))}
	kept := make(map[string]bool, len(syms))
	for _, sy := range syms {
		if _, ok := byKey[sy.StableKey]; ok {
			kept[sy.StableKey] = true
		}
	}
	if _, err := (cached{b}).Exec(`UPDATE symbol SET container_id = NULL WHERE file_id = ?`, fileID); err != nil {
		return FileSymbols{}, err
	}
	for k, o := range byKey {
		if !kept[k] {
			if _, err := (cached{b}).Exec(`DELETE FROM symbol WHERE id = ?`, o.ID); err != nil {
				return FileSymbols{}, err
			}
			res.Removed = append(res.Removed, o)
		}
	}
	return res, b.writeSymbols(fileID, moduleID, syms, byKey, &res)
}

// writeSymbols updates or inserts syms in order, linking each to the
// first symbol of the file with its Container's qualified name: at write
// time when that symbol came first, else afterwards.
func (b *Batch) writeSymbols(fileID, moduleID int64, syms []Symbol, byKey map[string]Symbol, res *FileSymbols) error {
	byQualified := make(map[string]int64, len(syms))
	var later []Symbol
	for _, sy := range syms {
		sy.ContainerID = 0
		if sy.Container != "" {
			sy.ContainerID = byQualified[sy.Container]
		}
		if err := b.syncSymbol(fileID, moduleID, sy, byKey, res); err != nil {
			return err
		}
		if _, dup := byQualified[sy.Qualified]; !dup {
			byQualified[sy.Qualified] = res.IDs[sy.StableKey]
		}
		if sy.Container != "" && sy.ContainerID == 0 {
			later = append(later, sy)
		}
	}
	for _, sy := range later {
		parent, ok := byQualified[sy.Container]
		if !ok {
			continue
		}
		if _, err := (cached{b}).Exec(`UPDATE symbol SET container_id = ? WHERE id = ?`, parent,
			res.IDs[sy.StableKey]); err != nil {
			return err
		}
	}
	return nil
}

// syncSymbol updates the row of sy's key or inserts sy, recording the
// outcome in res.
func (b *Batch) syncSymbol(fileID, moduleID int64, sy Symbol, byKey map[string]Symbol, res *FileSymbols) error {
	o, ok := byKey[sy.StableKey]
	if !ok {
		id, err := insertSymbol(cached{b}, fileID, moduleID, sy)
		res.IDs[sy.StableKey] = id
		res.Added = append(res.Added, sy)
		return err
	}
	res.IDs[sy.StableKey] = o.ID
	if o.Kind != sy.Kind || o.Signature != sy.Signature || o.BodyHash != sy.BodyHash || o.Value != sy.Value {
		res.Changed = append(res.Changed, [2]Symbol{o, sy})
	}
	if err := updateSymbol(cached{b}, o.ID, moduleID, sy); err != nil {
		return err
	}
	if o.StartLine == sy.StartLine {
		return nil
	}
	_, err := (cached{b}).Exec(`UPDATE edge SET line = ? WHERE src_symbol_id = ? AND reference_id IS NULL`, sy.StartLine, o.ID)
	return err
}

func updateSymbol(q querier, id, moduleID int64, sy Symbol) error {
	_, err := q.Exec(`UPDATE symbol SET module_id = ?, kind = ?, name = ?, qualified_name = ?, stable_key = ?,
    container_id = ?, start_line = ?, end_line = ?, start_byte = ?, end_byte = ?, sig_start_line = ?, sig_end_line = ?, signature = ?,
    visibility = ?, is_test = ?, body_hash = ?, doc_text = ?, doc_hash = ?, doc_body_hash = ?, doc_state = ?,
    doc_start_line = ?, doc_end_line = ?, value = ? WHERE id = ?`,
		nullID(moduleID), sy.Kind, sy.Name, sy.Qualified, sy.StableKey, nullID(sy.ContainerID),
		sy.StartLine, sy.EndLine, sy.StartByte, sy.EndByte, sy.SigStartLine, sy.SigEndLine, sy.Signature,
		sy.Visibility, sy.IsTest, sy.BodyHash, nullStr(sy.DocText), nullStr(sy.DocHash), nullStr(sy.DocBodyHash),
		defaultStr(sy.DocState, "missing"), nullID(int64(sy.DocStartLine)), nullID(int64(sy.DocEndLine)),
		nullStr(sy.Value), id)
	return err
}
