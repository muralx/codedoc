package python_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/muralx/codedoc/internal/testutil"
)

const constKeySrc = `import os
import cfg

MODE_KEY = "APP_MODE"
lower_key = "LOWER_KEY"
FMT_KEY = f"X{1}"


class C:
    HOME = "APP_HOME"

    def f(self, name):
        a = os.environ.get(MODE_KEY)
        b = os.getenv(cfg.LEVEL_KEY, "d")
        c = os.environ[MODE_KEY]
        d = os.environ[self.HOME]
        return os.getenv(name) + os.getenv(default=MODE_KEY)
`

func TestConfigReadsThroughConstants(t *testing.T) {
	res := extractSrc(t, "m.py", constKeySrc)
	want := []string{"MODE_KEY@13:27", "LEVEL_KEY@14:26", "MODE_KEY@15:23", "HOME@16:28", "name@17:25"}
	if got := testutil.ConstConfigRefs(res.References); !slices.Equal(got, want) {
		t.Errorf("const config refs %v, want %v", got, want)
	}
	values := map[string]string{"MODE_KEY": "APP_MODE", "C.HOME": "APP_HOME"}
	if got := testutil.SymbolValues(res.Symbols); !maps.Equal(got, values) {
		t.Errorf("values %v, want %v", got, values)
	}
}
