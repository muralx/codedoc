package resolve

import (
	"regexp"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
)

// Declared types as signatures write them. A field signature is its
// declaration up to the end of its first line; a method signature runs
// to its body.
var (
	// tsField captures what follows the name of a TypeScript field or
	// property signature: ": T", "?: T = v;" or "= new T()".
	tsField = regexp.MustCompile(`^(?:[a-z]+\s+)*#?[\w$]+[?!]?\s*([:=].*)$`)
	// tsInit ends a declared type where an initializer starts (not =>).
	tsInit = regexp.MustCompile(`\s=[^>]`)
	// tsNew captures the class an initializer constructs.
	tsNew = regexp.MustCompile(`^=\s*new\s+([A-Za-z_$][\w$.]*)\s*[(<;]?`)
	// tsGetter matches the signature of a get accessor.
	tsGetter = regexp.MustCompile(`^(?:[a-z]+\s+)*get\s+[\w$#]+\s*\(`)
	// tsTypeName captures a plain named type without its arguments.
	tsTypeName = regexp.MustCompile(`^([A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*)\s*(?:<.*>)?$`)
	// tsCollection captures the element type of a generic collection.
	tsCollection = regexp.MustCompile(`^(?:Array|ReadonlyArray|Set|ReadonlySet|Iterable)<(.*)>$`)
	// tsPromise captures what a promise resolves to.
	tsPromise = regexp.MustCompile(`^Promise<(.*)>$`)
	// goResultName matches the name of a named result.
	goResultName = regexp.MustCompile(`^[A-Za-z_]\w*\s+`)
)

// tsOpaque are the TypeScript types that say nothing of the members of
// a value: what is reached through one is as unknown as before.
var tsOpaque = map[string]bool{"any": true, "unknown": true, "object": true, "never": true, "void": true}

// goTypeWords are the keywords a Go type may start with, which a named
// result's name must not be taken for.
var goTypeWords = map[string]bool{"chan": true, "map": true, "func": true, "struct": true, "interface": true}

// declaredText returns the type a member's signature declares, as
// written: the type of a field (step extract.StepField; in TypeScript
// also the class its initializer constructs and the result of a get
// accessor) or the first result of a method (extract.StepResult). It is
// empty when the signature declares none or the member is of another
// kind.
func declaredText(fam string, step extract.TypeStepKind, kind, sig string) string {
	sig = strings.TrimSpace(sig)
	if kind == "method" {
		switch {
		case fam == "go" && step == extract.StepResult:
			return goResultText(sig)
		case fam == "ts" && (step == extract.StepResult || tsGetter.MatchString(sig)):
			return tsResultText(sig)
		}
		return ""
	}
	if kind != "field" || step != extract.StepField {
		return ""
	}
	switch fam {
	case "go":
		return strings.TrimSpace(goFieldNames.ReplaceAllString(sig, ""))
	case "ts":
		return tsFieldText(sig)
	case "java":
		return javaFieldText(sig)
	}
	return ""
}

// javaFieldText returns the type a Java field signature declares, type
// arguments dropped (Store from "private final Store store").
func javaFieldText(sig string) string {
	decl, _, _ := strings.Cut(sig, "=")
	words := strings.Fields(stripGenerics(decl))
	if len(words) < 2 {
		return ""
	}
	return words[len(words)-2]
}

// tsFieldText returns the type of a TypeScript field: its annotation,
// else the class its initializer constructs.
func tsFieldText(sig string) string {
	m := tsField.FindStringSubmatch(sig)
	if m == nil {
		return ""
	}
	rest := m[1]
	if n := tsNew.FindStringSubmatch(rest); n != nil {
		return n[1]
	}
	if !strings.HasPrefix(rest, ":") {
		return ""
	}
	if at := tsInit.FindStringIndex(rest); at != nil {
		rest = rest[:at[0]]
	}
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(rest[1:]), ";,"))
}

// tsResultText returns the result type a TypeScript function or method
// signature declares after its parameter list, "" when it declares
// none.
func tsResultText(sig string) string {
	open := strings.Index(sig, "(")
	if open < 0 {
		return ""
	}
	end := closing(sig[open:])
	if end < 0 {
		return ""
	}
	rest, ok := strings.CutPrefix(strings.TrimSpace(sig[open+end+1:]), ":")
	if !ok {
		return ""
	}
	rest = strings.TrimSuffix(strings.TrimSpace(rest), "=>") // const f = (a: A): B => ...
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(rest), ";{"))
}

