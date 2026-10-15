package mcpserver

// impactBudget is the default budget of codedoc_impact, as on the CLI:
// a summary whose sections each name the command for the rest of them.
const impactBudget = 3000

// ImpactInput is the input of codedoc_impact.
type ImpactInput struct {
	Common
	WithDetail
	WithLines
	Symbol string `json:"symbol" jsonschema:"address of the symbol about to change"`
	Only   string `json:"only,omitempty" jsonschema:"print one section alone, which offset then pages"`
}

// budget is the default budget of an impact answer.
func (ImpactInput) budget() int { return impactBudget }

func (in ImpactInput) build(a *argv) error {
	a.words = []string{"impact"}
	a.str("only", in.Only)
	a.str("context", in.Context)
	a.boolean("detail", in.Detail)
	a.arg(in.Symbol)
	return required("symbol", in.Symbol, "")
}

// HowInput is the input of codedoc_how.
type HowInput struct {
	Common
	Target string `json:"target" jsonschema:"symbol address or file path (repo:file, a path in the current repo)"`
}

func (in HowInput) build(a *argv) error {
	a.words = []string{"how"}
	a.arg(in.Target)
	return required("target", in.Target, "")
}

// APIInput is the input of codedoc_api.
type APIInput struct {
	Common
	WithDetail
	Package string `json:"package" jsonschema:"package name as the manifest declares it (zod, @scope/name, a Go module path), optionally name@version, or an import specifier below it"`
	Name    string `json:"name,omitempty" jsonschema:"a type, function or Type.member of the package: its doc and members"`
}

func (in APIInput) build(a *argv) error {
	a.words = []string{"api"}
	a.boolean("detail", in.Detail)
	a.arg(in.Package)
	a.arg(in.Name)
	return required("package", in.Package, "")
}
