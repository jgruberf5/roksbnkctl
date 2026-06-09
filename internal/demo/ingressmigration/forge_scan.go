package ingressmigration

import (
	"context"
	"fmt"
	"io"

	"github.com/JLCode-tech/awsbnkctl/internal/forge"
)

// defaultForgeScan calls forge.ScanCluster best-effort.
// Errors are logged to out and swallowed — forge is advisory only.
func defaultForgeScan(ctx context.Context, clusterID int, out io.Writer) {
	fc := forge.NewClient("")
	if _, err := fc.ScanCluster(ctx, clusterID); err != nil {
		fmt.Fprintf(out, "[demo/ingress-migration] forge scan_cluster: warning (non-fatal): %v\n", err)
		return
	}
	fmt.Fprintf(out, "[demo/ingress-migration] forge scan_cluster triggered OK (cluster_id=%d)\n", clusterID)
}