// goResultText returns the type of the first result a Go function or
// method signature declares, as written and without the name of a named
// result ("*Client" from "func New(name string) *Client", "[]Item" from
// "func (s *Store) All() (items []Item, err error)"), "" when there is
// none.
func goResultText(sig string) string {
	rest := strings.TrimSpace(strings.TrimPrefix(sig, "func"))
	if strings.HasPrefix(rest, "(") { // the receiver
		end := closing(rest)
		if end < 0 {
			return ""
		}
		rest = rest[end+1:]
	}
	open := strings.Index(rest, "(")
	if open < 0 {
		return ""
	}
	end := closing(rest[open:])
	if end < 0 {
		return ""
	}
	results := strings.TrimSpace(rest[open+end+1:])
	if strings.HasPrefix(results, "(") {
		if end = closing(results); end < 0 {
			return ""
		}
		results, _, _ = strings.Cut(results[1:end], ",")
		results = strings.TrimSpace(results)
		if name := goResultName.FindString(results); name != "" && !goTypeWords[strings.TrimSpace(name)] {
			results = results[len(name):]
		}
	}
	return strings.TrimSpace(results)
}

// typeNameOf returns the plain named type a declared type stands for
// (Store from "*Store", "Store<Item>" or "Store | undefined"), "" for
// any other type: a slice, a function, a union, and in TypeScript the
// types that say nothing of a value's members (see tsOpaque).
func typeNameOf(fam, text string) string {
	switch fam {
	case "go":
		if m := goTypeName.FindStringSubmatch(text); m != nil {
			return m[1]
		}
	case "java":
		if javaTypeName.MatchString(text) {
			return text
		}
	case "ts":
		if m := tsTypeName.FindStringSubmatch(tsDefined(text)); m != nil && !tsOpaque[m[1]] {
			return m[1]
		}
	}
	return ""
}

// tsDefined drops what makes a TypeScript type optional or read-only:
// "| undefined", "| null" and a leading readonly. A union of several
// other types is returned as written.
func tsDefined(text string) string {
	var parts []string
	for _, p := range splitTop(text, '|') {
		if p = strings.TrimSpace(p); p != "undefined" && p != "null" && p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) != 1 {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(strings.TrimPrefix(parts[0], "readonly "))
}

// splitTop splits s at the separators that lie outside brackets.
func splitTop(s string, sep byte) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case strings.IndexByte("<([{", c) >= 0:
			depth++
		case strings.IndexByte(")]}", c) >= 0, c == '>' && (i == 0 || s[i-1] != '='):
			depth--
		case c == sep && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// elemText returns the element type of a declared array, slice, map or
// collection type ("Item" from "[]Item", "map[string]Item", "Item[]"
// and "Array<Item>"), "" for any other type.
func elemText(fam, text string) string {
	switch fam {
	case "go":
		text = strings.TrimPrefix(strings.TrimSpace(text), "map")
		if !strings.HasPrefix(text, "[") {
			return ""
		}
		if end := closing(text); end >= 0 {
			return strings.TrimSpace(text[end+1:])
		}
	case "ts":
		text = tsDefined(text)
		if m := tsCollection.FindStringSubmatch(text); m != nil && len(splitTop(m[1], ',')) == 1 {
			return strings.TrimSpace(m[1])
		}
		if elem, ok := strings.CutSuffix(text, "[]"); ok {
			return strings.TrimSpace(elem)
		}
	}
	return ""
}

// awaitedText returns what a declared TypeScript type gives when
// awaited: the argument of Promise<T>, any other type itself.
func awaitedText(text string) string {
	if m := tsPromise.FindStringSubmatch(tsDefined(text)); m != nil {
		return strings.TrimSpace(m[1])
	}
	return text
}

// Type parameter lists: after the name of a TypeScript class, interface
// or type alias, or of a method; after the name of a Go type.
var (
	tsContainerParams = regexp.MustCompile(`(?:class|interface|type)\s+[\w$]+\s*<`)
	tsMethodParams    = regexp.MustCompile(`^(?:[a-z]+\s+)*[\w$#]+\??\s*<`)
	goTypeParams      = regexp.MustCompile(`^(?:type\s+)?\w+\s*\[`)
	paramName         = regexp.MustCompile(`^\s*([A-Za-z_$][\w$]*)`)
)

// declaresParam reports whether a signature declares the type parameter
// name: sig is that of a method (member) or of the type that holds it.
func declaresParam(fam, sig, name string, member bool) bool {
	list := tsContainerParams
	switch {
	case fam == "go":
		list = goTypeParams
	case member:
		list = tsMethodParams
	}
	at := list.FindStringIndex(sig)
	if at == nil {
		return false
	}
	end := closing(sig[at[1]-1:])
	if end < 0 {
		return false
	}
	for _, p := range splitTop(sig[at[1]:at[1]-1+end], ',') {
		if m := paramName.FindStringSubmatch(p); m != nil && m[1] == name {
			return true
		}
	}
	return false
}
