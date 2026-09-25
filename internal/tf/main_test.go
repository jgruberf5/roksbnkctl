package tf

import (
	"os"
	"testing"
)

// The terraform console harness keeps one initialised copy of each module it
// evaluates, because `terraform init` costs 16-65s per module on this checkout
// and running it per case put the package over go test's 10m timeout (#282).
// Those copies deliberately outlive the test that created them, so they cannot
// be t.TempDir()s — this removes them once the package is done.
func TestMain(m *testing.M) {
	code := m.Run()
	consoleTempDirsRemove()
	os.Exit(code)
}
