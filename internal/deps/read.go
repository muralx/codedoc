package deps

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxManifest caps a manifest read whole: package.json, go.mod,
// pnpm-workspace.yaml. Real ones are a few kilobytes.
const maxManifest = 4 << 20

// errOutside reports a file of a package that resolves, through
// symbolic links, to somewhere outside the package's directory.
var errOutside = errors.New("resolves outside the package")

// readCapped reads the regular file at path whole when it is at most
// limit bytes; a larger file is an error, not a truncated read.
func readCapped(path string, limit int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", path)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%s: %d bytes, over the %d byte limit", path, info.Size(), limit)
	}
	return os.ReadFile(path)
}

// readHead reads at most limit bytes from the start of the file at path.
func readHead(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, limit))
}

// readJSON decodes the JSON file at path, at most maxManifest bytes,
// into v.
func readJSON(path string, v any) error {
	raw, err := readCapped(path, maxManifest)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// within returns the real path of the file rel below the package
// directory root when, symbolic links resolved on both sides, it still
// lies inside root: a package may link its files anywhere, and nothing
// outside its own directory is read on its behalf (deps spec section
// 5). rel is slash separated.
func within(root, rel string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	if resolved != realRoot && !strings.HasPrefix(resolved, realRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("%s: %w", rel, errOutside)
	}
	return resolved, nil
}
