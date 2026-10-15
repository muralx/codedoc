package cfg

import "testing"

func TestEnv(t *testing.T) {
	runCases(t, Env, []extractCase{
		{name: "plain and export", src: "# c\nLIB_MODE=dev\nexport API_URL=\"https://x\"\nURL2=$API_URL/v1 # note\nMIX=${A:-d}-$B\nLIT='$NOPE'\nnot a line\n",
			defs: []KeyDef{{"LIB_MODE", 2, "dev"}, {"API_URL", 3, "https://x"}, {"URL2", 4, "$API_URL/v1"}, {"MIX", 5, "${A:-d}-$B"}, {"LIT", 6, "$NOPE"}},
			refs: []KeyRef{{"API_URL", 4}, {"A", 5}, {"B", 5}}},
		{name: "secret", src: "GITHUB_TOKEN=abc\nSECRET_KEY_BASE=x\n",
			defs: []KeyDef{{"GITHUB_TOKEN", 1, "***"}, {"SECRET_KEY_BASE", 2, "***"}}},
	})
}
