package python

// builtins holds Python's builtin functions, types and exceptions and the
// common typing names. Unqualified uses of them are not references to
// anything in the index.
var builtins = setOf(
	"abs", "aiter", "all", "anext", "any", "ascii", "bin", "bool", "breakpoint", "bytearray", "bytes",
	"callable", "chr", "classmethod", "compile", "complex", "delattr", "dict", "dir", "divmod",
	"enumerate", "eval", "exec", "filter", "float", "format", "frozenset", "getattr", "globals",
	"hasattr", "hash", "help", "hex", "id", "input", "int", "isinstance", "issubclass", "iter", "len",
	"list", "locals", "map", "max", "memoryview", "min", "next", "object", "oct", "open", "ord", "pow",
	"print", "property", "range", "repr", "reversed", "round", "set", "setattr", "slice", "sorted",
	"staticmethod", "str", "sum", "super", "tuple", "type", "vars", "zip", "__import__",
	"BaseException", "Exception", "ArithmeticError", "AssertionError", "AttributeError",
	"BlockingIOError", "BrokenPipeError", "BufferError", "ConnectionError", "EOFError",
	"FileExistsError", "FileNotFoundError", "FloatingPointError", "GeneratorExit", "ImportError",
	"IndexError", "InterruptedError", "IOError", "IsADirectoryError", "KeyError",
	"KeyboardInterrupt", "LookupError", "MemoryError", "ModuleNotFoundError", "NameError",
	"NotADirectoryError", "NotImplementedError", "OSError", "OverflowError", "PermissionError",
	"RecursionError", "ReferenceError", "RuntimeError", "StopAsyncIteration", "StopIteration",
	"SyntaxError", "SystemError", "SystemExit", "TimeoutError", "TypeError", "UnboundLocalError",
	"UnicodeDecodeError", "UnicodeEncodeError", "UnicodeError", "ValueError", "ZeroDivisionError",
	"Warning", "DeprecationWarning", "FutureWarning", "RuntimeWarning", "UserWarning",
	"Any", "Annotated", "Callable", "ClassVar", "Dict", "Final", "FrozenSet", "Generator", "Iterable",
	"Iterator", "List", "Literal", "Mapping", "Optional", "Self", "Sequence", "Set", "Tuple", "Type",
	"TypeVar", "Union",
)

func setOf(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}
