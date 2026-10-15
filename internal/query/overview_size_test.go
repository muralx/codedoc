package query_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

// TestOverviewModuleSizeIncludesNestedModules checks that the module
// tree sizes an npm package by the files of the directories below it,
// and not by those of another package nested in its directory.
func TestOverviewModuleSizeIncludesNestedModules(t *testing.T) {
	c := tempWorkspace(t, map[string]string{
		"package.json":                    `{"name": "root"}`,
		"tool.ts":                         "export const t = 1;\n",
		"packages/edge/package.json":      `{"name": "@x/edge"}`,
		"packages/edge/src/a.ts":          "export const a = 1;\nexport const b = 2;\n",
		"packages/edge/src/hooks/h.ts":    "export const h = 1;\n",
		"packages/edge/src/hooks/deep.ts": "export const d = 1;\n",
	})
	r, err := query.Overview(c)
	mustContain(t, run(t, r, err),
		"root  r:.  ts npm-package, 1 file, 1 line",
		"@x/edge  r:packages/edge  ts npm-package, 3 files, 4 lines",
		"@x/edge/src  r:packages/edge/src  ts npm-dir, 3 files, 4 lines")
}
