package cfg

// templateExtractor reads templates (.jsp, .tpl, .tmpl, .j2, .template),
// which define nothing and refer to keys through ${X} and ${X:default}.
type templateExtractor struct{}

// Extract implements Extractor. Templates have no hard failures.
func (templateExtractor) Extract(_ string, src []byte) (*Result, error) {
	r := &Result{Format: Template}
	r.addPlaceholders(string(src), 1)
	return r, nil
}
