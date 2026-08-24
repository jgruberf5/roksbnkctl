package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
)

// maxFileArtifactBytes bounds what a KindFile fetch will read. The Gateway API
// bundle is ~1 MB; this is generous enough for a future one and small enough
// that a redirect to something enormous cannot exhaust memory.
const maxFileArtifactBytes = 32 << 20

// copyFile carries a plain file into the mirror as a SINGLE-LAYER OCI IMAGE.
//
// Not an OCI artifact: an image manifest is the one shape every registry stores,
// so this works on IBM Container Registry — the default target — without
// depending on whether it accepts non-image artifact types. It also reuses the
// crane path already vendored for images, so no new dependency.
//
// The file is fetched from a.SourceURL on the connected side and verified
// against a.SHA256 BEFORE it is pushed. Applying a megabyte of unverified CRDs
// is the supply-chain gap the embedded provider lockfile closed; a mirror that
// faithfully replicates the wrong bytes is not an improvement.
func (e *Engine) copyFile(ctx context.Context, a bnkbom.Artifact) Result {
	res := Result{Artifact: a}

	if strings.TrimSpace(a.SourceURL) == "" {
		res.Err = fmt.Errorf("file artifact %s has no source_url", a.Name)
		return res
	}
	// Fetch and verify BEFORE anything is pushed. Refusing an unpinned file is
	// deliberate: an unpinned file is one an attacker upstream can change under
	// us, and the whole point of putting it in the mirror is that what installs
	// is what was reviewed.
	body, err := FetchAndVerifyFile(ctx, a.SourceURL, a.SHA256)
	if err != nil {
		res.Err = fmt.Errorf("file artifact %s: %w", a.Name, err)
		return res
	}

	// The layer's single file keeps its upstream basename, so whatever pulls it
	// back can find it without a side-channel convention.
	img, err := crane.Image(map[string][]byte{path.Base(a.SourceURL): body})
	if err != nil {
		res.Err = fmt.Errorf("file artifact %s: building image: %w", a.Name, err)
		return res
	}

	dst := sanitizeRef(e.Target.PushRef(a))
	if err := crane.Push(img, dst, e.craneOpts(ctx)...); err != nil {
		res.Err = fmt.Errorf("file artifact %s: push to %s: %w", a.Name, dst, err)
		return res
	}

	d, err := img.Digest()
	if err != nil {
		res.Err = fmt.Errorf("file artifact %s: digest: %w", a.Name, err)
		return res
	}
	res.Digest = d.String()
	return res
}

// FetchAndVerifyFile GETs url and returns its body only if it matches wantSHA256.
//
// The connected fallback for a KindFile artifact: what copyFile does on the way
// into the mirror, exposed for the install path that has no mirror to read from.
// One implementation of "fetch this file and prove it is the right one", so the
// no-mirror path cannot end up with a weaker guarantee than the mirrored one —
// which is the path that would have carried it, and the reason the guarantee
// exists at all.
func FetchAndVerifyFile(ctx context.Context, url, wantSHA256 string) ([]byte, error) {
	if strings.TrimSpace(url) == "" {
		return nil, fmt.Errorf("no source URL to fetch the file from")
	}
	if strings.TrimSpace(wantSHA256) == "" {
		return nil, fmt.Errorf("fetch %s: no sha256 pin; refusing to use unverified content", url)
	}
	body, err := fetchFileArtifact(ctx, url)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != wantSHA256 {
		return nil, fmt.Errorf("fetch %s: sha256 mismatch\n  want %s\n  got  %s\n"+
			"  the upstream content changed, or the pin is wrong; nothing was used",
			url, wantSHA256, got)
	}
	return body, nil
}

// fetchFileArtifact GETs the URL and returns its body, bounded.
func fetchFileArtifact(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFileArtifactBytes+1))
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	if len(body) > maxFileArtifactBytes {
		return nil, fmt.Errorf("fetch %s: larger than the %d-byte ceiling", url, maxFileArtifactBytes)
	}
	return body, nil
}
