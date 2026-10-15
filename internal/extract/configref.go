package extract

import (
	"regexp"
	"strings"
)

// Key-like string literals (spec 7.2): env style FOO_BAR, or dotted lower
// case a.b.c.
var (
	envKey    = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,}$`)
	dottedKey = regexp.MustCompile(`^[a-z][a-z0-9_-]*(\.[a-z0-9_-]+)+$`)
)

// Config candidates record in Qualifier how the key was read: through a
// recognized accessor (os.Getenv, process.env, System.getenv, @Value,
// os.environ) or only as a string literal that looks like a key, which
// is often not a config key at all. A ConfigAccessorConst candidate is
// an accessor whose key argument is a name (KEY, pkg.KEY, Class.KEY,
// this.KEY): its Name is the name's last segment, a constant whose
// string value the resolver reads as the key.
const (
	ConfigAccessor      = "accessor"
	ConfigLiteral       = "literal"
	ConfigAccessorConst = "accessor-const"
)

// ConfigRef returns the config candidate reference for key at a position,
// marked as read through an accessor or as a key-like literal.
func ConfigRef(key string, accessor bool, enclosing string, line, col int) Reference {
	how := ConfigLiteral
	if accessor {
		how = ConfigAccessor
	}
	return Reference{Kind: RefConfig, Name: key, Qualifier: how, Enclosing: enclosing, Line: line, Col: col, Arity: -1}
}

// ConfigConstRef returns the config candidate reference of an accessor
// whose key argument is the constant name at a position (see
// ConfigAccessorConst).
func ConfigConstRef(name, enclosing string, line, col int) Reference {
	return Reference{Kind: RefConfig, Name: name, Qualifier: ConfigAccessorConst, Enclosing: enclosing, Line: line,
		Col: col, Arity: -1}
}

// LooksLikeKey reports whether a string literal could name a config key:
// env style (FOO_BAR, at least three characters) or dotted lower case (a.b.c).
func LooksLikeKey(s string) bool {
	return envKey.MatchString(s) || dottedKey.MatchString(s)
}

// configAccessors lists, per language family, the calls that read the
// config key named by their first argument, written as the qualifier
// chain and the called name joined by a dot ("os.getenv"), or the bare
// name for an unqualified call.
var configAccessors = map[string]map[string]bool{
	"py": {
		"os.getenv": true, "getenv": true, "os.environ.get": true, "environ.get": true,
		"os.environ.setdefault": true, "environ.setdefault": true,
	},
	"java": {
		"System.getenv": true, "System.getProperty": true, "env.getProperty": true,
		"environment.getProperty": true, "getEnvironment().getProperty": true,
	},
	"go": {"os.Getenv": true, "os.LookupEnv": true},
}

// configAccessorPrefixes lists, per language family, accessor families
// matched by prefix: viper.Get, viper.GetString, viper.GetBool and so on.
var configAccessorPrefixes = map[string][]string{
	"go": {"viper.Get"},
}

// configMaps lists, per language family, the mapping expressions whose
// subscript with a string literal reads a config key (os.environ["X"]).
var configMaps = map[string]map[string]bool{
	"py": {"os.environ": true, "environ": true},
	"ts": {"process.env": true},
}

// IsConfigAccessor reports whether a call to name with the given qualifier
// chain ("" when unqualified) reads a config key from its first argument
// in the language family.
func IsConfigAccessor(family, qualifier, name string) bool {
	if qualifier != "" {
		name = qualifier + "." + name
	}
	if configAccessors[family][name] {
		return true
	}
	for _, p := range configAccessorPrefixes[family] {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// IsConfigMap reports whether expr, a qualifier chain, is a mapping whose
// string subscripts are config keys in the language family.
func IsConfigMap(family, expr string) bool {
	return configMaps[family][expr]
}

// PlaceholderKeys returns the keys of the ${key} and ${key:default}
// placeholders in s, in order. A placeholder nested in a default
// (${a:${b}}) is returned after its outer key.
func PlaceholderKeys(s string) []string {
	var keys []string
	for {
		start := strings.Index(s, "${")
		if start < 0 {
			return keys
		}
		s = s[start+2:]
		end := strings.IndexAny(s, ":}")
		if end < 0 {
			return keys
		}
		if key := strings.TrimSpace(s[:end]); key != "" {
			keys = append(keys, key)
		}
		s = s[end:]
	}
}
