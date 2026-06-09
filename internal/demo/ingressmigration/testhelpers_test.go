package ingressmigration_test

import "os"

func readManifest(path string) ([]byte, error) {
	return os.ReadFile(path) // #nosec G304 — test helper, path from WriteManifest
}
