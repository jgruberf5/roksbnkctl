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
)

// fakeTarget mirrors into "<host>/<ns>/<name>:<tag>".
type fakeTarget struct{ host, ns string }

func (f fakeTarget) PushRef(a bnkbom.Artifact) string {
	return f.host + "/" + f.ns + "/" + a.Name + ":" + a.Tag
}
func (f fakeTarget) PushHost() string              { return f.host }
func (f fakeTarget) PushAuth() authn.Authenticator { return authn.Anonymous }

// startRegistry spins up an in-process OCI registry and returns its host:port.
func startRegistry(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestEngine_Replicate_CopiesAndIsIdempotent(t *testing.T) {
	host := startRegistry(t)
	opts := []crane.Option{crane.Insecure}

	// Seed a source image at <host>/src/img:v1.
	img, err := random.Image(1024, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := crane.Push(img, host+"/src/img:v1", opts...); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	srcDigest, err := crane.Digest(host+"/src/img:v1", opts...)
	if err != nil {
		t.Fatal(err)
	}

	bom := &bnkbom.BOM{Artifacts: []bnkbom.Artifact{
		{Kind: bnkbom.KindImage, SourceHost: host, Name: "src/img", Tag: "v1", Origin: bnkbom.OriginManifest},
	}}
	eng := &Engine{Target: fakeTarget{host: host, ns: "mirror"}, Insecure: true, Concurrency: 2}

	// First run copies.
	res := eng.Replicate(context.Background(), bom)
	if len(res) != 1 || res[0].Err != nil {
		t.Fatalf("Replicate: %+v", res)
	}
	if res[0].Skipped {
		t.Error("first run should not skip")
	}
	dstDigest, err := crane.Digest(host+"/mirror/src/img:v1", opts...)
	if err != nil {
		t.Fatalf("destination missing after copy: %v", err)
	}
	if dstDigest != srcDigest {
		t.Errorf("digest mismatch: src %s dst %s", srcDigest, dstDigest)
	}

	// Second run is a no-op (idempotent by digest).
	res2 := eng.Replicate(context.Background(), bom)
	if res2[0].Err != nil || !res2[0].Skipped {
		t.Errorf("second run should skip, got %+v", res2[0])
	}

	// Verify is clean.
	if bad := eng.Verify(context.Background(), bom); len(bad) != 0 {
		t.Errorf("Verify reported %d bad: %+v", len(bad), bad)
	}
}

func TestEngine_Verify_FlagsMissing(t *testing.T) {
	host := startRegistry(t)
	bom := &bnkbom.BOM{Artifacts: []bnkbom.Artifact{
		{Kind: bnkbom.KindImage, SourceHost: host, Name: "src/img", Tag: "v1"},
	}}
	// Seed only the source; never mirror it.
	img, _ := random.Image(512, 1)
	if err := crane.Push(img, host+"/src/img:v1", crane.Insecure); err != nil {
		t.Fatal(err)
	}
	eng := &Engine{Target: fakeTarget{host: host, ns: "mirror"}, Insecure: true}
	bad := eng.Verify(context.Background(), bom)
	if len(bad) != 1 {
		t.Fatalf("Verify: want 1 missing, got %d", len(bad))
	}
}

func TestOCIDir(t *testing.T) {
	got := ociDir("registry.example.com/bnk-mirror/charts/cert-manager:v1.17.3")
	want := "oci://registry.example.com/bnk-mirror/charts"
	if got != want {
		t.Errorf("ociDir = %q, want %q", got, want)
	}
}
