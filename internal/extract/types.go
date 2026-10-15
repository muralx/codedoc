// Package extract defines the records every language extractor produces.
package extract

// Kind classifies a symbol.
type Kind string

// Symbol kinds. Containers (class, struct, interface) nest other symbols.
const (
	KindFunction  Kind = "function"
	KindMethod    Kind = "method"
	KindClass     Kind = "class"
	KindStruct    Kind = "struct"
	KindInterface Kind = "interface"
	KindTrait     Kind = "trait"
	KindEnum      Kind = "enum"
	KindTypeAlias Kind = "type"
	KindConst     Kind = "const"
	KindVar       Kind = "var"
	KindField     Kind = "field"
)

// Document kinds (documents spec section 2): a document is one file of
// prose, a section one heading with the text below it.
const (
	KindDocument Kind = "document"
	KindSection  Kind = "section"
)

// IsDocKind reports whether kind is a document or a section.
func IsDocKind[K ~string](kind K) bool {
	return Kind(kind) == KindDocument || Kind(kind) == KindSection
}

// Visibility is the language's notion of exported or public.
type Visibility string

// Visibility values.
const (
	Public  Visibility = "public"
	Package Visibility = "package"
	Private Visibility = "private"
)

// Span is a half-open byte range with 1-based inclusive line numbers.
type Span struct {
	StartByte, EndByte uint
	StartLine, EndLine int
}

// Symbol is one declaration.
type Symbol struct {
	Kind       Kind
	Name       string
	Qualified  string // dot path inside the module; TS/JS prefix the file path relative to the package root (spec 5.1)
	Container  string // Qualified of the enclosing symbol, "" at top level
	Span       Span   // whole declaration including body
	Signature  Span   // the signature lines only
	SigText    string // signature text, single line, whitespace-collapsed
	Visibility Visibility
	IsTest     bool
	BodyHash   string // xxhash of the signature and body text, whitespace removed outside literals (doc excluded; see CodeHash)
	// SigHash is set by extractors for overload-capable kinds (Java methods
	// and constructors): the first four hex digits of the hash of the
	// normalized parameter type list (spec 5.1). Empty otherwise.
	SigHash string `json:",omitempty"`
	Doc     *Doc   // nil when undocumented
	// GroupDoc is set on an undocumented member of a declaration group
	// that has a doc of its own (Go: "// Symbol kinds." above const
	// ( ... )): the group's doc documents it.
	GroupDoc bool `json:",omitempty"`
	// Value is the string a constant is initialized with (Go, TS and
	// Python constants, Java static final or final fields) when its
	// initializer is a plain string literal; empty otherwise. It lets a
	// config accessor that reads a key through a constant name the key.
	Value string `json:",omitempty"`
}

// Doc is the documentation attached to a symbol or module.
type Doc struct {
	Text string // comment markers stripped, paragraphs preserved
	Span Span
}

// RefKind classifies a reference.
type RefKind string

// Reference kinds.
const (
	RefCall        RefKind = "call"
	RefType        RefKind = "type"
	RefInstantiate RefKind = "instantiate"
	RefField       RefKind = "field"
	RefExtend      RefKind = "extend"
	RefImplement   RefKind = "implement"
	RefConfig      RefKind = "config" // candidate config key literal
	// RefLink is a relative link of a document: Name is the target as
	// written without its anchor, Qualifier the anchor.
	RefLink RefKind = "link"
)

// Reference is a use of a name at a position.
type Reference struct {
	Kind      RefKind
	Name      string // the identifier as written, e.g. "Lookup"
	Qualifier string // receiver or package text before the name, e.g. "svc.s" or "store"
	Enclosing string // Qualified of the enclosing symbol, "" at file level
	Line      int
	// Col is the 0-based byte column of the name in its line, which
	// tells apart references of one name on one line.
	Col   int
	Arity int // call argument count, -1 when not a call
	// RecvType is the type of the variable the qualifier starts with,
	// when its declaration in the enclosing symbol makes it evident
	// (see Locals): a type name as written (Server, kit.Client), or a
	// call whose first result it is (kit.New()), or BuiltinType. Empty
	// otherwise.
	RecvType string `json:",omitempty"`
}

// Import is one import statement.
type Import struct {
	Raw   string   // module path or specifier as written
	Alias string   // local name, "" when none
	Names []string // named imports for TS/JS and Python (aliases as "x=y"), nil for Go and Java
	Line  int
	// Reexport marks a TS/JS export ... from 'source': the names (all of
	// them when Names is nil and Alias empty) are exported again.
	Reexport bool `json:",omitempty"`
}

// FileResult is everything extracted from one file.
type FileResult struct {
	Symbols     []Symbol
	References  []Reference
	Imports     []Import
	ModuleDoc   *Doc   // package or module level doc found in this file
	ModuleName  string // Go package name or Java package; "" for other languages
	ParseErrors int
}
