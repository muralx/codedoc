package extract

import "strings"

// BuiltinType is the type recorded for a variable of a type the
// language itself provides (Go's predeclared types, the TS standard
// library, arrays): no symbol of the index declares its members.
const BuiltinType = "-"

// UnknownType is the type recorded for a variable whose declaration is
// seen but makes no type evident (the result of a method, a loop
// variable, a destructured name, an untyped parameter): the variable
// hides an import, a package or an outer variable of the same name, and
// what is reached through it is never a confident guess.
const UnknownType = "?"

// SelfPrefix starts the name a property of the enclosing class is
// declared under (see Locals.DeclareProperty): this.x.
const SelfPrefix = "this."

// Locals records, while a file is walked in source order, the types of
// the variables whose declaration makes them evident: a typed
// declaration or parameter, a constructor call, a composite literal.
// Variables live in lexical scopes: the walker pushes one when it enters
// a function, a closure, a block, a loop, a catch clause or the
// initializer of an if or switch, and pops it on the way out. A lookup
// walks outward from the innermost scope and sees what was declared
// above it; the outermost scope holds the variables of the file itself.
// Properties of a class (this.x) are kept by class, apart from scopes.
type Locals struct {
	scopes []map[string]string
	props  map[localKey]string
}

type localKey struct{ class, name string }

// NewLocals returns a record holding the file scope alone.
func NewLocals() *Locals {
	return &Locals{scopes: []map[string]string{nil}, props: map[localKey]string{}}
}

// Push opens a scope inside the current one.
func (l *Locals) Push() { l.scopes = append(l.scopes, nil) }

// Pop closes the innermost scope and forgets its variables; the file
// scope stays.
func (l *Locals) Pop() {
	if len(l.scopes) > 1 {
		l.scopes = l.scopes[:len(l.scopes)-1]
	}
}

// Nested reports whether a scope inside the file scope is open.
func (l *Locals) Nested() bool { return len(l.scopes) > 1 }

// Inner reports whether a scope inside the file scope declares name: a
// parameter or a variable of a function, which hides a function or an
// import of the file.
func (l *Locals) Inner(name string) bool {
	for i := len(l.scopes) - 1; i > 0; i-- {
		if _, ok := l.scopes[i][name]; ok {
			return true
		}
	}
	return false
}

// Declare records that the variable name, declared in the innermost
// scope, has the type typ from here on. An empty typ is a declaration
// whose type is not evident: it hides an outer or earlier variable of
// the same name. The blank name declares nothing.
func (l *Locals) Declare(name, typ string) {
	if name == "" || name == "_" {
		return
	}
	top := len(l.scopes) - 1
	if l.scopes[top] == nil {
		l.scopes[top] = map[string]string{}
	}
	l.scopes[top][name] = typ
}

// Retype gives the nearest declaration of name the type typ from here
// on: an assignment that changes what the variable holds. It reports
// false for a name no scope declares.
func (l *Locals) Retype(name, typ string) bool {
	for i := len(l.scopes) - 1; i >= 0; i-- {
		if _, ok := l.scopes[i][name]; ok {
			l.scopes[i][name] = typ
			return true
		}
	}
	return false
}

// Lookup returns the type of the nearest declaration of name, walking
// outward, "" when it is not evident; ok is false for a name no scope
// declares.
func (l *Locals) Lookup(name string) (typ string, ok bool) {
	for i := len(l.scopes) - 1; i >= 0; i-- {
		if typ, ok := l.scopes[i][name]; ok {
			return typ, true
		}
	}
	return "", false
}

// DeclareProperty records the type of the property name of the class a
// member lies in (member is the qualified name of the enclosing
// symbol); "" when its declaration makes none evident. It reports false
// outside a class.
func (l *Locals) DeclareProperty(member, name, typ string) bool {
	class := classOf(member)
	if class == "" || name == "" {
		return false
	}
	l.props[localKey{class, name}] = typ
	return true
}

// PropertyDeclared reports whether the class of member has a recorded
// property name.
func (l *Locals) PropertyDeclared(member, name string) bool {
	_, ok := l.props[localKey{classOf(member), name}]
	return ok
}

// Property returns the type recorded for the property name of the
// class of member, "" when none is evident.
func (l *Locals) Property(member, name string) string {
	return l.props[localKey{classOf(member), name}]
}

// TypeOf returns the type recorded for what a qualifier starts with, as
// seen from inside the symbol member: for this.x.y the property x of
// the enclosing class, else the variable its first segment names
// (UnknownType when the variable is declared without an evident type).
// It is empty when the qualifier starts with anything else. The type
// may be a type path (see FieldOf).
func (l *Locals) TypeOf(member, qualifier string) string {
	head, rest, _ := strings.Cut(qualifier, ".")
	if head+"." == SelfPrefix {
		prop, _, _ := strings.Cut(rest, ".")
		return memberless(l.props[localKey{classOf(member), prop}])
	}
	typ, ok := l.Lookup(head)
	if ok && typ == "" {
		return UnknownType
	}
	return memberless(typ)
}

// memberless turns the type of an array (see ArrayPrefix) into
// BuiltinType: its own members are the language's, whatever it holds.
func memberless(typ string) string {
	if strings.HasPrefix(typ, ArrayPrefix) {
		return BuiltinType
	}
	return typ
}

// LocalSegments returns how many leading segments of a qualifier a type
// recorded by Locals stands for: two for this.x, else one.
func LocalSegments(qualifier string) int {
	if strings.HasPrefix(qualifier, SelfPrefix) {
		return 2
	}
	return 1
}

// classOf returns the qualified name of the class a member's qualified
// name lies in (the name up to its last dot, after any file prefix), ""
// for a name that is no member.
func classOf(qualified string) string {
	dot := strings.LastIndex(qualified, ".")
	if dot <= strings.LastIndex(qualified, "/")+1 {
		return ""
	}
	return qualified[:dot]
}
