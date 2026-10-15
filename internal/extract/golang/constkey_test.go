package golang_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/muralx/codedoc/internal/testutil"
)

const constKeySrc = `package p

import (
	"os"

	"example.com/cfg"
)

// ClaudeBinEnv names the binary override.
const ClaudeBinEnv = "CODEDOC_CLAUDE_BIN"

const (
	Mode, Level = "APP_MODE", 3
	Raw         = ` + "`RAW_KEY`" + `
)

var notConst = "VAR_KEY"

func f(name string) {
	_ = os.Getenv(ClaudeBinEnv)
	_, _ = os.LookupEnv(cfg.HomeEnv)
	_ = os.Getenv(name)
	_ = os.Getenv(name + "X")
	_ = other(ClaudeBinEnv)
}
`

func TestConfigReadsThroughConstants(t *testing.T) {
	res := extractSrc(t, "p.go", constKeySrc)
	want := []string{"ClaudeBinEnv@20:15", "HomeEnv@21:25", "name@22:15"}
	if got := testutil.ConstConfigRefs(res.References); !slices.Equal(got, want) {
		t.Errorf("const config refs %v, want %v", got, want)
	}
	values := map[string]string{"ClaudeBinEnv": "CODEDOC_CLAUDE_BIN", "Mode": "APP_MODE", "Raw": "RAW_KEY"}
	if got := testutil.SymbolValues(res.Symbols); !maps.Equal(got, values) {
		t.Errorf("values %v, want %v", got, values)
	}
}
