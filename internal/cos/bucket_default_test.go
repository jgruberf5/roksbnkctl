// Sprint 18 staff Issue 1 — additive default-tag coverage for the new
// `cos bucket get` recursive-download verb.
//
// The validator's hermetic suite (`bucket_get_test.go`) is gated by the
// `sprint18_validator` build tag so it stays hidden until the integrator
// runs `go test -tags=sprint18_validator ./internal/cos/`. To keep the
// default `go test ./...` doing real work on this code path *without*
// editing that pre-existing file (Sprint 18 constraint), staff drops a
// parallel, smaller suite here that:
//
//   - exercises the package-level GetBucket entry point end-to-end via
//     an in-memory fake (sha256 round-trip + nested keys + counters);
//   - asserts the safeLocalPath traversal guard staff added on top of
//     the validator's acceptance grid; and
//   - asserts the .part-file cleanup on transport failure so a flaky
//     network leaves no half-written debris.
//
// No edits to bucket_get_test.go or cos_test.go — strictly additive.

package cos

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// staffFakeCOS is an intentionally minimal in-memory bucket the
// default-tag suite drives GetBucket through. Distinct from the
// validator's `fakeCOS` so the two suites can't accidentally couple.
type staffFakeCOS struct {
	objects     map[string][]byte
	downloadErr map[string]error
}

