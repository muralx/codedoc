package store

import (
	"path/filepath"
	"testing"
)

// failing lists calls that must fail; each returns the call's error.
type failing map[string]func() error

func (f failing) check(t *testing.T) {
	t.Helper()
	for name, call := range f {
		if call() == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func second[T any](_ T, err error) error { return err }

// Every reader and writer reports the database error instead of
// swallowing it once the database is closed.
func TestStoreMethodsFailOnClosedDatabase(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	failing{
		"SymbolCountsByFile":    func() error { return second(s.SymbolCountsByFile(1, "")) },
		"ModuleEdgesFrom":       func() error { return second(s.ModuleEdgesFrom(1)) },
		"Batch":                 func() error { return s.Batch(func(*Batch) error { return nil }) },
		"ReplaceRepoIdentities": func() error { return s.ReplaceRepoIdentities(1, nil) },
		"RepoIdentities":        func() error { return second(s.RepoIdentities(1)) },
		"ReposProviding":        func() error { return second(s.ReposProviding("go", "x")) },
		"DuplicateIdentities":   func() error { return second(s.DuplicateIdentities()) },
		"UpsertRepo":            func() error { return second(s.UpsertRepo(Repo{Workspace: "w", Name: "r", Path: "/r"})) },
		"RepoByPath":            func() error { return second(s.RepoByPath("/r")) },
		"UpdateRepoHead":        func() error { return s.UpdateRepoHead(1, "b", "c") },
		"SetRepoIndexedAt":      func() error { return s.SetRepoIndexedAt(1, 1) },
		"RepoStats":             func() error { _, _, err := s.RepoStats(1); return err },
		"UpsertFile":            func() error { return second(s.UpsertFile(File{RepoID: 1, Path: "a"})) },
		"FilesByRepo":           func() error { return second(s.FilesByRepo(1)) },
		"FileByPath":            func() error { return second(s.FileByPath(1, "a")) },
		"DeleteFile":            func() error { return s.DeleteFile(1) },
		"SchemaVersion":         func() error { return second(s.SchemaVersion()) },
		"migrate":               func() error { return s.migrate() },
		"FileImports":           func() error { return second(s.FileImports(1)) },
		"UpsertModule":          func() error { return second(s.UpsertModule(Module{RepoID: 1})) },
		"ModulesByRepo":         func() error { return second(s.ModulesByRepo(1)) },
		"ReplaceFileSymbols":    func() error { return second(s.ReplaceFileSymbols(1, 0, nil)) },
		"SymbolsByName":         func() error { return second(s.SymbolsByName("x", "", 0)) },
		"SymbolsInFile":         func() error { return second(s.SymbolsInFile(1)) },
	}.check(t)
}

// The graph readers fail on a closed database too.
func TestGraphReadsFailOnClosedDatabase(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	failing{
		"UnresolvedReferences": func() error { return second(s.UnresolvedReferences(1)) },
		"EdgesTo":              func() error { return second(s.EdgesTo(1, "calls")) },
		"EdgesFrom":            func() error { return second(s.EdgesFrom(1, "calls")) },
		"EdgesByID":            func() error { return second(s.EdgesByID([]int64{1})) },
		"EdgeCounts":           func() error { _, _, err := s.EdgeCounts(1); return err },
		"candidates":           func() error { return second(s.candidates([]int64{1})) },
		"Walk":                 func() error { return second(s.Walk(1, WalkOpts{Kinds: []string{"calls"}})) },
		"ModuleWalk":           func() error { return second(s.ModuleWalk(1, ModuleWalkOpts{})) },
		"ModuleEdgesTo":        func() error { return second(s.ModuleEdgesTo(1)) },
		"ModulesByIdentity":    func() error { return second(s.ModulesByIdentity("x")) },
	}.check(t)
}

// A batch whose transaction is over fails every call.
func TestBatchMethodsFailOnFinishedTransaction(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	b := &Batch{tx: tx}
	edge := []ModuleEdge{{Src: 1, Dst: 2, Kind: "imports", Confidence: "import"}}
	failing{
		"ReplaceModuleEdges":     func() error { return b.ReplaceModuleEdges(nil) },
		"ReplaceModuleEdgesFrom": func() error { return b.ReplaceModuleEdgesFrom([]int64{1}, nil) },
		"insertModuleEdges":      func() error { return b.insertModuleEdges(edge) },
		"UpsertFile":             func() error { return second(b.UpsertFile(File{RepoID: 1, Path: "a"})) },
		"DeleteFiles":            func() error { return second(b.DeleteFiles([]int64{1})) },
		"KeyExists":              func() error { return second(b.KeyExists("k")) },
		"ReplaceRepoIdentities":  func() error { return b.ReplaceRepoIdentities(1, nil) },
		"ReposProviding":         func() error { return second(b.ReposProviding("go", "x")) },
		"Providers":              func() error { return second(b.Providers()) },
		"ReplaceFileImports":     func() error { return b.ReplaceFileImports(1, nil) },
		"SetImportModule":        func() error { return b.SetImportModule(1, 0) },
		"Imports":                func() error { return second(b.Imports(nil)) },
		"Imports scoped":         func() error { return second(b.Imports([]int64{1})) },
		"ImportsInDir":           func() error { return second(b.ImportsInDir(1, "a")) },
		"ImportsAtPath":          func() error { return second(b.ImportsAtPath(1, "a")) },
		"ReposWithModule":        func() error { return second(b.ReposWithModule("py", "a")) },
		"UpsertModule":           func() error { return second(b.UpsertModule(Module{RepoID: 1})) },
		"DeleteModules":          func() error { return b.DeleteModules([]int64{1}) },
		"ModuleByIdentity":       func() error { return second(b.ModuleByIdentity(1, "go", "x")) },
		"ModuleByPath":           func() error { return second(b.ModuleByPath(1, "go", ".")) },
		"Modules":                func() error { return second(b.Modules()) },
		"AnyFile":                func() error { return second(b.AnyFile(1, []string{"a"})) },
		"ReplaceFileSymbols":     func() error { return second(b.ReplaceFileSymbols(1, 0, nil)) },
		"SetStableKey":           func() error { return b.SetStableKey(1, "k") },
	}.check(t)
}

// finishedBatch returns a batch whose transaction was rolled back.
func finishedBatch(t *testing.T) *Batch {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	return &Batch{tx: tx}
}

// The reference and edge writers of a finished batch fail too.
func TestBatchGraphMethodsFailOnFinishedTransaction(t *testing.T) {
	b := finishedBatch(t)
	failing{
		"ReplaceFileReferences":   func() error { return b.ReplaceFileReferences(1, nil) },
		"References":              func() error { return second(b.References(nil, nil)) },
		"References by file":      func() error { return second(b.References([]int64{1}, nil)) },
		"References by name":      func() error { return second(b.References(nil, []string{"x"})) },
		"SetReferenceCandidates":  func() error { return b.SetReferenceCandidates(1, 0) },
		"InsertEdge":              func() error { return b.InsertEdge(Edge{Src: 1}) },
		"DeleteHeuristicEdges":    func() error { return b.DeleteHeuristicEdges() },
		"PreciseReferences":       func() error { return second(b.PreciseReferences(nil)) },
		"PreciseReferences ids":   func() error { return second(b.PreciseReferences([]int64{1})) },
		"PreciseImplements":       func() error { return second(b.PreciseImplements()) },
		"DropPreciseDetected":     func() error { return second(b.DropPreciseDetected(1)) },
		"ReplaceSCIPSymbols":      func() error { return b.ReplaceSCIPSymbols([]string{"r:"}, nil) },
		"ReplaceSCIPSymbols rows": func() error { return b.ReplaceSCIPSymbols(nil, []SCIPSymbol{{StableKey: "k"}}) },
		"InsertReference":         func() error { return second(b.InsertReference(Reference{FileID: 1})) },
		"ReplaceReferenceEdges":   func() error { return b.ReplaceReferenceEdges(Edge{ReferenceID: 1}) },
		"SetPreciseImplements":    func() error { return b.SetPreciseImplements(1, 2, 1, 1) },
		"SetSCIPCommit":           func() error { return b.SetSCIPCommit(1, "c") },
		"DeleteReferenceEdges":    func() error { return b.DeleteReferenceEdges([]int64{1}) },
		"DeleteDetectedEdges":     func() error { return b.DeleteDetectedEdges("implements", []int64{1}) },
		"Targets":                 func() error { return second(b.Targets(nil)) },
		"Targets by name":         func() error { return second(b.Targets([]string{"x"})) },
		"TargetsOfKinds":          func() error { return second(b.TargetsOfKinds("go", []string{"struct"}, nil)) },
		"TargetsOfKinds scoped":   func() error { return second(b.TargetsOfKinds("go", []string{"struct"}, []int64{1})) },
		"ModuleEdges":             func() error { return second(b.ModuleEdges()) },
		"ReplaceFileConfig":       func() error { return b.ReplaceFileConfig(1, 1, "env", nil, nil) },
		"ClearFileConfig":         func() error { return second(b.ClearFileConfig(1)) },
		"DeleteOrphanConfigKeys":  func() error { return b.DeleteOrphanConfigKeys(1) },
		"DefinedConfigKeys":       func() error { return second(b.DefinedConfigKeys(nil)) },
		"DefinedConfigKeys named": func() error { return second(b.DefinedConfigKeys([]string{"x"})) },
		"InsertConfigRead":        func() error { return b.InsertConfigRead(ConfigRead{}) },
		"DeleteConfigReads":       func() error { return b.DeleteConfigReads(nil) },
		"DeleteConfigReads ids":   func() error { return b.DeleteConfigReads([]int64{1}) },
	}.check(t)
}

// Statements that fail after the first one roll the batch back.
func TestBatchWritesFailOnConstraintViolations(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	inTx := func(fn func(b *Batch) error) func() error { return func() error { return s.Batch(fn) } }
	failing{
		"edge to missing module": inTx(func(b *Batch) error {
			return b.ReplaceModuleEdges([]ModuleEdge{{Src: 98, Dst: 99, Kind: "imports", Confidence: "import"}})
		}),
		"import of missing file": inTx(func(b *Batch) error {
			return b.ReplaceFileImports(99, []Import{{Raw: "x", Names: []string{"a"}, Line: 1}})
		}),
		"edge from missing symbol": inTx(func(b *Batch) error {
			return b.InsertEdge(Edge{Src: 98, Dst: 99, Kind: "calls", RefKind: "call", Confidence: "local", FileID: 1})
		}),
		"reference of missing file": inTx(func(b *Batch) error {
			return b.ReplaceFileReferences(99, []Reference{{Kind: "call", Name: "x"}})
		}),
		"config of missing file": inTx(func(b *Batch) error {
			return b.ReplaceFileConfig(99, 99, "env", []ConfigEntry{{Key: "K", Line: 1}}, nil)
		}),
		"identity of missing repo": inTx(func(b *Batch) error {
			return b.ReplaceRepoIdentities(99, []RepoIdentity{{Lang: "go", Identity: "x", Path: "go.mod"}})
		}),
	}.check(t)
}
