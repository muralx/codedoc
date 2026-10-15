package ts

// builtins holds the global types and constructors of the JS runtime and
// the TS standard library. Unqualified uses of them are not references to
// anything in the index.
var builtins = setOf(
	"Array", "ReadonlyArray", "Map", "ReadonlyMap", "WeakMap", "Set", "ReadonlySet", "WeakSet",
	"Promise", "PromiseLike", "Date", "Error", "TypeError", "RangeError", "SyntaxError",
	"ReferenceError", "EvalError", "URIError", "AggregateError", "RegExp", "Object", "String",
	"Number", "Boolean", "Symbol", "BigInt", "Function", "JSON", "Math", "Reflect", "Proxy",
	"ArrayBuffer", "SharedArrayBuffer", "DataView", "Iterable", "Iterator", "IterableIterator",
	"AsyncIterable", "AsyncIterator", "Generator", "AsyncGenerator",
	"Int8Array", "Uint8Array", "Uint8ClampedArray", "Int16Array", "Uint16Array", "Int32Array",
	"Uint32Array", "Float32Array", "Float64Array", "BigInt64Array", "BigUint64Array",
	"Record", "Partial", "Required", "Readonly", "Pick", "Omit", "Exclude", "Extract",
	"NonNullable", "ReturnType", "Parameters", "ConstructorParameters", "InstanceType",
	"Awaited", "ThisType", "ThisParameterType", "OmitThisParameter",
	"Uppercase", "Lowercase", "Capitalize", "Uncapitalize",
)

func setOf(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}
