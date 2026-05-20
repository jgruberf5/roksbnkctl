package cos

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/IBM/ibm-cos-sdk-go/aws"
	"github.com/IBM/ibm-cos-sdk-go/service/s3"
	"github.com/IBM/ibm-cos-sdk-go/service/s3/s3manager"
)

// ObjectInfo describes an object as returned by ListObjects.
type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// PutObjectFromFile uploads the contents of localPath to bucket/key. Uses
// the s3manager Uploader so large files transparently use multipart with
// concurrent parts; small files take a single PUT.
//
// Sprint 18 Issue 3: the S3 handle is resolved per-bucket via
// s3ForBucket so the upload targets the bucket's actual region, not the
// workspace's home region.
func (c *Client) PutObjectFromFile(ctx context.Context, bucket, key, localPath string) error {
	sv, err := c.s3ForBucket(ctx, bucket)
	if err != nil {
		return err
	}
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", localPath, err)
	}
	defer f.Close()

	uploader := s3manager.NewUploaderWithClient(sv)
	_, err = uploader.UploadWithContext(ctx, &s3manager.UploadInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   f,
	})
	if err != nil {
		return fmt.Errorf("uploading %s to %s/%s: %w", localPath, bucket, key, err)
	}
	return nil
}

// GetObjectToFile downloads bucket/key to localPath, creating or
// truncating the file. Uses the s3manager Downloader for chunked,
// concurrent gets on large objects.
//
// Sprint 18 Issue 3: the S3 handle is resolved per-bucket via
// s3ForBucket so the download targets the bucket's actual region, not
// the workspace's home region — the fix that unblocks `cos object get`
// (and, by inheritance, the new `cos bucket get`) against a
// cross-region bucket like `bnk-schematics-resources` in us-south.
func (c *Client) GetObjectToFile(ctx context.Context, bucket, key, localPath string) error {
	sv, err := c.s3ForBucket(ctx, bucket)
	if err != nil {
		return err
	}
	f, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("creating %s: %w", localPath, err)
	}
	defer f.Close()

	downloader := s3manager.NewDownloaderWithClient(sv)
	_, err = downloader.DownloadWithContext(ctx, f, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		// Best-effort cleanup of the partial file so the next attempt
		// doesn't get tricked by leftover bytes.
		_ = os.Remove(localPath)
		return fmt.Errorf("downloading %s/%s to %s: %w", bucket, key, localPath, err)
	}
	return nil
}

// DeleteObject removes a single object.
//
// Sprint 18 Issue 3: routed through s3ForBucket so the delete hits the
// bucket's actual region.
func (c *Client) DeleteObject(ctx context.Context, bucket, key string) error {
	sv, err := c.s3ForBucket(ctx, bucket)
	if err != nil {
		return err
	}
	_, err = sv.DeleteObjectWithContext(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("deleting %s/%s: %w", bucket, key, err)
	}
	return nil
}

// ListObjects returns every object under bucket/prefix. Empty prefix =
// every object in the bucket. Pagination is handled internally — the
// caller gets one flat slice.
//
// Sprint 18 Issue 3: the S3 handle is resolved per-bucket via
// s3ForBucket. The resolution lookup happens at most once per bucket
// per CLI invocation — the *Client's bucket→region cache short-circuits
// subsequent calls, and the per-region S3 handle is built once and
// cached. So a recursive `cos bucket get` over N pages of objects in
// us-south runs exactly one region lookup, not N.
func (c *Client) ListObjects(ctx context.Context, bucket, prefix string) ([]ObjectInfo, error) {
	sv, err := c.s3ForBucket(ctx, bucket)
	if err != nil {
		return nil, err
	}
	var out []ObjectInfo
	err = sv.ListObjectsV2PagesWithContext(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	}, func(page *s3.ListObjectsV2Output, _ bool) bool {
		for _, o := range page.Contents {
			info := ObjectInfo{}
			if o.Key != nil {
				info.Key = *o.Key
			}
			if o.Size != nil {
				info.Size = *o.Size
			}
			if o.LastModified != nil {
				info.LastModified = *o.LastModified
			}
			out = append(out, info)
		}
		return true // keep paging
	})
	if err != nil {
		return nil, fmt.Errorf("listing %s/%s: %w", bucket, prefix, err)
	}
	return out, nil
}
