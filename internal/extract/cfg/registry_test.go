package cfg

import (
	"strings"
	"testing"
)

func TestFormatForPath(t *testing.T) {
	for _, tc := range []struct{ path, head, want string }{
		{"config/app.yaml", "", YAML}, {"a.YML", "", YAML},
		{"settings.json", "", JSON}, {"web/package.json", "", ""}, {"package-lock.json", "", ""},
		{"tsconfig.build.json", "", ""}, {"deps.lock.json", "", ""}, {"composer.lock", "", ""},
		{"pom.xml", "", XML}, {"conf/app.properties", "", Properties},
		{".env", "", Env}, {"dir/.env.local", "", Env}, {"prod.env", "", Env},
		{"main.tf", "", HCL}, {"x.hcl", "", HCL}, {"prod.tfvars", "", HCL},
		{"run.sh", "", Shell}, {"run.bash", "", Shell},
		{"bin/deploy", "#!/usr/bin/env bash\nset -e\n", Shell},
		{"scripts/go", "#!/bin/sh", Shell},
		{"tool", "#!/usr/bin/env -S bash -e\n", Shell},
		{"bin/py", "#!/usr/bin/env python3\n", ""},
		{"bin/empty", "#!\n", ""}, {"bin/plain", "echo\n", ""},
		{"script.py", "#!/bin/bash\n", ""},
		{"web/index.jsp", "", Template}, {"a.tpl", "", Template}, {"a.tmpl", "", Template},
		{"a.j2", "", Template}, {"nginx.conf.template", "", Template},
		{"main.go", "", ""}, {"README", "", ""},
	} {
		if got := FormatForPath(tc.path, []byte(tc.head)); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestForEveryFormat(t *testing.T) {
	for _, f := range []string{YAML, JSON, XML, Properties, Env, HCL, Shell, Template} {
		if For(f) == nil {
			t.Errorf("no extractor for %s", f)
		}
	}
	if For("toml") != nil {
		t.Error("unknown format must have no extractor")
	}
}

func TestPreview(t *testing.T) {
	long := strings.Repeat("a", 79) + "é"
	for _, tc := range []struct{ key, value, want string }{
		{"a", "  multi\n  line\tvalue ", "multi line value"},
		{"a", long, strings.Repeat("a", 79)},
		{"db.password", "x", masked}, {"apiKey", "x", masked}, {"API_KEY", "x", masked},
		{"oauth-Secret", "x", masked}, {"access_token", "x", masked}, {"APIKEY", "x", masked},
		{"keyboard", "x", "x"}, {"monkey", "x", "x"}, {"tokens", "x", "x"},
	} {
		if got := preview(tc.key, tc.value); got != tc.want {
			t.Errorf("preview(%q, %q) = %q, want %q", tc.key, tc.value, got, tc.want)
		}
	}
}
