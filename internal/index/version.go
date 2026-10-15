package index

// extractorVersion names the semantics of the extracted rows, body
// hashes above all. Bump it when a change to an extractor makes rows of
// the previous version incomparable: the next sync of every repo then
// re-extracts every file and seeds doc states from blame again, without a
// schema migration. Version 2 hashes containers over their declarations
// only (spec 7.5); version 3 records the column of each reference;
// version 4 marks config candidates read through an accessor or only a
// key-like literal (Qualifier); version 5 records accessors whose key
// argument is a constant name and the string values of constants;
// version 6 keeps the whitespace inside string and character literals in
// body hashes (see extract.CodeHash); version 7 extracts documents and
// their sections and records file titles (documents spec); version 8
// records the type of the local variable a reference goes through
// (extract.Reference.RecvType); version 9 scopes variables lexically,
// marks those of unknown type (extract.UnknownType), records
// module-level variables and the aliases of TypeScript named imports,
// and keeps Markdown headings inside HTML comments out of the sections;
// version 10 records type paths through declared field types and method
// results (extract.FieldOf), the element type of a loop variable, the
// constructor parameter properties of TypeScript classes, and the
// members of TypeScript object type aliases as symbols.
const extractorVersion = "10"

// ExtractorVersion returns the version of the extracted rows' semantics
// this build writes (see extractorVersion).
func ExtractorVersion() string { return extractorVersion }

// checkVersion finds the stored rows extracted with another extractor
// version (see store.File.ExtractedVersion). They are made to look never
// hashed, so the sync re-extracts them, and reseed tells docStates to
// ignore their old body hashes. Each file records the version it was
// extracted with, so a catch-up that stops part way (see filesChunked)
// keeps the files it re-extracted.
func (st *state) checkVersion() {
	for i := range st.rows {
		if f := &st.rows[i]; f.ExtractedVersion != extractorVersion {
			if st.reseed == nil {
				st.reseed = map[string]bool{}
			}
			st.reseed[f.Path] = true
			f.Hash, f.Mtime = "", 0
		}
	}
}