func (f *staffFakeCOS) listFn(_ context.Context, _ string) ([]ObjectInfo, error) {
	keys := make([]string, 0, len(f.objects))
	for k := range f.objects {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic for assertion stability
	out := make([]ObjectInfo, 0, len(keys))
	for _, k := range keys {
		out = append(out, ObjectInfo{
			Key:          k,
			Size:         int64(len(f.objects[k])),
			LastModified: time.Unix(1700000000, 0).UTC(),
		})
	}
	return out, nil
}

func (f *staffFakeCOS) getFn(_ context.Context, _ /*bucket*/, key, localPath string) error {
	if err, ok := f.downloadErr[key]; ok && err != nil {
		return err
	}
	data, ok := f.objects[key]
	if !ok {
		return errors.New("staffFake: no such key " + key)
	}
	// Mirror Client.GetObjectToFile's create+truncate semantics.
	return os.WriteFile(localPath, data, 0o644)
}

// optsFor wires a staffFakeCOS into a GetBucketOptions; the test layer
// adds NoClobber / OnItem on top.
func (f *staffFakeCOS) optsFor() GetBucketOptions {
	return GetBucketOptions{ListFn: f.listFn, GetFn: f.getFn}
}

// TestGetBucket_StaffDefault_BinarySha256RoundTrip mirrors acceptance
// criterion 2 (binary byte-identical) but runs on the default build tag
// so plain `go test ./...` exercises it. The validator's tagged suite
// has its own version; both are intentional belt-and-braces.
func TestGetBucket_StaffDefault_BinarySha256RoundTrip(t *testing.T) {
	payload := make([]byte, 128*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("rand: %v", err)
	}
	want := sha256.Sum256(payload)

	f := &staffFakeCOS{objects: map[string][]byte{
		"archives/payload.bin": payload,
	}}
	dest := t.TempDir()

	counts, err := GetBucket(context.Background(), "fake-instance", "b",
		dest, f.optsFor())
	if err != nil {
		t.Fatalf("GetBucket: %v", err)
	}
	if counts.Objects != 1 || counts.Bytes != int64(len(payload)) {
		t.Errorf("counts = %+v; want Objects=1 Bytes=%d", counts, len(payload))
	}

	got, err := os.ReadFile(filepath.Join(dest, "archives", "payload.bin"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	gotSum := sha256.Sum256(got)
	if gotSum != want {
		t.Error("sha256 mismatch: bucket-get is not byte-identical for binary objects")
	}
}

// TestGetBucket_StaffDefault_NestedKeysAndCounters proves the
// key/path mapping + per-item OnItem fan-out without depending on the
// validator's tagged file.
func TestGetBucket_StaffDefault_NestedKeysAndCounters(t *testing.T) {
	f := &staffFakeCOS{objects: map[string][]byte{
		"top.txt":          []byte("hello"),
		"foo/bar/baz.json": []byte(`{"k":"v"}`),
		"a/b/c/d.bin":      {0x00, 0x01, 0x02, 0x03},
	}}
	dest := t.TempDir()
	opts := f.optsFor()

	var seen []GetBucketItem
	opts.OnItem = func(it GetBucketItem) { seen = append(seen, it) }

	counts, err := GetBucket(context.Background(), "fake-instance", "b", dest, opts)
	if err != nil {
		t.Fatalf("GetBucket: %v", err)
	}
	if counts.Objects != 3 {
		t.Errorf("counts.Objects = %d, want 3", counts.Objects)
	}
	if len(seen) != 3 {
		t.Errorf("OnItem fired %d times, want 3", len(seen))
	}
	for _, rel := range []string{
		"top.txt",
		filepath.Join("foo", "bar", "baz.json"),
		filepath.Join("a", "b", "c", "d.bin"),
	} {
		if _, err := os.Stat(filepath.Join(dest, rel)); err != nil {
			t.Errorf("missing expected file %s: %v", rel, err)
		}
	}
}

// TestGetBucket_StaffDefault_PartFileCleanedOnFailure asserts that a
// mid-download failure removes the `.part` temp rather than leaving
// debris for the next run.
func TestGetBucket_StaffDefault_PartFileCleanedOnFailure(t *testing.T) {
	boom := errors.New("simulated network blip")
	f := &staffFakeCOS{
		objects:     map[string][]byte{"k.bin": []byte("payload")},
		downloadErr: map[string]error{"k.bin": boom},
	}
	dest := t.TempDir()

	_, err := GetBucket(context.Background(), "fake-instance", "b", dest, f.optsFor())
	if !errors.Is(err, boom) {
		t.Fatalf("GetBucket err = %v, want wraps %v", err, boom)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "k.bin.part")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf(".part file not cleaned up: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "k.bin")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("partial final file survived a failed download: %v", statErr)
	}
}

// TestGetBucket_StaffDefault_TraversalRefused defends against a hostile
// bucket whose keys try to write outside destDir. Not in the spec's
// acceptance grid; this is staff's belt-and-braces.
func TestGetBucket_StaffDefault_TraversalRefused(t *testing.T) {
	f := &staffFakeCOS{objects: map[string][]byte{
		"../escape.txt": []byte("nope"),
	}}
	dest := t.TempDir()
	_, err := GetBucket(context.Background(), "fake-instance", "b", dest, f.optsFor())
	if err == nil {
		t.Fatal("traversal key accepted; want refusal")
	}
	if !strings.Contains(err.Error(), "outside") {
		t.Errorf("error %q does not mention out-of-destDir refusal", err)
	}
}

// TestSafeLocalPath unit-tests the path-resolution helper in isolation
// so the security property doesn't ride on integration tests alone.
func TestSafeLocalPath(t *testing.T) {
	cases := []struct {
		name    string
		dest    string
		key     string
		wantErr bool
	}{
		{"simple", "/tmp/out", "a.txt", false},
		{"nested", "/tmp/out", "foo/bar.txt", false},
		{"empty key", "/tmp/out", "", true},
		{"traversal", "/tmp/out", "../etc/passwd", true},
		{"sneaky traversal", "/tmp/out", "foo/../../etc/passwd", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := safeLocalPath(c.dest, c.key)
			if (err != nil) != c.wantErr {
				t.Errorf("safeLocalPath(%q,%q) err=%v, wantErr=%v",
					c.dest, c.key, err, c.wantErr)
			}
		})
	}
}

// TestGetBucket_StaffDefault_MissingInstance covers acceptance criterion
// 6 at the library layer (default tag): empty instance returns a typed
// error before any seam call. The validator's tagged suite has the same
// shape — staff replicates here so plain `go test ./...` enforces it.
func TestGetBucket_StaffDefault_MissingInstance(t *testing.T) {
	dest := t.TempDir()
	opts := GetBucketOptions{
		ListFn: func(context.Context, string) ([]ObjectInfo, error) {
			t.Fatal("ListFn must not be called when instance is empty")
			return nil, nil
		},
		GetFn: func(context.Context, string, string, string) error {
			t.Fatal("GetFn must not be called when instance is empty")
			return nil
		},
	}
	_, err := GetBucket(context.Background(), "", "b", dest, opts)
	if err == nil {
		t.Fatal("empty instance: expected error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "instance") {
		t.Errorf("error %q does not mention 'instance'", err)
	}
}

// TestGetBucket_StaffDefault_NoClobber matches acceptance criterion 4
// at the library layer on the default tag.
func TestGetBucket_StaffDefault_NoClobber(t *testing.T) {
	f := &staffFakeCOS{objects: map[string][]byte{
		"keepme.txt": []byte("from-bucket"),
		"fresh.txt":  []byte("brand new"),
	}}
	dest := t.TempDir()

	preExisting := filepath.Join(dest, "keepme.txt")
	if err := os.WriteFile(preExisting, []byte("local-original"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	oldTime := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(preExisting, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	opts := f.optsFor()
	opts.NoClobber = true
	counts, err := GetBucket(context.Background(), "fake-instance", "b", dest, opts)
	if err != nil {
		t.Fatalf("GetBucket: %v", err)
	}
	if counts.Skipped != 1 {
		t.Errorf("counts.Skipped = %d, want 1", counts.Skipped)
	}
	if counts.Objects != 1 {
		t.Errorf("counts.Objects = %d, want 1", counts.Objects)
	}
	got, err := os.ReadFile(preExisting)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "local-original" {
		t.Errorf("pre-existing file was overwritten: %q", got)
	}
	info, err := os.Stat(preExisting)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.ModTime().Equal(oldTime) {
		t.Errorf("mtime drifted: got %v, want %v", info.ModTime(), oldTime)
	}
}
