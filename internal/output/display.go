package output

import (
	"regexp"
	"slices"
	"strings"
)

// locRange splits a location into its path and its line range:
// "a/b.go:12-20" is "a/b.go" and "12-20". A location without a line
// (a directory, a file) has no range.
var locRange = regexp.MustCompile(`^(.*):(\d+(?:-\d+)?)$`)

// splitLoc returns the path of a location and its line range, "" when
// it has none.
func splitLoc(loc string) (path, lines string) {
	if m := locRange.FindStringSubmatch(loc); m != nil {
		return m[1], m[2]
	}
	return loc, ""
}

// candidateCount matches the tag counting the candidates of an
// ambiguous edge: "1 of 2".
var candidateCount = regexp.MustCompile(`^\d+ of \d+$`)

// essentialTag reports whether a tag changes what the reader does:
// ambiguous (with its candidate count), test, stale, a step of a call
// tree that is not expanded (cycle, see above, see below), a dependency
// version or the count of members folded into an entry: the tags text
// always prints.
func essentialTag(tag string) bool {
	switch tag {
	case "ambiguous", "test", "stale", "dep", "cycle", "see above", "see below":
		return true
	}
	return strings.HasPrefix(tag, "dep ") || strings.HasPrefix(tag, "+") || candidateCount.MatchString(tag)
}

// display returns the entries of s as text prints them: the name in
// place of the stable key (unless keys are asked for, or two entries
// would read the same), only the tags that matter, the hidden parts
// blanked, a call site in place of the signature, a dependency tag only
// where it changes, and the path printed once per file: an entry in the
// file of the section, or in a run of entries of one file, keeps only
// its line range, and the first of a run is led by a line holding the
// path. A tree outside one file keeps its locations whole.
func (t *textWriter) display(s Section) []Entry {
	out := make([]Entry, len(s.Entries))
	for i, e := range s.Entries {
		out[i] = t.plain(e, s)
	}
	distinguish(out, s.Entries)
	deps := depTags(out)
	dropRepeatedDeps(out, deps)
	switch {
	case s.Dirs:
		groupByDir(out)
	case !tree(out) || s.Path != "":
		groupByFile(out, deps, s.Path)
	}
	return out
}

// depTags returns the dependency tag of each entry, "" for an entry of
// the workspace.
func depTags(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		for _, tag := range e.Tags {
			if strings.HasPrefix(tag, "dep ") {
				out[i] = tag
			}
		}
	}
	return out
}

// plain is e with its name for a key, its mark before it, and without
// the parts and tags the section or the entry hides.
func (t *textWriter) plain(e Entry, s Section) Entry {
	if e.Name != "" && !t.keys {
		e.Key = e.Name
	}
	e.Key = marked(e.Mark, e.Key)
	if e.Mark != "" {
		e.Tags = slices.DeleteFunc(slices.Clone(e.Tags), func(tag string) bool { return tag == "ambiguous" })
	}
	if t.detail {
		return e
	}
	hide := s.Hide | e.Hide
	if e.Site && len(e.Fragments) > 0 {
		hide |= HideText | HideDoc
	}
	if hide&HideText != 0 {
		e.Text = ""
	}
	if hide&HideDoc != 0 {
		e.Doc = ""
	}
	if line, ok := oneLine(e.Fragments); ok && e.Site {
		e.Text, e.Fragments = line, nil
	}
	if !s.AllTags {
		e.Tags = slices.DeleteFunc(slices.Clone(e.Tags), func(tag string) bool { return !essentialTag(tag) })
	}
	return e
}

// oneLine returns the text of fragments that are one line of the file
// of the entry, without its indentation.
func oneLine(fs []Fragment) (string, bool) {
	if len(fs) != 1 || fs[0].Path != "" || fs[0].End > fs[0].Start {
		return "", false
	}
	return strings.TrimSpace(fs[0].Text), true
}

// marked puts the mark of an entry before its name.
func marked(mark, name string) string {
	if mark == "" || name == "" {
		return mark + name
	}
	return mark + " " + name
}

