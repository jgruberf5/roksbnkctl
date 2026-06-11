package mirror

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"

	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
	"github.com/jgruberf5/roksbnkctl/internal/registry/ocireg"
)

// TestEngineDelete pushes a real image into an in-memory registry, then deletes
// it via Engine.Delete (by digest) and confirms it's gone.
func TestEngineDelete(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	tgt := &ocireg.Target{Host: host, Namespace: "ns", Auth: authn.Anonymous}
	a := bnkbom.Artifact{Name: "images/x", Tag: "v1"}
	pushRef := tgt.PushRef(a)

	img, err := random.Image(1024, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := crane.Push(img, pushRef, crane.Insecure); err != nil {
		t.Fatalf("seed push: %v", err)
	}
	dig, err := crane.Digest(pushRef, crane.Insecure)
	if err != nil {
		t.Fatalf("seed digest: %v", err)
	}

	eng := &Engine{Target: tgt, Insecure: true}
	a.Digest = dig
	results := eng.Delete(context.Background(), []bnkbom.Artifact{a})
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Err != nil {
		t.Fatalf("delete error: %v", results[0].Err)
	}

	// The manifest is gone by digest. (The in-memory test registry untags
	// lazily — it removes the digest entry but keeps the tag entry — so verify
	// by digest, which is what the engine deleted; a real registry also 404s the
	// tag.)
	byDigest := tgt.Host + "/ns/images/x@" + dig
	if _, err := crane.Head(byDigest, crane.Insecure); err == nil {
		t.Error("manifest still present by digest after Delete")
	}
}
