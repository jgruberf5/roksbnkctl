package mirror

import (
	"context"
	"errors"
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

// TestIsTransient pins the retry predicate: the 5xx/throttle/EOF classes the
// OpenShift registry produced, plus the concurrent-push 401 Harbor emits. A
// 404 or a 403 must NOT retry -- those are terminal.
func TestIsTransient(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"500", errors.New("unexpected status code 500 Internal Server Error"), true},
		{"503", errors.New("unexpected status code 503"), true},
		{"throttle", errors.New("TOOMANYREQUESTS: rate limited"), true},
		{"reset", errors.New("read tcp: connection reset by peer"), true},
		{"harbor 401", errors.New("HEAD https://h/v2/p/c/manifests/1: unexpected status code 401 Unauthorized"), true},
		{"404", errors.New("unexpected status code 404 Not Found"), false},
		{"403", errors.New("unexpected status code 403 Forbidden"), false},
		{"garbage", errors.New("no such host"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransient(tc.err); got != tc.want {
				t.Fatalf("isTransient(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestEngine_ProbeNamespace(t *testing.T) {
	host := startRegistry(t)
	opts := []crane.Option{crane.Insecure}

	img, err := random.Image(512, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{
		host + "/bnk-mirror/one:v1",
		host + "/bnk-mirror/two:v1",
		host + "/somewhere-else/three:v1",
	} {
		if err := crane.Push(img, ref, opts...); err != nil {
			t.Fatalf("seed %s: %v", ref, err)
		}
	}

	eng := &Engine{Target: fakeTarget{host: host, ns: "bnk-mirror"}, Insecure: true}

	// Counts only what is under the prefix — the point is to catch a prefix typo,
	// so repositories elsewhere on the same registry must not mask an empty one.
	n, err := eng.ProbeNamespace(context.Background(), "bnk-mirror")
	if err != nil {
		t.Fatalf("ProbeNamespace: %v", err)
	}
	if n != 2 {
		t.Fatalf("under bnk-mirror: got %d, want 2", n)
	}

	// A prefix nothing was pushed under reports zero rather than erroring —
	// adopt turns that into the "check registry.generic_repo_prefix" failure.
	n, err = eng.ProbeNamespace(context.Background(), "typo-mirror")
	if err != nil {
		t.Fatalf("ProbeNamespace(typo): %v", err)
	}
	if n != 0 {
		t.Fatalf("under typo-mirror: got %d, want 0", n)
	}

	// No prefix ⇒ everything the registry holds.
	n, err = eng.ProbeNamespace(context.Background(), "")
	if err != nil {
		t.Fatalf("ProbeNamespace(empty): %v", err)
	}
	if n != 3 {
		t.Fatalf("whole catalog: got %d, want 3", n)
	}
}

func TestEngine_ProbeNamespace_NoHost(t *testing.T) {
	eng := &Engine{Target: fakeTarget{host: "", ns: "bnk-mirror"}}
	if _, err := eng.ProbeNamespace(context.Background(), "bnk-mirror"); err == nil {
		t.Fatal("expected an error when the target has no push host")
	}
}
