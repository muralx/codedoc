package write

import (
	"bytes"
	"os"
)

// readSource reads a file; a file whose lines mostly end in CRLF comes
// back with LF line ends, so the writers work on one form (Plan restores
// CRLF on the result).
func readSource(abs string) ([]byte, error) {
	src, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	lf, _ := toLF(src)
	return lf, nil
}

// toLF returns src with CRLF line ends replaced by LF when CRLF is the
// dominant line end (more than half of the lines), and whether it was.
// Otherwise src is returned unchanged.
func toLF(src []byte) ([]byte, bool) {
	crlf := bytes.Count(src, []byte("\r\n"))
	if crlf == 0 || 2*crlf <= bytes.Count(src, []byte("\n")) {
		return src, false
	}
	return bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n")), true
}

// toCRLF ends every line of an LF source with CRLF.
func toCRLF(src []byte) []byte {
	return bytes.ReplaceAll(bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n")), []byte("\n"), []byte("\r\n"))
}
