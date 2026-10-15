package resolve

import (
	"github.com/muralx/codedoc/internal/store"
)

// configKind is the reference kind of config key candidates (spec 7.2).
const configKind = "config"

// valueKinds are the kinds of the symbols a config accessor's constant
// argument may name: constants, and variables and fields holding a
// constant string (extractors record a value only for those).
var valueKinds = []string{"const", "var", "field"}

// splitConfig separates config candidates from the references that
// resolve to symbols.
func splitConfig(refs []store.Reference) (symbols, configs []store.Reference) {
	for _, ref := range refs {
		if ref.Kind == configKind {
			configs = append(configs, ref)
		} else {
			symbols = append(symbols, ref)
		}
	}
	return symbols, configs
}

// configReads matches config candidates against config keys (spec 7.2,
// section 8): a candidate reads every key row of the workspace with its
// exact name that some config file defines, one reads use per key row;
// a candidate without a match stays in the unresolved view. A candidate
// read through a constant (store.ConfigAccessorConst) names the key its
// constant's value spells, recorded on the reference (see constKey). With
// all set every reads use is rebuilt; otherwise only those of refs. Run
// it after load: constants resolve against the loaded symbols.
func (r *refResolver) configReads(refs []store.Reference, all bool) error {
	ids := make([]int64, len(refs))
	keyOf := make(map[int64]string, len(refs))
	names := map[string]bool{}
	for i, ref := range refs {
		key, err := r.configKey(ref)
		if err != nil {
			return err
		}
		ids[i], keyOf[ref.ID] = ref.ID, key
		if key != "" {
			names[key] = true
		}
	}
	var del []int64
	var want []string
	if !all {
		del, want = ids, keys(names)
	}
	if err := r.b.DeleteConfigReads(del); err != nil || len(refs) == 0 {
		return err
	}
	byName, err := r.definedKeys(want)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		for _, k := range byName[keyOf[ref.ID]] {
			err := r.b.InsertConfigRead(store.ConfigRead{KeyID: k.ID, FileID: ref.FileID, SymbolID: ref.SymbolID,
				ReferenceID: ref.ID, Line: ref.Line, Format: ref.Lang})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// definedKeys returns the defined key rows named one of names (all of
// them when names is nil) by name.
func (r *refResolver) definedKeys(names []string) (map[string][]store.ConfigKey, error) {
	defined, err := r.b.DefinedConfigKeys(names)
	byName := map[string][]store.ConfigKey{}
	for _, k := range defined {
		byName[k.Key] = append(byName[k.Key], k)
	}
	return byName, err
}

// configKey returns the key a config candidate reads: its name, or for
// one read through a constant the constant's value, which it records on
// the reference ("" when the name resolves to no constant string).
func (r *refResolver) configKey(ref store.Reference) (string, error) {
	if ref.Qualifier != store.ConfigAccessorConst {
		return ref.Name, nil
	}
	key, err := r.constKey(ref)
	if err != nil {
		return "", err
	}
	return key, r.b.SetReferenceConfigKey(ref.ID, key)
}

// constKey resolves the constant name of a config candidate like a field
// reference through the tiers of spec 7.3, qualified as written (the
// field reference the extractor recorded at the same position carries
// the qualifier: pkg in pkg.KEY), and returns the string value of the
// constant it names; "" when it names none, or several with different
// values.
func (r *refResolver) constKey(ref store.Reference) (string, error) {
	q, err := r.b.FieldQualifierAt(ref.FileID, ref.Line, ref.Col, ref.Name)
	if err != nil {
		return "", err
	}
	field := ref
	field.Kind, field.Qualifier = "field", q
	out, c := r.resolveKinds(field, func(string, bool) []string { return valueKinds })
	if out.confidence == "" {
		return "", nil
	}
	chosen := make(map[int64]bool, len(out.dst))
	for _, id := range out.dst {
		chosen[id] = true
	}
	value := ""
	for _, t := range c.all() {
		switch {
		case !chosen[t.ID]:
		case t.Value == "" || (value != "" && t.Value != value):
			return "", nil
		default:
			value = t.Value
		}
	}
	return value, nil
}
