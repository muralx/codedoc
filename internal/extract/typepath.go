package extract

import "strings"

// A type path is what Locals records for a variable whose type is not
// named where it is declared but follows from declared types: a root
// (a type name, a call whose first result it is, SelfRoot, an
// OuterPrefix name, or ArrayPrefix and an element type) and the steps
// taken from it, each read by the resolver from a declaration:
//
//	Options#client      the declared type of the field client of Options
//	Store#get()         the declared result of the method get of Store
//	Options#items@      an element of that field (for...of, range)
//	Store#get()^        that result awaited (Promise<T> gives T)
//
// The extractors only write the path down; what a step lands on is
// decided at resolution time, where the declarations are known.
const (
	// ArrayPrefix starts the type of an array, slice or map: its
	// elements have the type that follows.
	ArrayPrefix = "[]"
	// SelfRoot is the root of a path that starts at the enclosing
	// class (this).
	SelfRoot = "this"
	// OuterPrefix starts the root of a path that begins with a name no
	// scope of the file declares: the receiver of the enclosing Go
	// method or a variable of its package.
	OuterPrefix = "$"

	stepField = '#'
	stepElem  = '@'
	stepAwait = '^'
	stepChars = "#@^"
	callMark  = "()"
)

// TypeStepKind tells the steps of a type path apart.
type TypeStepKind int

// Steps of a type path.
const (
	// StepField is the declared type of a field.
	StepField TypeStepKind = iota
	// StepResult is the declared result of a method.
	StepResult
	// StepElem is the element of an array, slice or map.
	StepElem
	// StepAwait is the value a promise resolves to.
	StepAwait
)

// TypeStep is one step of a type path; Name is the field or method of a
// StepField or StepResult.
type TypeStep struct {
	Kind TypeStepKind
	Name string
}

// follows reports whether a step may be taken from the recorded type t:
// it names a type, which a builtin, an unknown and an array do not.
func follows(t string) bool {
	return t != "" && t != UnknownType && t != BuiltinType && !strings.HasPrefix(t, ArrayPrefix)
}

// FieldOf returns the path to the declared type of the field name of t,
// "" when t names no type.
func FieldOf(t, name string) string {
	if !follows(t) || name == "" {
		return ""
	}
	return t + string(stepField) + name
}

// ResultOf returns the path to the declared result of the method name
// of t, "" when t names no type.
func ResultOf(t, name string) string {
	if f := FieldOf(t, name); f != "" {
		return f + callMark
	}
	return ""
}

// ArrayOf returns the type of an array of elem ("" for elements whose
// type is not evident).
func ArrayOf(elem string) string {
	if elem == UnknownType {
		elem = ""
	}
	return ArrayPrefix + elem
}

// ElemOf returns the type of an element of t: what follows ArrayPrefix,
// else the path to the element type of what t leads to.
func ElemOf(t string) string {
	if elem, ok := strings.CutPrefix(t, ArrayPrefix); ok {
		return elem
	}
	if !follows(t) {
		return ""
	}
	return t + string(stepElem)
}

// Awaited returns the type of an awaited value of type t: t itself for
// an array, else the path through the promise t may lead to.
func Awaited(t string) string {
	switch {
	case strings.HasPrefix(t, ArrayPrefix):
		return t
	case !follows(t):
		return ""
	}
	return t + string(stepAwait)
}

// ParseTypePath splits a recorded type into its root and its steps; a
// type recorded without steps is its own root.
func ParseTypePath(t string) (root string, steps []TypeStep) {
	i := strings.IndexAny(t, stepChars)
	if i < 0 {
		return t, nil
	}
	root = t[:i]
	for rest := t[i:]; rest != ""; {
		kind, end := rest[0], strings.IndexAny(rest[1:], stepChars)+1
		if end == 0 {
			end = len(rest)
		}
		name := rest[1:end]
		switch call, isCall := strings.CutSuffix(name, callMark); {
		case kind == stepElem:
			steps = append(steps, TypeStep{Kind: StepElem})
		case kind == stepAwait:
			steps = append(steps, TypeStep{Kind: StepAwait})
		case isCall:
			steps = append(steps, TypeStep{Kind: StepResult, Name: call})
		default:
			steps = append(steps, TypeStep{Kind: StepField, Name: name})
		}
		rest = rest[end:]
	}
	return root, steps
}
