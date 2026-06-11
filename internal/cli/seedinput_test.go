package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

func TestResolveSeedInput_LocalPath(t *testing.T) {
	dir := t.TempDir()
	src := dir + "/seed.yaml"
	if err := os.WriteFile(src, []byte("prefix: local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, cleanup, err := resolveSeedInput(src)
	if err != nil {
		t.Fatalf("resolveSeedInput: %v", err)
	}
	defer cleanup()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading resolved path: %v", err)
	}
	if string(b) != "prefix: local\n" {
		t.Fatalf("content = %q", b)
	}
}

func TestResolveSeedInput_URL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "prefix: hello\n")
	}))
	defer srv.Close()

	p, cleanup, err := resolveSeedInput(srv.URL + "/config.yaml")
	if err != nil {
		t.Fatalf("resolveSeedInput(url): %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading fetched temp: %v", err)
	}
	if string(b) != "prefix: hello\n" {
		t.Fatalf("content = %q", b)
	}
	cleanup()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("cleanup did not remove the temp file %s", p)
	}
}

func TestResolveSeedInput_URL404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	if _, _, err := resolveSeedInput(srv.URL); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("want a 404 error, got %v", err)
	}
}

func TestResolveSeedInput_URLOversize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, seedMaxBytes+10))
	}))
	defer srv.Close()
	if _, _, err := resolveSeedInput(srv.URL); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("want a size-limit error, got %v", err)
	}
}

func TestMissingRequiredConfigFields(t *testing.T) {
	complete := &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "eu-de", ResourceGroup: "default"},
		Prefix:   "p",
		TFSource: config.TFSourceCfg{Type: "embedded"},
	}
	if m := missingRequiredConfigFields(complete); len(m) != 0 {
		t.Fatalf("complete config reported missing fields: %v", m)
	}
	if m := missingRequiredConfigFields(&config.Workspace{}); len(m) != 4 {
		t.Fatalf("empty config: want 4 missing fields, got %v", m)
	}
}
