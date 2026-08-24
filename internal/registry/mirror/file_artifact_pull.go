package mirror

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
)

// The read side of the KindFile artifact (#185).
//
// copyFile puts a plain file into the mirror as a single-layer OCI image; this
// takes it back out. It lives in the mirror package next to the writer on
// purpose: the layout — one layer, one file, stored under the upstream basename
// — is a private convention between the two, and a reader written anywhere else
// would encode that convention a second time and drift from it.

// PullFileOptions are the crane options for reading a KindFile artifact out of a
// mirror: the target's credential, and the private CA it serves TLS with.
//
// The install may run from somewhere the replicate never did — in the CI path,
// as an Argo pod inside the cluster — so it cannot inherit a keychain or an OS
// trust store from the machine that replicated. Both facts have to be passed.
func PullFileOptions(ctx context.Context, host string, auth authn.Authenticator, caPEM string, insecure bool) []crane.Option {
	if auth == nil {
		auth = authn.Anonymous
	}
	opts := []crane.Option{
		crane.WithContext(ctx),
		crane.WithAuthFromKeychain(keychain{targetHost: host, targetAuth: auth}),
	}
	if insecure {
		opts = append(opts, crane.Insecure)
	}
	if tr := CATransport(caPEM); tr != nil {
		opts = append(opts, crane.WithTransport(tr))
	}
	return opts
}

// PullFile pulls the single-layer image at ref and returns the bytes of the one
// file it carries, verified against wantSHA256.
//
// The pin is checked HERE, not only at replicate time. A mirror is a place other
// people can write to — that is what makes it useful and what makes it worth
// checking — and the bytes are about to be applied to a cluster with
// --force-conflicts. Verifying only on the way in would mean the guarantee
// expires the moment the artifact lands.
//
// basenameHint selects the file when the layer somehow carries more than one; an
// empty hint takes the only regular file, and refuses if there is not exactly
// one, rather than picking arbitrarily.
func PullFile(ctx context.Context, ref, basenameHint, wantSHA256 string, opts ...crane.Option) ([]byte, error) {
	if strings.TrimSpace(wantSHA256) == "" {
		return nil, fmt.Errorf("pull %s: no sha256 pin; refusing to use unverified content", ref)
	}
	img, err := crane.Pull(sanitizeRef(ref), opts...)
	if err != nil {
		return nil, fmt.Errorf("pull %s: %w", ref, err)
	}
	var buf bytes.Buffer
	if err := crane.Export(img, &buf); err != nil {
		return nil, fmt.Errorf("pull %s: exporting the image filesystem: %w", ref, err)
	}

	var got []byte
	var names []string
	tr := tar.NewReader(&buf)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("pull %s: reading the image filesystem: %w", ref, err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		names = append(names, h.Name)
		if basenameHint != "" && path.Base(h.Name) != basenameHint {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(tr, maxFileArtifactBytes+1))
		if err != nil {
			return nil, fmt.Errorf("pull %s: reading %s: %w", ref, h.Name, err)
		}
		if len(body) > maxFileArtifactBytes {
			return nil, fmt.Errorf("pull %s: %s is larger than the %d-byte ceiling", ref, h.Name, maxFileArtifactBytes)
		}
		if got != nil {
			return nil, fmt.Errorf("pull %s: the image carries more than one candidate file (%s); "+
				"refusing to guess which one to apply", ref, strings.Join(names, ", "))
		}
		got = body
	}
	if got == nil {
		if basenameHint != "" {
			return nil, fmt.Errorf("pull %s: no %s in the mirrored image (it carries: %s)",
				ref, basenameHint, filesOrNone(names))
		}
		return nil, fmt.Errorf("pull %s: the mirrored image carries no file", ref)
	}

	sum := sha256.Sum256(got)
	if h := hex.EncodeToString(sum[:]); h != wantSHA256 {
		return nil, fmt.Errorf("pull %s: sha256 mismatch\n  want %s\n  got  %s\n"+
			"  the mirror holds different bytes than this build pins; nothing was applied",
			ref, wantSHA256, h)
	}
	return got, nil
}

func filesOrNone(names []string) string {
	if len(names) == 0 {
		return "nothing"
	}
	return strings.Join(names, ", ")
}
