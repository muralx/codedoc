package cfg

import "testing"

func TestTemplate(t *testing.T) {
	runCases(t, Template, []extractCase{
		{name: "jsp", src: "<a href=\"${API_URL}\">\n${lib.mode:dev} {{ ignored }}\n</a>\n",
			refs: []KeyRef{{"API_URL", 1}, {"lib.mode", 2}}},
		{name: "none", src: "plain text"},
	})
}
