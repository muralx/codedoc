package cfg

import "testing"

func TestJSON(t *testing.T) {
	runCases(t, JSON, []extractCase{
		{name: "nested", src: "{\n  \"lib\": {\n    \"mode\": \"dev\",\n    \"n\": 3,\n    \"on\": true,\n    \"x\": null\n  },\n  \"url\": \"${API_URL}\"\n}\n",
			defs: []KeyDef{{"lib.mode", 3, "dev"}, {"lib.n", 4, "3"}, {"lib.on", 5, "true"}, {"lib.x", 6, "null"}, {"url", 8, "${API_URL}"}},
			refs: []KeyRef{{"API_URL", 8}}},
		{name: "arrays and empties", src: "{\"a\": [\"x\",\n {\"b\": 1}, [2]],\n\"e\": {}, \"f\": []}",
			defs: []KeyDef{{"a.0", 1, "x"}, {"a.1.b", 2, "1"}, {"a.2.0", 2, "2"}, {"e", 3, "{}"}, {"f", 3, "[]"}}},
		{name: "secret masked", src: `{"auth": {"apiKey": "abc", "monkey": "m"}}`,
			defs: []KeyDef{{"auth.apiKey", 1, "***"}, {"auth.monkey", 1, "m"}}},
		{name: "root array", src: `["a"]`, defs: []KeyDef{{"0", 1, "a"}}},
		{name: "scalar root", src: `"x"`},
		{name: "invalid", src: "{\"a\": 1,,}", defs: []KeyDef{{"a", 1, "1"}}, wantErr: true},
		{name: "trailing data", src: "{} {}", wantErr: true},
		{name: "truncated", src: "{\"a\": [1", defs: []KeyDef{{"a.0", 1, "1"}}, wantErr: true},
	})
}
