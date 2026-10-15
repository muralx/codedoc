package java_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/muralx/codedoc/internal/testutil"
)

const constKeySrc = `package p;

class C {
    static final String HOME_ENV = "APP_HOME";
    private final String key = "INSTANCE_KEY";
    static String mutable = "MUTABLE_KEY";
    static final int N = 3;

    String f(String name) {
        String a = System.getenv(HOME_ENV);
        String b = System.getProperty(Config.PROP);
        String c = System.getenv(this.key);
        return System.getenv(name) + System.getenv("LIT_KEY") + System.getenv(name.trim());
    }
}
`

func TestConfigReadsThroughConstants(t *testing.T) {
	res := extractSrc(t, "C.java", constKeySrc)
	want := []string{"HOME_ENV@10:33", "PROP@11:45", "key@12:38", "name@13:29"}
	if got := testutil.ConstConfigRefs(res.References); !slices.Equal(got, want) {
		t.Errorf("const config refs %v, want %v", got, want)
	}
	values := map[string]string{"C.HOME_ENV": "APP_HOME", "C.key": "INSTANCE_KEY"}
	if got := testutil.SymbolValues(res.Symbols); !maps.Equal(got, values) {
		t.Errorf("values %v, want %v", got, values)
	}
}
