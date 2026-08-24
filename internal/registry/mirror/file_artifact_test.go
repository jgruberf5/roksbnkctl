package mirror

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
)

// #185. A disconnected cluster can only reach the mirror, and in the CI path
// roksbnkctl itself runs as a pod IN that cluster — so the Gateway API bundle
// has to be carried by the mirror like everything else. It rides as a
// single-layer OCI image so any registry holds it, ICR included.
//
// This drives the real path: an HTTP source, a real in-process registry, a real
// crane push, and the bytes read back out of the pushed image.
func TestCopyFileCarriesTheBundleThroughTheMirror(t *testing.T) {
	payload := []byte("apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\n")
	sum := sha256.Sum256(payload)
	good := hex.EncodeToString(sum[:])

	var served int
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served++
		w.Write(payload)
	}))
	t.Cleanup(src.Close)

	host := startRegistry(t)
	eng := &Engine{Target: fakeTarget{host: host, ns: "bnk"}}

	art := bnkbom.Artifact{
		Kind:      bnkbom.KindFile,
		Name:      "gateway-api/standard-install",
		Tag:       "v1.5.0",
		SourceURL: src.URL + "/standard-install.yaml",
		SHA256:    good,
	}

	res := eng.copyOne(context.Background(), art)
	if res.Err != nil {
		t.Fatalf("copy: %v", res.Err)
	}
	if res.Digest == "" {
		t.Error("no digest recorded for the pushed file artifact")
	}
	if served == 0 {
		t.Fatal("the source was never fetched; this test proved nothing")
	}

	// The bytes must survive the round trip, under the upstream basename.
	img, err := crane.Pull(host + "/bnk/" + art.Name + ":" + art.Tag)
	if err != nil {
		t.Fatalf("pull back: %v", err)
	}
	var buf bytes.Buffer
	if err := crane.Export(img, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	tr := tar.NewReader(&buf)
	found := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		if strings.HasSuffix(h.Name, "standard-install.yaml") {
			got, _ := io.ReadAll(tr)
			if !bytes.Equal(got, payload) {
				t.Errorf("round-tripped content differs\n got: %q\nwant: %q", got, payload)
			}
			found = true
		}
	}
	if !found {
		t.Error("standard-install.yaml is not in the pushed image; whatever pulls it back cannot find it")
	}
}

// A mirror that faithfully replicates the wrong bytes is not an improvement, so
// the pin is checked BEFORE anything is pushed.
func TestCopyFileRefusesUnverifiedContent(t *testing.T) {
	payload := []byte("some crds")
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	t.Cleanup(src.Close)

	host := startRegistry(t)
	eng := &Engine{Target: fakeTarget{host: host, ns: "bnk"}}
	base := bnkbom.Artifact{
		Kind: bnkbom.KindFile, Name: "gw/bundle", Tag: "v1",
		SourceURL: src.URL + "/b.yaml",
	}

	for _, tc := range []struct {
		name, sha, url, want string
	}{
		{"sha mismatch", strings.Repeat("a", 64), base.SourceURL, "sha256 mismatch"},
		{"no pin at all", "", base.SourceURL, "no sha256 pin"},
		{"no source url", strings.Repeat("a", 64), "", "no source_url"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := base
			a.SHA256, a.SourceURL = tc.sha, tc.url
			res := eng.copyOne(context.Background(), a)
			if res.Err == nil {
				t.Fatal("expected a refusal, got none — unverified content would have been mirrored")
			}
			if !strings.Contains(res.Err.Error(), tc.want) {
				t.Errorf("refusal does not say why: %v", res.Err)
			}
			if res.Digest != "" {
				t.Error("a digest was recorded for content that was refused")
			}
			// The guarantee that matters is that NOTHING reached the mirror. A
			// check on the returned digest alone would pass even if the push
			// happened before the pin was verified.
			if _, err := crane.Pull(host + "/bnk/" + a.Name + ":" + a.Tag); err == nil {
				t.Error("refused content was pushed to the mirror anyway — verify the pin BEFORE pushing")
			}
		})
	}
}
