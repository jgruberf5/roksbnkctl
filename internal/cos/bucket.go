package cos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/IBM/ibm-cos-sdk-go/aws"
	"github.com/IBM/ibm-cos-sdk-go/service/s3"
)

// CreateBucket creates a bucket bound to the client's COS instance. The
// region/class pair is encoded into a LocationConstraint string that IBM
// COS uses to pick the storage tier and locality.
func (c *Client) CreateBucket(ctx context.Context, name, class string) error {
	if name == "" {
		return fmt.Errorf("bucket name is empty")
	}
	_, err := c.s3.CreateBucketWithContext(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(name),
		CreateBucketConfiguration: &s3.CreateBucketConfiguration{
			LocationConstraint: aws.String(LocationConstraint(c.region, class)),
		},
	})
	if err != nil {
		return fmt.Errorf("creating bucket %s: %w", name, err)
	}
	return nil
}

// DeleteBucket removes a bucket. Fails if the bucket is non-empty —
// callers should empty the bucket first or expose --force in the CLI to
// do that for them (deferred to v1.x).
func (c *Client) DeleteBucket(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("bucket name is empty")
	}
	_, err := c.s3.DeleteBucketWithContext(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(name),
	})
	if err != nil {
		return fmt.Errorf("deleting bucket %s: %w", name, err)
	}
	return nil
}

// GetBucketOptions tunes a recursive bucket download.
//
// NoClobber, when true, skips an object whose local target already
// exists (mtime untouched). Default is overwrite — the operator just
// asked to download, so `cp -n` semantics only apply when explicitly
// requested.
//
// ListFn and GetFn are the two operations GetBucket needs against COS:
// list-everything and stream-one-object-to-disk. The CLI binds them to
// the live `Client.ListObjects` + `Client.GetObjectToFile` methods (see
// ClientGetBucketOptions); tests bind them to an in-memory fake so the
// hermetic suite never opens a socket. Both are required — GetBucket
// will not invent a transport on its own.
//
// OnItem, if non-nil, is invoked once per object after the per-object
// outcome (downloaded or skipped) is known. The CLI uses this to emit
// one JSON record per file when --output json.
type GetBucketOptions struct {
	NoClobber bool
	ListFn    func(ctx context.Context, bucket string) ([]ObjectInfo, error)
	GetFn     func(ctx context.Context, bucket, key, localPath string) error
	OnItem    func(GetBucketItem)
}

// GetBucketCounts is the end-of-run tally emitted on stderr (text mode)
// or returned to the caller for higher-level reporting.
type GetBucketCounts struct {
	Objects int   `json:"objects"`
	Bytes   int64 `json:"bytes"`
	Skipped int   `json:"skipped"`
}

// GetBucketItem reports the outcome of a single object inside a
// recursive bucket download. The CLI emits one JSON object per item
// when --output json, and a single text line otherwise.
type GetBucketItem struct {
	Key       string `json:"key"`
	LocalPath string `json:"local_path"`
	Size      int64  `json:"size"`
	ETag      string `json:"etag,omitempty"`
	Outcome   string `json:"outcome"` // "downloaded" | "skipped"
}

// ClientGetBucketOptions wires the live Client into a GetBucketOptions
// — convenience helper the CLI uses so it doesn't have to spell out
// the closures every call. The returned opts already point at the
// Client's `ListObjects(bucket, "")` and `GetObjectToFile` so callers
// can layer NoClobber / OnItem and pass straight to GetBucket.
func ClientGetBucketOptions(c *Client) GetBucketOptions {
	return GetBucketOptions{
		ListFn: func(ctx context.Context, bucket string) ([]ObjectInfo, error) {
			return c.ListObjects(ctx, bucket, "")
		},
		GetFn: c.GetObjectToFile,
	}
}

