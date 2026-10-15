// Package buildinfo holds facts about the running codedoc build that
// internal packages need, such as the daemon telling a client of
// another build apart. The main package sets them at startup.
package buildinfo

import "runtime/debug"

// Version is the build's version (see Resolve); "dev" when nothing
// names it.
var Version = "dev"

// Resolve returns the version of this build: ldflag, the value make
// build and make install set with -ldflags "-X main.version=...", unless
// it is empty or "dev"; else the module version go install stamps, or
// the VCS revision go build stamps in a git checkout (with "-dirty" for
// uncommitted changes); else "dev".
func Resolve(ldflag string) string {
	if ldflag != "" && ldflag != "dev" {
		return ldflag
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	return fromBuildInfo(info)
}

// fromBuildInfo is the version go stamped into a binary, or "dev".
func fromBuildInfo(info *debug.BuildInfo) string {
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	var rev string
	dirty := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value[:min(len(s.Value), 7)]
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	switch {
	case rev == "":
		return "dev"
	case dirty:
		return rev + "-dirty"
	}
	return rev
}
