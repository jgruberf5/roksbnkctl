package http2

import (
	"io/fs"
	"testing"
)

func TestManifestFS(t *testing.T) {
	expected := []string{
		"manifests/01-namespace.yaml",
		"manifests/02-f5bnkgateway.yaml",
		"manifests/03-backend.yaml",
		"manifests/04-gateway.yaml",
		"manifests/05-httproute.yaml",
	}

	fsys := ManifestFS()
	for _, name := range expected {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			t.Errorf("ReadFile(%q): %v", name, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("file %q is empty", name)
		}
	}
}