// GetBucket recursively downloads every object in bucket to destDir,
// preserving object-key directory structure. instanceID is validated
// up-front (empty → typed error; acceptance criterion 6) but otherwise
// passed through to the caller's ListFn/GetFn — GetBucket itself
// doesn't open IAM sessions; the *cos.Client behind the seam already
// carries the IAM scope for its instance.
//
// destDir is created (mkdir -p semantics, mode 0755) if it does not
// already exist; failure to create returns before any list or download
// fires, so a permission-denied destination never half-populates the
// local tree (acceptance criterion 8) and the seam stays untouched.
//
// Object keys with `/`-separated segments map to nested subdirectories
// under destDir; a key `foo/bar/baz.json` lands at
// `<destDir>/foo/bar/baz.json` (acceptance criterion 3). Each download
// streams via opts.GetFn — production wiring uses the s3manager
// Downloader inside Client.GetObjectToFile, the same per-object
// streaming path `cos object get` rides on, so binaries of arbitrary
// size work without in-memory buffering.
//
// Returns the run-wide counts even on partial-failure error returns so
// the CLI can report progress before exiting non-zero.
func GetBucket(ctx context.Context, instanceID, bucket, destDir string, opts GetBucketOptions) (GetBucketCounts, error) {
	var counts GetBucketCounts
	if instanceID == "" {
		// Acceptance criterion 6 surfaces as "--instance is required"
		// in the CLI layer; here it's the typed library-level shape.
		return counts, fmt.Errorf("COS instance is required (instance name or CRN)")
	}
	if bucket == "" {
		return counts, fmt.Errorf("bucket name is empty")
	}
	if destDir == "" {
		return counts, fmt.Errorf("destination directory is empty")
	}
	if opts.ListFn == nil {
		return counts, fmt.Errorf("GetBucket: ListFn is required")
	}
	if opts.GetFn == nil {
		return counts, fmt.Errorf("GetBucket: GetFn is required")
	}

	// Create destDir up-front so a permission-denied destination
	// (acceptance criterion 8) fails before any list / download starts.
	// Validators assert via no-call sentinels that this path runs first.
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return counts, fmt.Errorf("creating destination %s: %w", destDir, err)
	}

	objs, err := opts.ListFn(ctx, bucket)
	if err != nil {
		return counts, err
	}

	for _, o := range objs {
		// Treat keys that end in `/` as directory placeholders (an S3
		// convention; not files). Create the matching local subdir but
		// don't attempt to download "the directory".
		if strings.HasSuffix(o.Key, "/") {
			subdir := filepath.Join(destDir, filepath.FromSlash(o.Key))
			if err := os.MkdirAll(subdir, 0o755); err != nil {
				return counts, fmt.Errorf("creating subdir %s: %w", subdir, err)
			}
			continue
		}

		localPath, err := safeLocalPath(destDir, o.Key)
		if err != nil {
			return counts, err
		}

		if opts.NoClobber {
			if _, statErr := os.Stat(localPath); statErr == nil {
				counts.Skipped++
				if opts.OnItem != nil {
					opts.OnItem(GetBucketItem{
						Key:       o.Key,
						LocalPath: localPath,
						Size:      o.Size,
						Outcome:   "skipped",
					})
				}
				continue
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return counts, fmt.Errorf("stat %s: %w", localPath, statErr)
			}
		}

		// Ensure parent dirs for nested keys exist before the streaming
		// download writes the file (acceptance criterion 3).
		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			return counts, fmt.Errorf("creating parent of %s: %w", localPath, err)
		}

		// Stream to <file>.part then atomically rename — no half-written
		// files survive a context cancellation or transient network
		// error. The temp name lives next to the final file so the
		// rename stays on the same filesystem (rename(2) is atomic only
		// within a single fs).
		tmpPath := localPath + ".part"
		// Clear any leftover partial from a prior aborted run before
		// asking the downloader to (re)create it.
		_ = os.Remove(tmpPath)
		if err := opts.GetFn(ctx, bucket, o.Key, tmpPath); err != nil {
			_ = os.Remove(tmpPath)
			return counts, err
		}
		if err := os.Rename(tmpPath, localPath); err != nil {
			_ = os.Remove(tmpPath)
			return counts, fmt.Errorf("finalising %s: %w", localPath, err)
		}

		counts.Objects++
		counts.Bytes += o.Size
		if opts.OnItem != nil {
			opts.OnItem(GetBucketItem{
				Key:       o.Key,
				LocalPath: localPath,
				Size:      o.Size,
				Outcome:   "downloaded",
			})
		}
	}

	return counts, nil
}

// safeLocalPath joins destDir with an object key, rejecting keys that
// would resolve outside destDir (defensive — a hostile bucket should
// not be able to write `/etc/passwd` via a key of `../../etc/passwd`).
// Returns the cleaned local path.
func safeLocalPath(destDir, key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("object key is empty")
	}
	// Convert S3-style forward slashes to the host path separator so
	// nested keys map to nested directories on every platform.
	rel := filepath.FromSlash(key)
	joined := filepath.Join(destDir, rel)
	// Clean the dest once so the prefix check below is robust to
	// trailing-slash and `.` segments the caller may have passed.
	cleanedDest := filepath.Clean(destDir)
	cleanedJoined := filepath.Clean(joined)
	// `filepath.Join` already collapses `..` segments, but the result
	// can still escape destDir (e.g. key `../etc/passwd` with
	// destDir=`/tmp/out` yields `/tmp/etc/passwd`). Reject any such
	// resolution.
	if cleanedJoined != cleanedDest && !strings.HasPrefix(cleanedJoined, cleanedDest+string(filepath.Separator)) {
		return "", fmt.Errorf("refusing to write %q outside %s", key, destDir)
	}
	return cleanedJoined, nil
}

// ListBuckets returns every bucket in the client's COS instance.
// IBM COS scopes ListBuckets by the IAM credentials' instance ID, so
// only buckets in this Client's instance come back.
func (c *Client) ListBuckets(ctx context.Context) ([]string, error) {
	res, err := c.s3.ListBucketsWithContext(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("listing buckets: %w", err)
	}
	names := make([]string, 0, len(res.Buckets))
	for _, b := range res.Buckets {
		if b.Name != nil {
			names = append(names, *b.Name)
		}
	}
	return names, nil
}
