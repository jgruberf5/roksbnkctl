package cli

import (
	"crypto/tls"
	"encoding/pem"
	"fmt"
	"net"
	"strings"
	"time"
)

// registryHostFromPath returns the bare host[:port] from a "<host>/<ns>" mirror
// path (ImageHostPath), e.g. "10.241.0.4/bnk-mirror" -> "10.241.0.4".
func registryHostFromPath(hostPath string) string {
	hostPath = strings.TrimSpace(hostPath)
	if i := strings.IndexByte(hostPath, '/'); i >= 0 {
		return hostPath[:i]
	}
	return hostPath
}

// captureRegistryCA determines whether the mirror at host serves TLS with a CA
// the cluster nodes won't already trust, and if so returns it PEM-encoded so
// `bnk up` can install it into each node's certs.d before pulling images.
//
// Returns:
//   - (pem, nil)  a private/untrusted CA the nodes must be taught to trust
//   - ("", nil)   the host is publicly trusted (no per-node CA needed)
//   - ("", err)   the host could not be reached to determine trust
//
// For a self-signed registry (e.g. a co-located Harbor) the served leaf is its
// own CA; the full served chain is encoded so CRI-O trusts it.
func captureRegistryCA(host string) (string, error) {
	if !strings.Contains(host, ":") {
		host += ":443"
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	// A verified handshake means the host already chains to a public root the
	// nodes trust — nothing to install.
	if conn, err := tls.DialWithDialer(dialer, "tcp", host, nil); err == nil {
		conn.Close()
		return "", nil
	}

	// Verification failed (self-signed / private CA). Re-dial without
	// verification purely to capture what the host serves.
	conn, err := tls.DialWithDialer(dialer, "tcp", host, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // capture only; no data is transported over this connection
	if err != nil {
		return "", fmt.Errorf("dialing %s to capture its CA: %w", host, err)
	}
	defer conn.Close()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", fmt.Errorf("%s served no certificates", host)
	}
	var b strings.Builder
	for _, c := range certs {
		_ = pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
	}
	return b.String(), nil
}
