package cfg

import "testing"

func TestShell(t *testing.T) {
	runCases(t, Shell, []extractCase{
		{name: "assignments and expansions", src: "#!/bin/sh\nexport JAVALIB_HOME=/opt/j\nMODE=\"${LIB_MODE:-dev}\"\nlocal L='x'\nA[0]=1\necho \"$API_URL $1 ${10} $@ $? ${#Z}\"\n",
			defs: []KeyDef{{"JAVALIB_HOME", 2, "/opt/j"}, {"MODE", 3, "${LIB_MODE:-dev}"}, {"L", 4, "x"}},
			refs: []KeyRef{{"LIB_MODE", 3}, {"API_URL", 6}, {"Z", 6}}},
		{name: "empty value and secret", src: "X=\nDB_PASSWORD=pw\n",
			defs: []KeyDef{{"X", 1, ""}, {"DB_PASSWORD", 2, "***"}}},
		{name: "broken script still yields keys", src: "A=1\nif then fi (\n",
			defs: []KeyDef{{"A", 1, "1"}}},
	})
}
