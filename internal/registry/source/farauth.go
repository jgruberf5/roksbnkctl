package source

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strings"
)

// ExtractServiceAccountFromTarball reads the FAR auth tarball (the
// f5-cne-far-auth-key.tgz published in the orchestration COS bucket) and returns
// the FAR `_json_key_base64` service account — the content of the single `.json`
// entry inside it. This mirrors what the terraform flo module does (untar, take
// the first `*.json`, read it as far_service_account_b64).
func ExtractServiceAccountFromTarball(tgzPath string) (string, error) {
	f, err := os.Open(tgzPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("gunzip %s: %w", tgzPath, err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", tgzPath, err)
		}
		if hdr.Typeflag != tar.TypeReg || !strings.HasSuffix(hdr.Name, ".json") {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return "", fmt.Errorf("reading %s from %s: %w", hdr.Name, tgzPath, err)
		}
		sa := strings.TrimSpace(string(body))
		if sa == "" {
			return "", fmt.Errorf("FAR auth %s in %s is empty", hdr.Name, tgzPath)
		}
		return sa, nil
	}
	return "", fmt.Errorf("no .json service account found in %s", tgzPath)
}
