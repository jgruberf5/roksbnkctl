// Sprint 18 validator Issue 1 — hermetic regression for the new
// `cos bucket get` recursive-download verb staff Issue 1 introduces.
//
// Mirrors the Sprint 16 follow-up pattern: additive test, no edits to
// the pre-existing `internal/cos/cos_test.go`, sub-test names tagged
// with the staff acceptance-criterion number they cover so the `-v`
// output reads as a coverage map. Validator and staff drafted in
// parallel; the integrator's fold-in tree exposes the public symbols
// this file binds to (`GetBucket`, `GetBucketOptions`,
// `GetBucketCounts`).
//
// Test seam: the suite drives `GetBucket` through a fake COS surface
// (`fakeCOS`) implementing the two operations the recursive download
// needs — list and per-object download. The fake never opens a socket;
// the "downloads" are in-process byte copies into the test's tempdir,
// which is enough to exercise (a) the path-mapping `/`→subdir
// behaviour, (b) `--no-clobber` skip semantics, (c) the error paths
// (bad bucket, missing instance, uncreatable dest), and (d) the
// sha256 round-trip every successful download must preserve.

package cos

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// fakeObject is the in-memory backing for one COS object served by the
// fake. Keys, bytes, etag — the exact triple `GetBucket` consumes
// downstream.
type fakeObject struct {
	key   string
	bytes []byte
}

// fakeCOS is the test-only stand-in for staff's COS surface that
// `GetBucket` walks. The exact method set is intentionally minimal:
// list-everything + download-one. Both are the contract `GetBucket`
// already needs (staff Issue 1 §"Files likely touched" wires
// `GetBucket` to existing `ListObjects` + `GetObjectToFile` patterns).
//
// `listErr` and `downloadErr` let cases (e), (f) inject typed errors
// without standing up a real COS server.
type fakeCOS struct {
	bucket      string
	objects     []fakeObject
	listErr     error
	downloadErr error
}

