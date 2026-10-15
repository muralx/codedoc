// Package write places documentation into source files (spec section 10):
// per-language formatting and placement, verification by re-extraction,
// restore on failure, and a per-run journal of original files.
package write

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/muralx/codedoc/internal/extract"
	// The extractors register themselves; verification re-extracts files.
	_ "github.com/muralx/codedoc/internal/extract/golang"
	_ "github.com/muralx/codedoc/internal/extract/java"
	_ "github.com/muralx/codedoc/internal/extract/python"
	_ "github.com/muralx/codedoc/internal/extract/ts"
)

// ErrGenerated reports a refusal to write into a generated file.
var ErrGenerated = errors.New("write: refusing to document a generated file")

// Target identifies what to document.
type Target struct {
	Root string // absolute repo root
	// Path is the repo-relative file of the symbol, or the module
	// directory for a module doc.
	Path string
	// Lang is the precise language: go, typescript, tsx, javascript,
	// python or java.
	Lang string
	// Qualified is the symbol's qualified name as the extractor reports
	// it; "" for a module doc.
	Qualified string
	// SigHash tells overloads apart (Java); "" otherwise.
	SigHash string
	// ModuleDir is the repo-relative module directory; set for module docs.
	// For a Python file module it names the .py file.
	ModuleDir string
	// ExtractPath is the path the extractor sees (for TS/JS, relative to
	// the package root); it defaults to Path.
	ExtractPath string
	// Generated marks a generated file, which is never written.
	Generated bool
}

// isModule reports whether t names a module doc.
func (t Target) isModule() bool { return t.ModuleDir != "" }

// extractPath returns the path qualified names are built from.
func (t Target) extractPath() string {
	if t.ExtractPath != "" {
		return t.ExtractPath
	}
	return t.Path
}

// Edit is a planned change to one file.
type Edit struct {
	Path    string // repo-relative file to write
	Existed bool   // false when the file is created
	Before  []byte // current content; nil when the file is created
	After   []byte // content to write
	Want    string // the doc text the file carries afterwards
	// Others lists the other files that carry a module doc of the same
	// module (a Go package comment outside doc.go); they are left alone.
	Others []string
}

// Result describes a doc written by Apply.
type Result struct {
	Path    string      // repo-relative file written
	Created bool        // the file did not exist before
	Doc     extract.Doc // the doc as re-extracted from the written file
	Others  []string    // see Edit.Others
}

// language places docs for one language.
type language interface {
	// symbolDoc returns src with text as the doc of sym, and the doc
	// text the result must carry.
	symbolDoc(src []byte, sym extract.Symbol, text string) ([]byte, string, error)
	// moduleDoc plans the module doc of t.
	moduleDoc(t Target, text string) (Edit, error)
	// readModuleDoc returns the module doc carried by src, the content of
	// the module doc file path, or nil.
	readModuleDoc(path string, src []byte) (*extract.Doc, int, error)
}

var languages = map[string]language{
	"go": goLang{}, "typescript": tsLang{lang: "typescript"}, "tsx": tsLang{lang: "tsx"},
	"javascript": tsLang{lang: "javascript"}, "python": pyLang{}, "java": javaLang{},
}

// planner plans an edit: Plan, or in tests one that injects broken edits.
type planner func(Target, string) (Edit, error)

// Plan computes the edit that documents t with text. It never writes.
// A file whose lines mostly end in CRLF is planned on its LF form and
// written back with CRLF line ends throughout.
func Plan(t Target, text string) (Edit, error) {
	e, err := planLF(t, text)
	if err != nil || !e.Existed {
		return e, err
	}
	orig, err := os.ReadFile(filepath.Join(t.Root, e.Path))
	if err != nil {
		return Edit{}, err
	}
	if _, crlf := toLF(orig); crlf {
		e.Before, e.After = orig, toCRLF(e.After)
	}
	return e, nil
}

// planLF is Plan over sources read through readSource.
func planLF(t Target, text string) (Edit, error) {
	if t.Generated {
		return Edit{}, ErrGenerated
	}
	lang, ok := languages[t.Lang]
	if !ok {
		return Edit{}, fmt.Errorf("write: unsupported language %q", t.Lang)
	}
	text = cleanText(text)
	if text == "" {
		return Edit{}, errors.New("write: empty doc text")
	}
	if t.isModule() {
		return lang.moduleDoc(t, text)
	}
	src, err := readSource(filepath.Join(t.Root, t.Path))
	if err != nil {
		return Edit{}, err
	}
	sym, _, err := findSymbol(t, src)
	if err != nil {
		return Edit{}, err
	}
	out, want, err := lang.symbolDoc(src, sym, text)
	if err != nil {
		return Edit{}, err
	}
	return Edit{Path: t.Path, Existed: true, Before: src, After: out, Want: want}, nil
}

