package cfg

import "testing"

func TestProperties(t *testing.T) {
	runCases(t, Properties, []extractCase{
		{name: "separators", src: "# comment\n! also\n\nlib.mode=dev\napi.url : ${API_URL}\nspaced value here\nempty=\n",
			defs: []KeyDef{{"lib.mode", 4, "dev"}, {"api.url", 5, "${API_URL}"}, {"spaced", 6, "value here"}, {"empty", 7, ""}},
			refs: []KeyRef{{"API_URL", 5}}},
		{name: "continuation", src: "list=a,\\\n    b,\\\n    ${C}\nnext=1\n",
			defs: []KeyDef{{"list", 1, "a,b,${C}"}, {"next", 4, "1"}},
			refs: []KeyRef{{"C", 3}}},
		{name: "escapes and masking", src: "a\\:b=1\nends\\\\=x\ndb.password=hunter2\n",
			defs: []KeyDef{{"a:b", 1, "1"}, {`ends\`, 2, "x"}, {"db.password", 3, "***"}}},
		{name: "long value cut", src: "k=0123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890\n",
			defs: []KeyDef{{"k", 1, "01234567890123456789012345678901234567890123456789012345678901234567890123456789"}}},
	})
}

func TestPropertiesEscapes(t *testing.T) {
	runCases(t, Properties, []extractCase{
		{name: "key escapes", src: "a\\ b\\:c\\=d=1\nuni\\u00e9=\\u0041\\u00e9\nback\\\\slash=x\n",
			defs: []KeyDef{{"a b:c=d", 1, "1"}, {"unié", 2, "Aé"}, {`back\slash`, 3, "x"}}},
		{name: "value escapes", src: "tabs=a\\tb\nlines=a\\nb\nother=\\q\\:\\=\\ x\ncut=\\u00\n",
			defs: []KeyDef{{"tabs", 1, "a b"}, {"lines", 2, "a b"}, {"other", 3, "q:= x"}, {"cut", 4, `\u00`}}},
	})
}