// listObjects returns every object's metadata, sorted by key for stable
// test output. Mirrors `ListObjects(ctx, bucket, "")` semantics: empty
// prefix = every object.
func (f *fakeCOS) listObjects(_ context.Context, bucket string) ([]ObjectInfo, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if bucket != f.bucket {
		return nil, fmt.Errorf("listing %s/: bucket %q not found", bucket, bucket)
	}
	out := make([]ObjectInfo, 0, len(f.objects))
	for _, o := range f.objects {
		out = append(out, ObjectInfo{
			Key:          o.key,
			Size:         int64(len(o.bytes)),
			LastModified: time.Unix(0, 0).UTC(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// getObjectToFile is the streamed-write side: it copies the in-memory
// bytes to `localPath`, creating parent dirs the same way real
// `GetObjectToFile` won't (staff's `GetBucket` is the layer that does
// `mkdir -p`, exactly what cases (b) and (c) assert).
func (f *fakeCOS) getObjectToFile(_ context.Context, bucket, key, localPath string) error {
	if f.downloadErr != nil {
		return f.downloadErr
	}
	for _, o := range f.objects {
		if o.key == key {
			return os.WriteFile(localPath, o.bytes, 0o644)
		}
	}
	return fmt.Errorf("downloading %s/%s: not found", bucket, key)
}

// sha256Hex returns the lowercase-hex sha256 of b; used by the
// round-trip assertions for acceptance criterion #2 (binary
// byte-identical round-trip).
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// runGetBucket dispatches to the staff-exposed entry point against the
// fake surface. Centralised so the cases below stay small. The
// signature mirrors staff Issue 1 §"Files likely touched":
//
//	GetBucket(ctx, instanceID, bucket, destDir, opts) (Counts, error)
//
// Staff may legitimately rename `Counts` → `GetBucketCounts` or fold
// `instanceID` into a `Client` receiver — the integrator adapts this
// helper at fold-in time, not the body of the seven cases.
func runGetBucket(t *testing.T, f *fakeCOS, bucket, destDir string, noClobber bool) (GetBucketCounts, error) {
	t.Helper()
	opts := GetBucketOptions{
		NoClobber: noClobber,
		ListFn:    f.listObjects,
		GetFn:     f.getObjectToFile,
	}
	return GetBucket(context.Background(), "fake-instance", bucket, destDir, opts)
}

// ── (a) empty bucket → exit 0, no files ────────────────────────────
//
// Staff acceptance criterion 5: empty bucket → exit 0 + informational
// stderr; no filesystem changes. Hermetic version: zero files created
// under destDir.
func TestGetBucket_AcceptanceCriterion5_EmptyBucket(t *testing.T) {
	f := &fakeCOS{bucket: "ws-empty", objects: nil}
	dest := t.TempDir()

	counts, err := runGetBucket(t, f, "ws-empty", dest, false)
	if err != nil {
		t.Fatalf("empty bucket: unexpected error: %v", err)
	}
	if counts.Objects != 0 || counts.Bytes != 0 {
		t.Errorf("empty bucket: counts = %+v, want zeros", counts)
	}

	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("empty bucket: dest contains %d entries, want 0: %v", len(entries), entries)
	}
}

// ── (b) flat keys → flat files, sha256 round-trip ──────────────────
//
// Staff acceptance criteria 1 + 2: every object downloaded, binary
// byte-identical (sha256 before put == sha256 after get).
func TestGetBucket_AcceptanceCriteria1And2_FlatKeysSha256RoundTrip(t *testing.T) {
	textBody := []byte("hello roksbnkctl cos bucket get\n")
	binBody := make([]byte, 4096)
	for i := range binBody {
		binBody[i] = byte(i % 251) // arbitrary non-uniform binary
	}
	f := &fakeCOS{
		bucket: "ws-flat",
		objects: []fakeObject{
			{key: "alpha.txt", bytes: textBody},
			{key: "beta.bin", bytes: binBody},
		},
	}
	dest := t.TempDir()

	counts, err := runGetBucket(t, f, "ws-flat", dest, false)
	if err != nil {
		t.Fatalf("flat keys: unexpected error: %v", err)
	}
	if counts.Objects != 2 {
		t.Errorf("flat keys: counts.Objects = %d, want 2", counts.Objects)
	}

	for _, c := range []struct {
		rel  string
		want []byte
	}{
		{"alpha.txt", textBody},
		{"beta.bin", binBody},
	} {
		gotBytes, err := os.ReadFile(filepath.Join(dest, c.rel))
		if err != nil {
			t.Fatalf("read %s: %v", c.rel, err)
		}
		if sha256Hex(gotBytes) != sha256Hex(c.want) {
			t.Errorf("%s: sha256 mismatch — round-trip lost bytes (got %d bytes, want %d)",
				c.rel, len(gotBytes), len(c.want))
		}
	}
}

// ── (c) /-containing keys → nested subdirs (mkdir -p) ──────────────
//
// Staff acceptance criterion 3: a text object with `/`-containing key
// lands in the right subdirectory.
func TestGetBucket_AcceptanceCriterion3_NestedKeysMkdirP(t *testing.T) {
	deepBody := []byte("deep nesting payload\n")
	f := &fakeCOS{
		bucket: "ws-nested",
		objects: []fakeObject{
			{key: "foo/bar/baz.json", bytes: deepBody},
			{key: "foo/sibling.json", bytes: []byte(`{"x":1}` + "\n")},
		},
	}
	dest := t.TempDir()

	counts, err := runGetBucket(t, f, "ws-nested", dest, false)
	if err != nil {
		t.Fatalf("nested keys: unexpected error: %v", err)
	}
	if counts.Objects != 2 {
		t.Errorf("nested keys: counts.Objects = %d, want 2", counts.Objects)
	}

	want := filepath.Join(dest, "foo", "bar", "baz.json")
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("expected nested file %s missing: %v", want, err)
	}
	if sha256Hex(got) != sha256Hex(deepBody) {
		t.Errorf("nested file sha256 mismatch — round-trip lost bytes")
	}

	// Parent dir must exist as a dir (i.e., `mkdir -p` ran, not a
	// path-as-filename collapse).
	parentInfo, err := os.Stat(filepath.Join(dest, "foo", "bar"))
	if err != nil || !parentInfo.IsDir() {
		t.Errorf("expected foo/bar to be a directory (mkdir -p semantics): err=%v info=%v",
			err, parentInfo)
	}
}

// ── (d) --no-clobber skips an existing local file (mtime unchanged) ──
//
// Staff acceptance criterion 4: `--no-clobber` skips an object whose
// local target already exists (verified by mtime unchanged on a
// pre-existing file).
func TestGetBucket_AcceptanceCriterion4_NoClobberSkipsExisting(t *testing.T) {
	f := &fakeCOS{
		bucket: "ws-noclob",
		objects: []fakeObject{
			{key: "keep.txt", bytes: []byte("remote bytes\n")},
		},
	}
	dest := t.TempDir()

	preExisting := filepath.Join(dest, "keep.txt")
	preBytes := []byte("LOCAL pre-existing — must NOT be clobbered\n")
	if err := os.WriteFile(preExisting, preBytes, 0o644); err != nil {
		t.Fatalf("seed pre-existing file: %v", err)
	}
	// Backdate so a clobber would visibly bump mtime even within the
	// test's sub-second wall clock.
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(preExisting, past, past); err != nil {
		t.Fatalf("chtimes seed: %v", err)
	}
	preStat, err := os.Stat(preExisting)
	if err != nil {
		t.Fatalf("stat seed: %v", err)
	}

	counts, err := runGetBucket(t, f, "ws-noclob", dest, true /* noClobber */)
	if err != nil {
		t.Fatalf("--no-clobber: unexpected error: %v", err)
	}
	if counts.Skipped != 1 {
		t.Errorf("--no-clobber: counts.Skipped = %d, want 1", counts.Skipped)
	}

	postStat, err := os.Stat(preExisting)
	if err != nil {
		t.Fatalf("stat post-run: %v", err)
	}
	if !postStat.ModTime().Equal(preStat.ModTime()) {
		t.Errorf("--no-clobber: mtime changed from %v → %v (file was clobbered)",
			preStat.ModTime(), postStat.ModTime())
	}
	gotBytes, _ := os.ReadFile(preExisting)
	if string(gotBytes) != string(preBytes) {
		t.Errorf("--no-clobber: bytes mutated despite skip — content drift")
	}
}

// ── (e) --instance missing → typed error ───────────────────────────
//
// Staff acceptance criterion 6: `--instance` missing → exit non-zero
// with the same error text as the other `cos bucket` verbs. At the
// library layer that surfaces as a typed validation error returned
// before any cloud call.
func TestGetBucket_AcceptanceCriterion6_MissingInstance(t *testing.T) {
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
	_, err := GetBucket(context.Background(), "" /* empty instance */, "ws", dest, opts)
	if err == nil {
		t.Fatal("missing --instance: expected typed error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "instance") {
		t.Errorf("missing --instance: error text = %q, want substring 'instance'", err)
	}
}

// ── (f) non-existent bucket → typed error ──────────────────────────
//
// Staff acceptance criterion 7: non-existent `<bucket>` → exit
// non-zero, error names the bucket.
func TestGetBucket_AcceptanceCriterion7_NonExistentBucket(t *testing.T) {
	notFound := errors.New("NoSuchBucket: The specified bucket does not exist")
	f := &fakeCOS{bucket: "ws-actual", listErr: notFound}
	dest := t.TempDir()

	_, err := runGetBucket(t, f, "ws-does-not-exist", dest, false)
	if err == nil {
		t.Fatal("non-existent bucket: expected typed error, got nil")
	}
	if !strings.Contains(err.Error(), "ws-does-not-exist") &&
		!strings.Contains(err.Error(), "NoSuchBucket") {
		t.Errorf("non-existent bucket: error text = %q, want bucket name or NoSuchBucket", err)
	}
}

// ── (g) <local-dir> uncreatable → typed error BEFORE any download ──
//
// Staff acceptance criterion 8: `<local-dir>` not creatable (e.g.
// permission denied) → exit non-zero before any download starts.
//
// Implementation: point destDir at a path whose parent is a regular
// file, so `mkdir -p` must fail. Assert that neither ListFn nor GetFn
// fires — the validation must happen first.
func TestGetBucket_AcceptanceCriterion8_UncreatableDestBeforeDownload(t *testing.T) {
	tmp := t.TempDir()
	parentAsFile := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(parentAsFile, []byte("regular file blocking mkdir\n"), 0o644); err != nil {
		t.Fatalf("seed regular-file blocker: %v", err)
	}
	uncreatable := filepath.Join(parentAsFile, "child-dir")

	listCalled, getCalled := false, false
	opts := GetBucketOptions{
		ListFn: func(context.Context, string) ([]ObjectInfo, error) {
			listCalled = true
			return nil, nil
		},
		GetFn: func(context.Context, string, string, string) error {
			getCalled = true
			return nil
		},
	}
	_, err := GetBucket(context.Background(), "fake-instance", "ws-any", uncreatable, opts)
	if err == nil {
		t.Fatal("uncreatable dest: expected typed error, got nil")
	}
	if listCalled {
		t.Errorf("uncreatable dest: ListFn was called — validation must fire first")
	}
	if getCalled {
		t.Errorf("uncreatable dest: GetFn was called — validation must fire first")
	}
}
