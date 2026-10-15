package extract

import "strconv"

// UniqueQualified makes the qualified names of one file's symbols unique,
// as the store's stable keys require: a symbol whose name was already
// taken by an earlier one, in source order, gets the first free suffix of
// ~2, ~3 and so on ("#" is reserved for signature hashes in stable keys). Extractors call it last, after their language-specific
// disambiguation.
//
// Overloads that differ in SigHash keep their shared qualified name: the
// stable key appends the hash, so only the pair must be unique.
func UniqueQualified(syms []Symbol) {
	seen := make(map[string]bool, len(syms))
	for i := range syms {
		q := syms[i].Qualified
		for n := 2; seen[q+"#"+syms[i].SigHash]; n++ {
			q = syms[i].Qualified + "~" + strconv.Itoa(n)
		}
		syms[i].Qualified = q
		seen[q+"#"+syms[i].SigHash] = true
	}
}
