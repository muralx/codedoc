package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestResolvePrefersTheLinkerFlag(t *testing.T) {
	if got := Resolve("v1.2.3"); got != "v1.2.3" {
		t.Fatal(got)
	}
	if got := Resolve(""); got == "" {
		t.Fatal("empty version")
	}
}

func TestFromBuildInfo(t *testing.T) {
	for _, c := range []struct {
		info debug.BuildInfo
		want string
	}{
		{debug.BuildInfo{Main: debug.Module{Version: "v0.3.0"}}, "v0.3.0"},
		{debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "dev"},
		{debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"}}}, "0123456"},
		{debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"},
			{Key: "vcs.modified", Value: "true"}}}, "0123456-dirty"},
	} {
		if got := fromBuildInfo(&c.info); got != c.want {
			t.Errorf("%+v: %s, want %s", c.info, got, c.want)
		}
	}
}
