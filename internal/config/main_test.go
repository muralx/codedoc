package config_test

import (
	"os"
	"testing"

	"github.com/muralx/codedoc/internal/testutil"
)

// TestMain keeps the tests out of the user's real directories (see
// testutil.IsolateEnv).
func TestMain(m *testing.M) {
	os.Exit(testutil.MainIsolated(m))
}
