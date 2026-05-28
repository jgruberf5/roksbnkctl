package diameter

import (
	"io/fs"
	"testing"
)

func TestManifestFS(t *testing.T) {
	expected := []string{
		"manifests/01-namespace.yaml",
		"manifests/02-f5bnkgateway.yaml",
		"manifests/03-backend.yaml",
		"manifests/06-gateway.yaml",
		"manifests/07-l4route.yaml",
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

func TestClientFS(t *testing.T) {
	expected := []string{
		"diameter_client.py",
		"responder.py",
	}

	fsys := ClientFS()
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
