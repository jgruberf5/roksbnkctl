package source

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// maxManifestBytes caps the inner manifest YAML read so a hostile/corrupt
// tarball can't exhaust memory. The real manifest is ~2 KB; 8 MiB is generous.
const maxManifestBytes = 8 << 20

// extractManifestYAML opens the helm-pulled chart tarball at tgzPath and returns
// the bytes of the inner bigip-k8s-manifest-<version>.yaml. The archive layout
// (confirmed against the real 2.3.0 artifact) is:
//
//	f5-bigip-k8s-manifest-<version>/
//	f5-bigip-k8s-manifest-<version>/bigip-k8s-manifest-<version>.yaml
//	f5-bigip-k8s-manifest-<version>/Chart.yaml
//
// We match on the basename bigip-k8s-manifest-<version>.yaml rather than the
// full path so a future chart that nests the file differently still resolves.
func extractManifestYAML(tgzPath, version string) ([]byte, error) {
	f, err := os.Open(tgzPath)
	if err != nil {
		return nil, fmt.Errorf("open pulled manifest %s: %w", tgzPath, err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("gunzip %s: %w", tgzPath, err)
	}
	defer gz.Close()

	want := fmt.Sprintf("bigip-k8s-manifest-%s.yaml", version)
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", tgzPath, err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if path.Base(hdr.Name) == want {
			data, err := io.ReadAll(io.LimitReader(tr, maxManifestBytes))
			if err != nil {
				return nil, fmt.Errorf("reading %s from %s: %w", want, tgzPath, err)
			}
			if len(data) == 0 {
				return nil, fmt.Errorf("%s in %s is empty", want, tgzPath)
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("%s not found in %s (entries did not include the manifest YAML)", want, tgzPath)
}

// manifestYAMLName is exported-for-test-equivalent helper kept unexported; the
// fixture test builds an archive matching this name shape.
func manifestYAMLName(version string) string {
	return strings.Join([]string{"bigip-k8s-manifest-", version, ".yaml"}, "")
}