// Apply plans the edit, journals the original file when CODEDOC_RUN_ID is
// set, writes the result, re-extracts it and verifies that the target now
// carries exactly the planned doc and that the file parses no worse than
// before. On verification failure it restores the original bytes (or
// removes a created file) unless the file changed since Apply wrote it,
// and returns an error. It holds the file's lock (see lockFile) from
// planning to its last write, and every write is atomic (see writeFile).
func Apply(t Target, text string) (Result, error) {
	return apply(Plan, t, text)
}

// apply is Apply with the edit planned by plan.
func apply(plan planner, t Target, text string) (res Result, err error) {
	e, unlock, err := planLocked(plan, t, text)
	if err != nil {
		return Result{}, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return applyPlanned(t, e)
}

// planAttempts bounds re-planning a module doc whose file moved.
const planAttempts = 3

// planLocked locks the file the edit writes and plans the edit under
// the lock. A symbol's doc goes into its own file; a module doc's file
// is known only from a plan, so it is planned again under the lock and
// retried should the file have changed meanwhile.
func planLocked(plan planner, t Target, text string) (Edit, func() error, error) {
	path := t.Path
	for range planAttempts {
		if t.isModule() {
			e, err := plan(t, text)
			if err != nil {
				return Edit{}, nil, err
			}
			path = e.Path
		}
		unlock, err := lockFile(filepath.Join(t.Root, path))
		if err != nil {
			return Edit{}, nil, err
		}
		e, err := plan(t, text)
		if err == nil && e.Path == path {
			return e, unlock, nil
		}
		if err := errors.Join(err, unlock()); err != nil {
			return Edit{}, nil, err
		}
	}
	return Edit{}, nil, errors.New("write: the module doc file kept changing while planning")
}

// applyPlanned journals, writes and verifies e, under its file's lock.
func applyPlanned(t Target, e Edit) (Result, error) {
	abs := filepath.Join(t.Root, e.Path)
	hook("planned", abs)
	id := os.Getenv(RunIDEnv)
	if id != "" {
		if err := journalFile(id, t.Root, e.Path); err != nil {
			return Result{}, err
		}
	}
	if err := writeFile(abs, e.After); err != nil {
		return Result{}, err
	}
	hook("written", abs)
	doc, err := verify(t, e)
	if err != nil {
		err = errors.Join(err, undo(abs, e))
		return Result{}, errors.Join(err, record(id, t.Root, e.Path, e.Before, !e.Existed))
	}
	if err := record(id, t.Root, e.Path, e.After, false); err != nil {
		return Result{}, err
	}
	return Result{Path: e.Path, Created: !e.Existed, Doc: doc, Others: e.Others}, nil
}

// verify re-reads the written file (in its LF form, see readSource)
// and checks the doc and parse errors.
func verify(t Target, e Edit) (extract.Doc, error) {
	after, err := readSource(filepath.Join(t.Root, e.Path))
	if err != nil {
		return extract.Doc{}, err
	}
	doc, errsAfter, err := docAt(t, e.Path, after)
	if err != nil {
		return extract.Doc{}, fmt.Errorf("write: verification failed: %w", err)
	}
	errsBefore := 0
	if e.Existed {
		_, errsBefore, _ = docAt(t, e.Path, e.Before)
	}
	switch {
	case errsAfter > errsBefore:
		return extract.Doc{}, fmt.Errorf("write: verification failed: %s has %d parse errors, had %d", e.Path, errsAfter, errsBefore)
	case doc == nil:
		return extract.Doc{}, errors.New("write: verification failed: no doc found after writing")
	case normalize(doc.Text) != normalize(e.Want):
		return extract.Doc{}, fmt.Errorf("write: verification failed: doc reads %q, want %q", doc.Text, e.Want)
	}
	return *doc, nil
}

// docAt returns the doc of t found in src (the content of path) and the
// number of parse errors in src.
func docAt(t Target, path string, src []byte) (*extract.Doc, int, error) {
	if t.isModule() {
		return languages[t.Lang].readModuleDoc(path, src)
	}
	sym, res, err := findSymbol(t, src)
	if err != nil {
		return nil, 0, err
	}
	return sym.Doc, res.ParseErrors, nil
}

// record notes in the journal of run id, when there is one, what Apply
// left in the file.
func record(id, root, path string, data []byte, absent bool) error {
	if id == "" {
		return nil
	}
	return recordWrite(id, root, path, data, absent)
}

// undo restores the state before an edit, unless the file no longer
// holds what the edit wrote: someone changed it since, and their
// content is left as it is.
func undo(abs string, e Edit) error {
	cur, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	if !bytes.Equal(cur, e.After) {
		return fmt.Errorf("write: %s changed after codedoc wrote it; left as it is", abs)
	}
	if !e.Existed {
		return os.Remove(abs)
	}
	return writeFile(abs, e.Before)
}
