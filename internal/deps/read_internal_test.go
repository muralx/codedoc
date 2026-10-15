package deps

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCappedReadsRefuseWhatIsTooLargeOrNoFile(t *testing.T) {
	dir := t.TempDir()
	small, large := filepath.Join(dir, "small.json"), filepath.Join(dir, "large.json")
	if err := os.WriteFile(small, []byte(`{"name": "x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(large, []byte(strings.Repeat("x", 100)), 0o644); err != nil {
		t.Fatal(err)
	}
	if raw, err := readCapped(small, 50); err != nil || len(raw) != 13 {
		t.Errorf("small file: %q %v", raw, err)
	}
	for _, path := range []string{large, dir, filepath.Join(dir, "absent")} {
		if _, err := readCapped(path, 50); err == nil {
			t.Errorf("%s was read", path)
		}
	}
}

func TestHeadAndJSONReads(t *testing.T) {
	dir := t.TempDir()
	small, large := filepath.Join(dir, "small.json"), filepath.Join(dir, "large.json")
	if err := os.WriteFile(small, []byte(`{"name": "x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(large, []byte(strings.Repeat("x", 100)), 0o644); err != nil {
		t.Fatal(err)
	}
	if head, err := readHead(large, 10); err != nil || len(head) != 10 {
		t.Errorf("head: %q %v", head, err)
	}
	if _, err := readHead(filepath.Join(dir, "absent"), 10); err == nil {
		t.Error("the head of an absent file was read")
	}
	var pj packageJSON
	if err := readJSON(small, &pj); err != nil || pj.Name != "x" {
		t.Errorf("readJSON: %+v %v", pj, err)
	}
	if err := readJSON(large, &pj); err == nil || !strings.Contains(err.Error(), "large.json") {
		t.Errorf("garbled JSON: %v", err)
	}
}

func TestWithinFollowsLinksOnBothSides(t *testing.T) {
	outside, root := t.TempDir(), filepath.Join(t.TempDir(), "pkg")
	alias := filepath.Join(filepath.Dir(root), "alias")
	for _, p := range []string{filepath.Join(root, "in.d.ts"), filepath.Join(outside, "out.d.ts")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{filepath.Join(root, "out.d.ts"): filepath.Join(outside, "out.d.ts"), alias: root} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	if full, err := within(alias, "in.d.ts"); err != nil || filepath.Base(full) != "in.d.ts" {
		t.Errorf("a file of a linked package directory: %q %v", full, err)
	}
	if _, err := within(root, "out.d.ts"); !errors.Is(err, errOutside) {
		t.Errorf("a link out of the package: %v", err)
	}
	if _, err := within(root, "absent.d.ts"); err == nil {
		t.Error("an absent file is within")
	}
	if _, err := within(filepath.Join(root, "absent"), "x"); err == nil {
		t.Error("an absent root holds files")
	}
}

func TestMutableRangesAndPatches(t *testing.T) {
	for rng, want := range map[string]bool{
		"^1.0.0": false, "1.2.3": false, "npm:lodash-es@^4": false, "latest": false, ">=1 <2": false, "*": false,
		"file:../x": true, "link:../x": true, "workspace:*": true, "github:u/r": true, "u/r#main": true,
		"git+ssh://git@host/r.git": true, "https://h/x.tgz": true, "../x": true, "patch:x@1#p": true,
	} {
		if got := mutableRange(rng); got != want {
			t.Errorf("mutableRange(%q) = %v", rng, got)
		}
	}
	p := npmPatches{"a@1.0.0": true, "b": true}
	if !p.has("a", "1.0.0") || p.has("a", "2.0.0") || !p.has("b", "3.0.0") || p.has("c", "1.0.0") {
		t.Error("patches match by name@version or by name")
	}
	if !npmMutable("^1", "/r/node_modules/.pnpm/x@1_patch_hash=ab/node_modules/x", "x", "1", nil) ||
		!npmMutable("^1", "/elsewhere/x", "x", "1", nil) || npmMutable("^1", "/r/node_modules/x", "x", "1", nil) {
		t.Error("a patched or linked directory is mutable, an installed one is not")
	}
	if (gomod{}).Fingerprint(Package{Dir: filepath.Join(t.TempDir(), "absent")}) != "" {
		t.Error("an absent module has a fingerprint")
	}
}
