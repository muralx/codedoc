package index

import (
	"sort"
	"strconv"

	"github.com/muralx/codedoc/internal/stablekey"
	"github.com/muralx/codedoc/internal/store"
)

// Stable key collisions (spec 5.1). A sync applies its change set in
// order: rows of gone files are deleted, the symbols of every changed
// file are cleared, then the changed files are written. So when a file is
// written, every other symbol row is current, and a key used elsewhere in
// the module is a real collision, not a stale row of a file renamed or
// edited in the same change set.

// AssignKeys gives a written symbol its plain key unless another file of
// the module, or this file again, declares the same qualified name; then
// every colliding symbol gets the path hash suffix (the other file's
// symbol too, when it still carries the plain key).
//
// A key that carries a signature hash (Java methods and constructors,
// see hasSigHash) collides only with the same qualified name and
// signature hash, and its collision suffix follows the signature hash:
// Q#sig#path. The signature hash is part of its base, so a field X.m and
// a method X.m#sig never collide, and restoring a lone key keeps the
// signature hash.
//
// After the writes, restoreLoneKeys revisits every key base whose rows
// were deleted or cleared: when a single symbol is left with that base and
// it carries a hash, it gets the plain key back. That symbol is alone, so
// nothing else uses the plain key. A collision that disappears thus
// restores the plain key, and one that persists keeps the hashed keys.
func AssignKeys(b *store.Batch, fileID int64, filePath string, syms []store.Symbol) error {
	count := make(map[string]int, len(syms))
	for _, sy := range syms {
		count[sy.StableKey]++
	}
	taken := make(map[string]bool, len(syms))
	used := func(k string) (bool, error) {
		if taken[k] {
			return true, nil
		}
		return b.KeyExists(k)
	}
	byBase, err := sameBases(b, syms, fileID)
	if err != nil {
		return err
	}
	seen := make(map[string]int, len(syms))
	for i := range syms {
		base := syms[i].StableKey
		others := byBase[base]
		if len(others) == 0 && count[base] == 1 {
			taken[base] = true
			continue
		}
		if err := hashPlainOthers(b, base, others, used); err != nil {
			return err
		}
		input := filePath
		if n := seen[base]; n > 0 {
			input += "#" + strconv.Itoa(n)
		}
		seen[base]++
		var err error
		if syms[i].StableKey, err = pickHashedKey(base, input, used); err != nil {
			return err
		}
		taken[syms[i].StableKey] = true
	}
	return nil
}

// hashPlainOthers moves the symbols of others that still carry the plain
// base key to a hashed key of their own path.
func hashPlainOthers(b *store.Batch, base string, others []store.Symbol, used func(string) (bool, error)) error {
	for i, o := range others {
		if o.StableKey != base {
			continue
		}
		k, err := pickHashedKey(base, o.Path, used)
		if err != nil {
			return err
		}
		if err := b.SetStableKey(o.ID, k); err != nil {
			return err
		}
		others[i].StableKey = k
	}
	return nil
}

// sameBases returns, by key base, the symbols of files other than fileID
// whose key is the key of one of syms or that key with a hash suffix,
// read in few queries.
func sameBases(b *store.Batch, syms []store.Symbol, fileID int64) (map[string][]store.Symbol, error) {
	bases := make([]string, len(syms))
	for i, sy := range syms {
		bases[i] = sy.StableKey
	}
	return byBases(b, bases, fileID)
}

// byBases returns, by key base, the symbols of files other than fileID
// whose key has one of bases as its base, read in few queries.
func byBases(b *store.Batch, bases []string, fileID int64) (map[string][]store.Symbol, error) {
	want := make(map[string]bool, len(bases))
	for _, base := range bases {
		want[base] = true
	}
	rows, err := b.SymbolsByKeyBases(bases, fileID)
	out := map[string][]store.Symbol{}
	for _, r := range rows {
		if base := stablekey.Base(r.StableKey, hasSigHash(r.Lang, r.Kind)); want[base] {
			out[base] = append(out[base], r)
		}
	}
	return out, err
}

// pickHashedKey returns base#hash for the first suffix not in use: the
// first four hex digits of the hash of input, else eight, else eight
// followed by -2, -3 and so on.
func pickHashedKey(base, input string, used func(string) (bool, error)) (string, error) {
	h := pathHash(input)
	for n := 0; ; n++ {
		var k string
		switch n {
		case 0:
			k = base + "#" + h[:4]
		case 1:
			k = base + "#" + h[:8]
		default:
			k = base + "#" + h[:8] + "-" + strconv.Itoa(n)
		}
		taken, err := used(k)
		if err != nil || !taken {
			return k, err
		}
	}
}

// restoreLoneKeys gives the plain key back to a hashed symbol that is
// the only one left with its base; see AssignKeys.
func restoreLoneKeys(b *store.Batch, bases map[string]bool) error {
	sorted := make([]string, 0, len(bases))
	for k := range bases {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for lo := 0; lo < len(sorted); lo += batchSize {
		chunk := sorted[lo:min(lo+batchSize, len(sorted))]
		found, err := byBases(b, chunk, 0)
		if err != nil {
			return err
		}
		for _, base := range chunk {
			if rows := found[base]; len(rows) == 1 && rows[0].StableKey != base {
				if err := b.SetStableKey(rows[0].ID, base); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
