package java_test

import (
	"github.com/muralx/codedoc/internal/testutil"
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
)

const refSrc = `package p;

import com.fixture.store.Store;
import java.util.*;
import static java.util.Objects.requireNonNull;

class C<T extends Base> extends a.Parent<Item> implements Runnable, b.Named<T> {
    @Value("${app.mode}") String mode;
    @Value(value = "${app.port:8080}/${app.host}") String port;
    private final Map<String, Store> stores = new HashMap<>();

    C(Store s) { super(s); }

    public void run() {
        this.stores.get("k").load(new a.Loader<Item>(1, 2), Integer.valueOf(3));
        String home = System.getenv("JAVA_HOME") + System.getProperty("user.dir");
        env.getProperty("plain");
        log("app.db.url", "Not a key", "ESCAPED\tKEY", "");
        Object o = new Object();
    }
}

interface I extends J, K<L> {}
`

func ref(kind extract.RefKind, name, qual, enc string, line, arity int) extract.Reference {
	return extract.Reference{Kind: kind, Name: name, Qualifier: qual, Enclosing: enc, Line: line, Arity: arity}
}

const c, run = "C", "C.run"

var refWant = []extract.Reference{
	ref(extract.RefType, "Base", "", c, 7, -1),
	ref(extract.RefExtend, "Parent", "a", c, 7, -1),
	ref(extract.RefType, "Item", "", c, 7, -1),
	ref(extract.RefImplement, "Runnable", "", c, 7, -1),
	ref(extract.RefImplement, "Named", "b", c, 7, -1),
	ref(extract.RefType, "T", "", c, 7, -1),
	ref(extract.RefType, "Value", "", "C.mode", 8, -1),
	ref(extract.RefConfig, "app.mode", extract.ConfigAccessor, "C.mode", 8, -1),
	ref(extract.RefType, "Value", "", "C.port", 9, -1),
	ref(extract.RefConfig, "app.port", extract.ConfigAccessor, "C.port", 9, -1),
	ref(extract.RefConfig, "app.host", extract.ConfigAccessor, "C.port", 9, -1),
	ref(extract.RefType, "Map", "", "C.stores", 10, -1),
	ref(extract.RefType, "Store", "", "C.stores", 10, -1),
	ref(extract.RefInstantiate, "HashMap", "", "C.stores", 10, 0),
	ref(extract.RefType, "Store", "", "C.<init>", 12, -1),
	ref(extract.RefCall, "load", "this.stores.get()", run, 15, 2),
	ref(extract.RefCall, "get", "this.stores", run, 15, 1),
	ref(extract.RefField, "stores", "this", run, 15, -1),
	ref(extract.RefInstantiate, "Loader", "a", run, 15, 2),
	ref(extract.RefType, "Item", "", run, 15, -1),
	ref(extract.RefCall, "valueOf", "Integer", run, 15, 1),
	ref(extract.RefCall, "getenv", "System", run, 16, 1),
	ref(extract.RefConfig, "JAVA_HOME", extract.ConfigAccessor, run, 16, -1),
	ref(extract.RefCall, "getProperty", "System", run, 16, 1),
	ref(extract.RefConfig, "user.dir", extract.ConfigAccessor, run, 16, -1),
	ref(extract.RefCall, "getProperty", "env", run, 17, 1),
	ref(extract.RefConfig, "plain", extract.ConfigAccessor, run, 17, -1),
	ref(extract.RefCall, "log", "", run, 18, 4),
	ref(extract.RefConfig, "app.db.url", extract.ConfigLiteral, run, 18, -1),
	ref(extract.RefExtend, "J", "", "I", 23, -1),
	ref(extract.RefExtend, "K", "", "I", 23, -1),
	ref(extract.RefType, "L", "", "I", 23, -1),
}

func TestReferences(t *testing.T) {
	res := extractSrc(t, "src/main/java/p/C.java", refSrc)
	want := refWant
	if !reflect.DeepEqual(testutil.WithoutCols(res.References), want) {
		for _, r := range res.References {
			t.Logf("%+v", r)
		}
		t.Errorf("references differ")
	}
}

func TestImports(t *testing.T) {
	res := extractSrc(t, "src/main/java/p/C.java", refSrc)
	want := []extract.Import{
		{Raw: "com.fixture.store.Store", Alias: "Store", Line: 3},
		{Raw: "java.util.*", Line: 4},
		{Raw: "java.util.Objects.requireNonNull", Alias: "requireNonNull", Line: 5},
	}
	if !reflect.DeepEqual(res.Imports, want) {
		t.Errorf("got %+v", res.Imports)
	}
}
