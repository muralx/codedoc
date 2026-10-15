package cfg

import (
	"path"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// hclExtractor reads HCL and Terraform: variable "x" blocks define var.x,
// locals attributes define local.x, top-level attributes their name, and
// resource, data and module attributes their address plus attribute name.
// In a .tfvars file top-level attributes assign variables, so they define
// var.x. var.x and local.x traversals anywhere are references.
type hclExtractor struct{}

// Extract implements Extractor.
func (hclExtractor) Extract(p string, src []byte) (*Result, error) {
	r := &Result{Format: HCL}
	file, diags := hclsyntax.ParseConfig(src, p, hcl.InitialPos)
	if file != nil {
		if body, ok := file.Body.(*hclsyntax.Body); ok {
			prefix := ""
			if strings.EqualFold(path.Ext(p), ".tfvars") {
				prefix = "var."
			}
			hclBody(r, src, body, prefix)
		}
	}
	if diags.HasErrors() {
		return r, diags
	}
	return r, nil
}

// hclBody records the definitions of the top-level body, its attributes
// under prefix, then the references of the whole tree.
func hclBody(r *Result, src []byte, body *hclsyntax.Body, prefix string) {
	hclAttributes(r, src, prefix, body)
	for _, b := range body.Blocks {
		hclBlock(r, src, b)
	}
	_ = hclsyntax.VisitAll(body, func(n hclsyntax.Node) hcl.Diagnostics {
		if t, ok := n.(*hclsyntax.ScopeTraversalExpr); ok {
			hclTraversal(r, t.Traversal)
		}
		return nil
	})
}

// hclBlock records the definitions one top-level block makes.
func hclBlock(r *Result, src []byte, b *hclsyntax.Block) {
	switch {
	case b.Type == "variable" && len(b.Labels) == 1:
		value := ""
		if a, ok := b.Body.Attributes["default"]; ok {
			value = exprText(src, a.Expr)
		}
		r.addDef("var."+b.Labels[0], b.TypeRange.Start.Line, value)
	case b.Type == "locals":
		hclAttributes(r, src, "local.", b.Body)
	case b.Type == "resource" && len(b.Labels) == 2:
		hclAttributes(r, src, strings.Join(b.Labels, ".")+".", b.Body)
	case (b.Type == "data" || b.Type == "module") && len(b.Labels) >= 1:
		hclAttributes(r, src, b.Type+"."+strings.Join(b.Labels, ".")+".", b.Body)
	}
}

// hclAttributes defines the attributes of body, in source order, as
// prefix plus name.
func hclAttributes(r *Result, src []byte, prefix string, body *hclsyntax.Body) {
	attrs := make([]*hclsyntax.Attribute, 0, len(body.Attributes))
	for _, a := range body.Attributes {
		attrs = append(attrs, a)
	}
	slices.SortFunc(attrs, func(a, b *hclsyntax.Attribute) int {
		return a.SrcRange.Start.Byte - b.SrcRange.Start.Byte
	})
	for _, a := range attrs {
		r.addDef(prefix+a.Name, a.SrcRange.Start.Line, exprText(src, a.Expr))
	}
}

// hclTraversal records var.x and local.x traversals as references.
func hclTraversal(r *Result, t hcl.Traversal) {
	root := t.RootName()
	if (root != "var" && root != "local") || len(t) < 2 {
		return
	}
	if attr, ok := t[1].(hcl.TraverseAttr); ok {
		r.addRef(root+"."+attr.Name, t.SourceRange().Start.Line)
	}
}

// exprText returns the source text of an expression.
func exprText(src []byte, e hclsyntax.Expression) string {
	rng := e.Range()
	return string(src[rng.Start.Byte:rng.End.Byte])
}
