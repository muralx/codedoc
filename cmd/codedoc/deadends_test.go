package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An address that matches nothing is answered: the nearest candidates on
// stdout, nothing on stderr, exit status 0 (efficiency spec section 4).
func TestNotFoundIsAnsweredWithExitZero(t *testing.T) {
	indexed(t)
	for _, c := range []struct {
		args []string
		want []string
	}{
		{[]string{"symbol", "go-lib:store/store.go#Nope", "--body"},
			[]string{"== no exact match; nearest: (6)\nstore/store.go\n  ErrMissing  :7  "}},
		{[]string{"symbol", "store.go#memstore"}, []string{"MemStore  store/store.go:18-20\n", "\nanswered for MemStore\n"}},
		{[]string{"symbol", "newmemstore", "--no-body"}, []string{"NewMemStore  store/store.go:23  ", "\nanswered for NewMemStore\n"}},
		{[]string{"callers", "newmemstore"}, []string{"== callers of NewMemStore (", "\nanswered for NewMemStore\n"}},
		{[]string{"outline", "null.go"}, []string{"== store/null.go, 8 lines (", "\nanswered for store/null.go\n"}},
		{[]string{"lines", "null.go", "6"}, []string{"store/null.go:6  in NullStore.Get (6)\n", "\nanswered for store/null.go\n"}},
		{[]string{"find", "value stored under key"}, []string{"== where \"value stored under key\" ("}},
		{[]string{"find", "persists"}, []string{"== no symbol named so; search results: (", "Store  store/store.go:10-15  "}},
		{[]string{"symbol", "Get"}, []string{"Store.Get  store/store.go:12\n", "\nalso: MemStore.Get (go-lib:store/store.go:26), "}},
	} {
		stdout, stderr, code := exitOf(t, append(c.args, "--workspace", "fx", "--repo", "go-lib")...)
		if code != 0 || stderr != "" {
			t.Errorf("%v: exit %d, stderr %q\n%s", c.args, code, stderr, stdout)
		}
		for _, w := range c.want {
			if !strings.Contains(stdout, w) {
				t.Errorf("%v: missing %q in\n%s", c.args, w, stdout)
			}
		}
	}
}

// Nothing to offer, and a path outside the index, are one line on stderr
// and exit status 1; --strict makes every miss that, and an ambiguous
// address exit status 3 after its candidates.
func TestNothingToOfferIsOneLineAndExitOne(t *testing.T) {
	_, goLib := indexed(t)
	if err := os.WriteFile(filepath.Join(goLib, "go.sum"), []byte("x v1 h1:x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args   []string
		stderr string
		code   int
	}{
		{[]string{"symbol", "Zzyzxq"}, "codedoc: symbol Zzyzxq: not found\n", exitFailed},
		{[]string{"outline", "go-lib:nowhere/at/all"}, "codedoc: outline go-lib:nowhere/at/all: not found: no such file or directory in go-lib\n", exitFailed},
		{[]string{"outline", "go-lib:go.sum"}, "codedoc: go.sum is not indexed: dependency lock files are never indexed\n", exitFailed},
		{[]string{"symbol", "go-lib:go.sum#x"}, "codedoc: go.sum is not indexed: dependency lock files are never indexed\n", exitFailed},
		{[]string{"outline", "/etc/hosts"}, "codedoc: /etc/hosts: path is outside the repos of the workspace and the directories of its dependencies\n", exitFailed},
		{[]string{"symbol", "newmemstore", "--strict"}, "codedoc: symbol newmemstore: not found\n", exitFailed},
		{[]string{"outline", "go-lib:null.go", "--strict"}, "codedoc: outline go-lib:null.go: not found: no such file or directory in go-lib\n", exitFailed},
		{[]string{"symbol", "Get", "--strict", "--repo", "go-lib"}, "codedoc: symbol Get: ambiguous (4 candidates)\n", exitAmbiguous},
	} {
		stdout, stderr, code := exitOf(t, append(c.args, "--workspace", "fx")...)
		if code != c.code || stderr != c.stderr || strings.Contains(stdout, "nearest") {
			t.Errorf("%v: exit %d, stderr %q\n%s", c.args, code, stderr, stdout)
		}
	}
}

// In JSON the error object carries the exit status, after the response
// of a command that printed one.
func TestJSONCarriesTheExitStatus(t *testing.T) {
	indexed(t)
	for _, c := range []struct {
		args []string
		code int
		last string
	}{
		{[]string{"symbol", "Zzyzxq"}, exitFailed, `{"error":"symbol Zzyzxq: not found","code":1}`},
		{[]string{"symbol", "Get", "--repo", "go-lib", "--strict"}, exitAmbiguous, `{"error":"symbol Get: ambiguous (4 candidates)","code":3}`},
		{[]string{"symbol", "Get", "--budget", "x"}, exitUsage, ""},
	} {
		stdout, _, code := exitOf(t, append(c.args, "--workspace", "fx", "--format", "json")...)
		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		if code != c.code || lines[len(lines)-1] != c.last {
			t.Errorf("%v: exit %d: %s", c.args, code, stdout)
		}
	}
}

// An answered miss is a JSON response like any other: no error object.
func TestAnsweredMissInJSON(t *testing.T) {
	indexed(t)
	stdout, stderr, code := exitOf(t, "symbol", "go-lib:store/store.go#Nope", "--workspace", "fx", "--format", "json")
	var resp struct {
		Sections []struct {
			Title   string
			Entries []struct{ Key string }
		}
		Error string
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil || code != 0 || stderr != "" || resp.Error != "" {
		t.Fatalf("exit %d, %v: %s%s", code, err, stdout, stderr)
	}
	if len(resp.Sections) != 1 || resp.Sections[0].Title != "no exact match; nearest:" || len(resp.Sections[0].Entries) != 6 {
		t.Errorf("%s", stdout)
	}
}

// A write to an address that names nothing or several symbols stays an
// error of one line: exit status 1, and 3 for the ambiguous one only
// when --strict asks for it.
func TestDocSetOfANearMissIsAnError(t *testing.T) {
	_, goLib := indexed(t)
	before, err := os.ReadFile(filepath.Join(goLib, "store", "store.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args   []string
		stderr string
		code   int
	}{
		{[]string{"newmemstore"}, "codedoc: doc set newmemstore: not found; look its name up with codedoc find, or its file with codedoc outline\n", exitFailed},
		{[]string{"go-lib:store/store.go#Nope"}, "codedoc: doc set go-lib:store/store.go#Nope: not found; look its name up with codedoc find, or its file with codedoc outline\n", exitFailed},
		{[]string{"Put"}, "codedoc: doc set Put: ambiguous (3 candidates); address one by its key: ", exitFailed},
		{[]string{"Put", "--strict"}, "codedoc: doc set Put: ambiguous (3 candidates); address one by its key: ", exitAmbiguous},
	} {
		args := append([]string{"doc", "set"}, c.args...)
		stdout, stderr, code := exitOf(t, append(args, "--text", "A doc.", "--workspace", "fx", "--repo", "go-lib")...)
		if code != c.code || stdout != "" || !strings.HasPrefix(stderr, c.stderr) || strings.Count(stderr, "\n") != 1 {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", c.args, code, stdout, stderr)
		}
	}
	if after, err := os.ReadFile(filepath.Join(goLib, "store", "store.go")); err != nil || string(after) != string(before) {
		t.Errorf("nothing was written: %v\n%s", err, after)
	}
}
