// Command flp-status serves the F5 License Proxy status web UI. It runs the SAME
// binary regardless of deployment type — as a container in the FLP podman pod on
// a standalone VSI, or as a Deployment in a ROKS cluster (exposed via NodePort,
// like the :8443 proxy Service). It auto-selects its data source (podman socket
// on the VSI, kubectl/k8s API in-cluster) or honors FLP_BACKEND. NO auth.
//
// Env: PORT (default 80), FLP_BACKEND (podman|k8s), FLP_NAMESPACE (k8s),
//
//	FLP_ENDPOINT, FLP_ROOT_CA_B64.
package main

import (
	"log"
	"os"

	"github.com/jgruberf5/roksbnkctl/internal/flpstatus"
)

func main() {
	addr := ":80"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}
	b := flpstatus.NewBackend()
	log.Printf("flp-status serving on %s (backend=%s)", addr, b.Kind())
	if err := flpstatus.Serve(addr, b); err != nil {
		log.Fatalf("flp-status: %v", err)
	}
}
