package cli

import (
	"bytes"
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
//   - (pem, nil)  a private/self-signed CA the nodes must be taught to trust
//   - ("", nil)   the host chains to a separate (public) issuer — no CA needed
//   - ("", err)   the host could not be reached to determine trust
//
// Trust is decided from the served chain itself, NOT from a verified handshake:
// on the operator host the private registry's CA is often already in the local
// trust store (e.g. `update-ca-certificates`), so a verified dial succeeds even
// though the air-gapped CLUSTER NODES do not trust it. We instead capture the
// served chain unconditionally and key on whether its top certificate is
// self-signed — the signature of a private CA / self-signed registry (a
// co-located Harbor). A registry fronted by a public CA sends a chain whose top
// is an intermediate (issuer != subject), which the nodes' default roots cover.
func captureRegistryCA(host string) (string, error) {
	if !strings.Contains(host, ":") {
		host += ":443"
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", host, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // capture only; no data is transported over this connection
	if err != nil {
		return "", fmt.Errorf("dialing %s to capture its CA: %w", host, err)
	}
	defer conn.Close()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", fmt.Errorf("%s served no certificates", host)
	}
	top := certs[len(certs)-1]
	if !bytes.Equal(top.RawIssuer, top.RawSubject) {
		// Chains to a separate issuer → treat as public; nodes already trust it.
		return "", nil
	}
	var b strings.Builder
	for _, c := range certs {
		_ = pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
	}
	return b.String(), nil
}
