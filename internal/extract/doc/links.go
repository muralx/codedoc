package doc

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
)

// inlineLink matches the target of [text](target) and of an image, with
// an optional title; refLink the target of a definition "[id]: target".
var (
	inlineLink = regexp.MustCompile(`\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)`)
	refLink    = regexp.MustCompile(`^ {0,3}\[[^\]]+\]:\s*<?([^\s>]+)>?`)
)

// links returns the relative links of Markdown lines outside fenced
// code as references of kind link: Name is the path as written,
// Qualifier the anchor, Enclosing the section owning the line. Links
// with a URL scheme, and links to an anchor of the same document, are
// left out.
func links(lines []string, from int, owners []string) []extract.Reference {
	var out []extract.Reference
	var open fence
	for i := from; i < len(lines); i++ {
		if f, ok := fenceLine(lines[i]); ok || open.n > 0 {
			open = open.next(f, ok, lines[i])
			continue
		}
		for _, target := range targets(lines[i]) {
			p, anchor, ok := relative(target)
			if !ok {
				continue
			}
			out = append(out, extract.Reference{Kind: extract.RefLink, Name: p, Qualifier: anchor,
				Enclosing: owners[i], Line: i + 1, Col: strings.Index(lines[i], target), Arity: -1})
		}
	}
	return out
}

// targets returns the link targets written on one line.
func targets(line string) []string {
	var out []string
	for _, m := range inlineLink.FindAllStringSubmatch(line, -1) {
		out = append(out, m[1])
	}
	if m := refLink.FindStringSubmatch(line); m != nil {
		out = append(out, m[1])
	}
	return out
}

// relative splits a link target into a path and an anchor; ok is false
// for a URL with a scheme or host, a mail address, and a bare anchor.
func relative(target string) (p, anchor string, ok bool) {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" {
		return "", "", false
	}
	return u.Path, u.Fragment, true
}
