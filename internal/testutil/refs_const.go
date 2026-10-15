package testutil

import (
	"strconv"

	"github.com/muralx/codedoc/internal/extract"
)

// ConstConfigRefs returns the config candidates read through a constant
// name (extract.ConfigAccessorConst) as "Name@line:col", in order.
func ConstConfigRefs(refs []extract.Reference) []string {
	var out []string
	for _, r := range refs {
		if r.Kind == extract.RefConfig && r.Qualifier == extract.ConfigAccessorConst {
			out = append(out, r.Name+"@"+strconv.Itoa(r.Line)+":"+strconv.Itoa(r.Col))
		}
	}
	return out
}

// SymbolValues maps the symbols that carry a constant string value to
// that value.
func SymbolValues(syms []extract.Symbol) map[string]string {
	out := map[string]string{}
	for _, s := range syms {
		if s.Value != "" {
			out[s.Qualified] = s.Value
		}
	}
	return out
}