// distinguish tells apart the entries that would print the same line as
// another although they are different symbols: each gets the shortest
// part of its stable key that differs among them before its name (the
// repo, else the module), else the key itself.
func distinguish(out, orig []Entry) {
	same := map[string][]int{}
	for i, e := range out {
		line := entryText(e, false, true)
		same[line] = append(same[line], i)
	}
	for _, group := range same {
		if len(group) < 2 || sameKey(orig, group) {
			continue
		}
		prefix := distinctPrefix(orig, group)
		for _, i := range group {
			switch {
			case orig[i].Key == "" || out[i].Key == marked(orig[i].Mark, orig[i].Key):
			case prefix[i] != "":
				out[i].Key = marked(orig[i].Mark, prefix[i]+":"+orig[i].Name)
			default:
				out[i].Key = marked(orig[i].Mark, orig[i].Key)
			}
		}
	}
}

// sameKey reports whether the entries of a group are one symbol listed
// several times (a callee at several places of a call tree): no part of
// its key tells those lines apart.
func sameKey(entries []Entry, group []int) bool {
	for _, i := range group[1:] {
		if entries[i].Key != entries[group[0]].Key {
			return false
		}
	}
	return true
}

// distinctPrefix returns, for the entries of a group, the first segment
// of their stable keys (the repo or package version, then the module) that is different for
// each of them; none when no segment tells them all apart.
func distinctPrefix(entries []Entry, group []int) map[int]string {
	for _, seg := range []int{0, 2} {
		out := map[int]string{}
		seen := map[string]bool{}
		for _, i := range group {
			parts := strings.SplitN(entries[i].Key, ":", 4)
			if len(parts) < 4 || seen[parts[seg]] {
				break
			}
			seen[parts[seg]] = true
			out[i] = parts[seg]
		}
		if len(out) == len(group) {
			return out
		}
	}
	return map[int]string{}
}

// dropRepeatedDeps removes the dependency tag (deps holds them) of an
// entry that follows one with the same tag: a package version is named
// once per group. Only a list whose dependency entries come last is
// grouped so: where workspace entries follow them (a search), an entry
// without the tag would read as one of the workspace.
func dropRepeatedDeps(out []Entry, deps []string) {
	first := slices.IndexFunc(deps, func(dep string) bool { return dep != "" })
	if first < 0 || slices.Contains(deps[first:], "") {
		return
	}
	for i := first + 1; i < len(out); i++ {
		if dep := deps[i]; dep == deps[i-1] {
			out[i].Tags = slices.DeleteFunc(slices.Clone(out[i].Tags), func(tag string) bool { return tag == dep })
		}
	}
}

// tree reports whether entries nest.
func tree(entries []Entry) bool {
	return slices.ContainsFunc(entries, func(e Entry) bool { return e.Depth > 0 })
}

// groupByFile shortens the location of every entry in the file named
// path to ":lines", and that of each run of two or more entries of one
// other file likewise, indenting them under a line with the path. A
// path is relative to its package for a dependency (deps holds the tag
// of each entry), so a run never spans two packages.
func groupByFile(out []Entry, deps []string, path string) {
	for i := 0; i < len(out); {
		p, lines := splitLoc(out[i].Loc)
		j := i + 1
		for j < len(out) && sameFile(out[j].Loc, p) && deps[j] == deps[i] {
			j++
		}
		switch {
		case lines == "" || p == "":
		case p == path:
			shorten(out[i:j], 0)
		case j-i > 1 && path == "":
			out[i].lead = p + "\n"
			shorten(out[i:j], 1)
		}
		i = j
	}
}

// sameFile reports whether a location with a line range is in file p.
func sameFile(loc, p string) bool {
	q, lines := splitLoc(loc)
	return lines != "" && q == p
}

// shorten replaces the locations of entries of one file by their line
// ranges and indents them by depth levels.
func shorten(run []Entry, depth int) {
	for i := range run {
		_, lines := splitLoc(run[i].Loc)
		run[i].Loc = ":" + lines
		run[i].Depth += depth
	}
}

// groupByDir prints the directory of each run of two or more entries
// that are files of one directory once, on a line of its own, and their
// names under it.
func groupByDir(out []Entry) {
	for i := 0; i < len(out); {
		dir := dirOf(out[i].Loc)
		j := i + 1
		for j < len(out) && dirOf(out[j].Loc) == dir {
			j++
		}
		if j-i > 1 && dir != "" {
			out[i].lead = dir + "\n"
			for k := i; k < j; k++ {
				out[k].Loc = out[k].Loc[len(dir):]
				out[k].Depth++
			}
		}
		i = j
	}
}

// dirOf is the directory of a file location with its closing slash, ""
// for a file at the root.
func dirOf(loc string) string {
	return loc[:strings.LastIndex(loc, "/")+1]
}
