package ts_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/extract/ts"
	"github.com/muralx/codedoc/internal/testutil"
)

const constKeySrc = `export const KEY = "APP_KEY";
let mutable = "MUTABLE_KEY";
const tmpl = ` + "`T_KEY`" + `;

export class C {
  static readonly HOME = "APP_HOME";
  plain = "PLAIN";
  f() {
    return process.env[KEY] + process.env[Cfg.LEVEL] + process.env[this.HOME] + process.env["LIT"];
  }
}
`

func TestConfigReadsThroughConstants(t *testing.T) {
	res, err := ts.New("typescript").Extract("src/c.ts", []byte(constKeySrc))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"KEY@9:23", "LEVEL@9:46", "HOME@9:72"}
	if got := testutil.ConstConfigRefs(res.References); !slices.Equal(got, want) {
		t.Errorf("const config refs %v, want %v", got, want)
	}
	field := extract.Reference{Kind: extract.RefField, Name: "LEVEL", Qualifier: "Cfg", Enclosing: "src/c.ts/C.f", Line: 9,
		Col: 46, Arity: -1}
	if !slices.Contains(res.References, field) {
		t.Errorf("no field reference %+v in %+v", field, res.References)
	}
	values := map[string]string{"src/c.ts/KEY": "APP_KEY", "src/c.ts/tmpl": "T_KEY", "src/c.ts/C.HOME": "APP_HOME"}
	if got := testutil.SymbolValues(res.Symbols); !maps.Equal(got, values) {
		t.Errorf("values %v, want %v", got, values)
	}
}
