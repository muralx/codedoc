package cfg

import (
	"bytes"
	"path"
	"strings"
)

// extractors holds one extractor per format.
var extractors = map[string]Extractor{
	YAML: yamlExtractor{}, JSON: jsonExtractor{}, XML: xmlExtractor{},
	Properties: propertiesExtractor{}, Env: envExtractor{}, HCL: hclExtractor{},
	Shell: shellExtractor{}, Template: templateExtractor{},
}

var formatByExt = map[string]string{
	".yaml": YAML, ".yml": YAML, ".json": JSON, ".xml": XML, ".properties": Properties,
	".env": Env, ".tf": HCL, ".hcl": HCL, ".tfvars": HCL, ".sh": Shell, ".bash": Shell,
	".jsp": Template, ".tpl": Template, ".tmpl": Template, ".j2": Template, ".template": Template,
}

// notConfigJSON lists JSON files that are tool manifests or lock files, not
// configuration.
var notConfigJSON = map[string]bool{
	"package.json": true, "package-lock.json": true,
}

// For returns the extractor for a format, or nil for an unknown format.
func For(format string) Extractor {
	return extractors[format]
}

// FormatForPath returns the config format of a file from its slash path and
// the first bytes of its content (for the shebang of extension-less
// scripts), or "" when the file is not a config file.
func FormatForPath(p string, head []byte) string {
	base := path.Base(p)
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return Env
	}
	ext := strings.ToLower(path.Ext(base))
	if ext == ".json" && isExcludedJSON(base) {
		return ""
	}
	if f, ok := formatByExt[ext]; ok {
		return f
	}
	if ext == "" && isShellShebang(head) {
		return Shell
	}
	return ""
}

// isExcludedJSON reports whether a .json file name is a manifest, a
// compiler config or a lock file.
func isExcludedJSON(base string) bool {
	lower := strings.ToLower(base)
	return notConfigJSON[lower] || strings.HasPrefix(lower, "tsconfig") ||
		strings.HasSuffix(lower, ".lock.json")
}

// isShellShebang reports whether head starts with a #! line running sh or
// bash, directly or through env.
func isShellShebang(head []byte) bool {
	if !bytes.HasPrefix(head, []byte("#!")) {
		return false
	}
	line, _, _ := bytes.Cut(head[2:], []byte("\n"))
	fields := strings.Fields(string(line))
	if len(fields) > 0 && path.Base(fields[0]) == "env" {
		fields = fields[1:]
		for len(fields) > 0 && strings.HasPrefix(fields[0], "-") {
			fields = fields[1:]
		}
	}
	if len(fields) == 0 {
		return false
	}
	interp := path.Base(fields[0])
	return interp == "sh" || interp == "bash"
}
