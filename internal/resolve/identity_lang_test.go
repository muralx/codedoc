package resolve_test

import (
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/resolve"
)

func TestIdentitiesPython(t *testing.T) {
	m, err := resolve.Identities(fixtureFiles(t, "py-lib"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []resolve.Identity{{Lang: "py", Identity: "fixture-pylib", Path: "pyproject.toml"}}; !reflect.DeepEqual(m.Repo, want) {
		t.Errorf("repo identities %+v", m.Repo)
	}
	for _, c := range []moduleCase{
		{"py", "src/pylib/store.py", "pylib.store", "py-module", "src/pylib", "src/pylib/store.py", "pylib.store"},
		{"py", "src/pylib/__init__.py", "pylib", "py-package", "", "src/pylib/__init__.py", "pylib"},
		{"py", "tests/test_store.py", "tests.test_store", "py-module", "tests", "tests/test_store.py", "tests.test_store"},
	} {
		c.check(t, m)
	}
	if mod, ok := m.ModuleFor("py", "tests/x/y.py"); ok {
		t.Errorf("no module for an unlisted file: %+v", mod)
	}
}

func TestIdentitiesPythonLayouts(t *testing.T) {
	root := t.TempDir()
	write(t, root, "setup.py", "from setuptools import setup\nsetup(\n    name='flat-lib',\n)\n")
	write(t, root, "flat/a/b.py", "")
	write(t, root, "tool.py", "")
	write(t, root, "svc/pyproject.toml", "[tool.poetry]\nname = \"svc-dist\"\n")
	write(t, root, "svc/src/svc/main.py", "")
	write(t, root, "nameless/pyproject.toml", "[build-system]\nrequires = []\n")
	write(t, root, "nameless/n.py", "")
	m, err := resolve.Identities(root, []string{"setup.py", "flat/a/b.py", "tool.py", "svc/pyproject.toml",
		"svc/src/svc/main.py", "nameless/pyproject.toml", "nameless/n.py"})
	if err != nil {
		t.Fatal(err)
	}
	want := []resolve.Identity{
		{Lang: "py", Identity: "flat-lib", Path: "setup.py"},
		{Lang: "py", Identity: "svc-dist", Path: "svc/pyproject.toml"},
	}
	if !reflect.DeepEqual(m.Repo, want) {
		t.Errorf("repo identities %+v", m.Repo)
	}
	for _, c := range []moduleCase{
		{"py", "flat/a/b.py", "flat.a.b", "py-module", "flat/a", "flat/a/b.py", "flat.a.b"},
		{"py", "tool.py", "tool", "py-module", "", "tool.py", "tool"},
		{"py", "svc/src/svc/main.py", "svc.main", "py-module", "svc/src/svc", "svc/src/svc/main.py", "svc.main"},
		{"py", "nameless/n.py", "n", "py-module", "", "nameless/n.py", "n"},
	} {
		c.check(t, m)
	}
}

func TestIdentitiesJava(t *testing.T) {
	m, err := resolve.Identities(fixtureFiles(t, "java-lib"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []resolve.Identity{{Lang: "java", Identity: "com.fixture/javalib", Path: "pom.xml"}}; !reflect.DeepEqual(m.Repo, want) {
		t.Errorf("repo identities %+v", m.Repo)
	}
	for _, c := range []moduleCase{
		{"java", "src/main/java/com/fixture/store/Store.java", "com.fixture.store", "java-package", "",
			"src/main/java/com/fixture/store/Store.java", "com.fixture.store"},
		{"java", "src/test/java/com/fixture/store/MemStoreTest.java", "com.fixture.store", "java-package", "",
			"src/test/java/com/fixture/store/MemStoreTest.java", "com.fixture.store"},
		{"java", "src/main/java/com/fixture/service/Service.java", "com.fixture.service", "java-package", "",
			"src/main/java/com/fixture/service/Service.java", "com.fixture.service"},
	} {
		c.check(t, m)
	}
}

func TestIdentitiesJavaLayouts(t *testing.T) {
	root := t.TempDir()
	write(t, root, "settings.gradle.kts", "rootProject.name = \"gradle-app\"\n")
	write(t, root, "child/pom.xml", "<project><parent><groupId>org.p</groupId></parent><artifactId>child</artifactId></project>")
	write(t, root, "bad/pom.xml", "<project><artifactId>")
	write(t, root, "settings.gradle", "include 'x'\n")
	files := []string{"settings.gradle.kts", "child/pom.xml", "bad/pom.xml", "settings.gradle",
		"src/org/x/A.java", "lib/src/main/java/org/y/B.java", "plain/C.java", "D.java", "app/src/main/kotlin/K.java"}
	m, err := resolve.Identities(root, files)
	if err != nil {
		t.Fatal(err)
	}
	want := []resolve.Identity{
		{Lang: "java", Identity: "org.p/child", Path: "child/pom.xml"},
		{Lang: "java", Identity: "gradle-app", Path: "settings.gradle.kts"},
	}
	if !reflect.DeepEqual(m.Repo, want) {
		t.Errorf("repo identities %+v", m.Repo)
	}
	for _, c := range []moduleCase{
		{"java", "src/org/x/A.java", "org.x", "java-package", "", "src/org/x/A.java", "org.x"},
		{"java", "lib/src/main/java/org/y/B.java", "org.y", "java-package", "", "lib/src/main/java/org/y/B.java", "org.y"},
		{"java", "plain/C.java", "plain", "java-package", "", "plain/C.java", "plain"},
		{"java", "D.java", ".", "java-package", "", "D.java", "."},
		{"java", "app/src/main/kotlin/K.java", "app/src/main/kotlin", "java-package", "", "app/src/main/kotlin/K.java",
			"app/src/main/kotlin"},
	} {
		c.check(t, m)
	}
}
