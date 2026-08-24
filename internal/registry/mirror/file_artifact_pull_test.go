package mirror

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
)

// #185. Replicating the bundle into the mirror is only half of it — the install
// has to read it back out, from a disconnected cluster, and end up with the same
// bytes the pin describes. This drives that whole path through a real in-process
// registry: HTTP source, crane push, crane pull, tar extraction, pin check.
func TestPullFileReadsBackWhatCopyFilePushed(t *testing.T) {
	payload := []byte("apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: x\n")
	sum := sha256.Sum256(payload)
	pin := hex.EncodeToString(sum[:])

	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	t.Cleanup(src.Close)

	host := startRegistry(t)
	eng := &Engine{Target: fakeTarget{host: host, ns: "bnk"}}
	art := bnkbom.Artifact{
		Kind:      bnkbom.KindFile,
		Name:      bnkbom.GatewayAPIBundleName,
		Tag:       "v1.5.0",
		SourceURL: src.URL + "/" + bnkbom.GatewayAPIBundleFile,
		SHA256:    pin,
	}
	res := eng.copyOne(context.Background(), art)
	if res.Err != nil {
		t.Fatalf("copy into the mirror: %v", res.Err)
	}

	ref := host + "/bnk/" + art.Name + ":" + art.Tag
	got, err := PullFile(context.Background(), ref, bnkbom.GatewayAPIBundleFile, pin,
		PullFileOptions(context.Background(), host, nil, "", true)...)
	if err != nil {
		t.Fatalf("pull back out of the mirror: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("the bytes changed in transit\n got %q\nwant %q", got, payload)
	}

	// By digest as well as by tag — that is the form the install prefers, because
	// a tag can be moved under it.
	byDigest := host + "/bnk/" + art.Name + "@" + res.Digest
	if _, err := PullFile(context.Background(), byDigest, bnkbom.GatewayAPIBundleFile, pin,
		PullFileOptions(context.Background(), host, nil, "", true)...); err != nil {
		t.Errorf("pull by digest %s: %v", byDigest, err)
	}
}

// A mirror is a place other people can write to. The pin is checked on the way
// OUT as well as on the way in, because these bytes are applied to a cluster
// with --force-conflicts, and a guarantee that expires on arrival is not one.
func TestPullFileRefusesAMirrorThatHoldsDifferentBytes(t *testing.T) {
	reviewed := []byte("kind: CustomResourceDefinition\n")
	sum := sha256.Sum256(reviewed)
	pin := hex.EncodeToString(sum[:])

	host := startRegistry(t)
	ref := host + "/bnk/files/gateway-api-standard-install:v1.5.0"

	// Someone else pushes a different file under the same reference.
	tampered, err := crane.Image(map[string][]byte{"standard-install.yaml": []byte("kind: Something\n")})
	if err != nil {
		t.Fatal(err)
	}
	if err := crane.Push(tampered, ref, crane.Insecure); err != nil {
		t.Fatal(err)
	}

	if _, err := PullFile(context.Background(), ref, "standard-install.yaml", pin,
		PullFileOptions(context.Background(), host, nil, "", true)...); err == nil {
		t.Fatal("content that does not match the pin was accepted; it would have been applied to the cluster")
	} else if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// An unpinned pull is refused outright rather than trusted, mirroring the
// refusal on the push side. Without this, an artifact recorded before the pin
// existed would be applied unverified.
func TestPullFileRefusesToRunWithoutAPin(t *testing.T) {
	_, err := PullFile(context.Background(), "example.com/x:1", "f.yaml", "")
	if err == nil {
		t.Fatal("an unpinned pull was allowed")
	}
	if !strings.Contains(err.Error(), "no sha256 pin") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// The fetch-and-verify used by the no-mirror path must reject bad content
// before returning it — it is the same guarantee the mirrored path gets, and
// the point of having one implementation is that neither path can be weaker.
func TestFetchAndVerifyFileRefusesUnpinnedAndMismatchedContent(t *testing.T) {
	body := []byte("kind: CustomResourceDefinition\n")
	sum := sha256.Sum256(body)
	good := hex.EncodeToString(sum[:])

	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	t.Cleanup(src.Close)

	got, err := FetchAndVerifyFile(context.Background(), src.URL+"/f.yaml", good)
	if err != nil {
		t.Fatalf("a correctly-pinned fetch failed: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Error("the verified fetch returned different bytes than were served")
	}

	for _, tc := range []struct{ name, pin, want string }{
		{"wrong pin", strings.Repeat("b", 64), "sha256 mismatch"},
		{"no pin", "", "no sha256 pin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := FetchAndVerifyFile(context.Background(), src.URL+"/f.yaml", tc.pin)
			if err == nil {
				t.Fatal("unverified content was returned")
			}
			if out != nil {
				t.Error("content was returned alongside the refusal; a caller ignoring the error would apply it")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say why: %v", err)
			}
		})
	}
}
