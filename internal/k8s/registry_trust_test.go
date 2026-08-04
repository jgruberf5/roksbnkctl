package k8s

import "strings"

import "testing"

// TestRegistryCAInstallCmd guards the exact regression that broke air-gap
// pulls: the shell loop variable $d must survive into the container command
// (so the CA lands at <certs.d>/<host>/ca.crt on the node), and the host must
// be substituted by Go. A prior heredoc-generated DaemonSet ate $d, writing
// the CA to a bogus in-container path and leaving nodes with x509 "unknown
// authority".
func TestRegistryCAInstallCmd(t *testing.T) {
	host := "10.241.0.4"
	cmd := registryCAInstallCmd(host)

	// $d must be present (as a live shell variable), not eaten.
	if !strings.Contains(cmd, `"$d/10.241.0.4"`) {
		t.Errorf("install cmd lost the $d loop variable; got:\n%s", cmd)
	}
	if !strings.Contains(cmd, `install -m644 /ca/ca.crt "$d/10.241.0.4/ca.crt"`) {
		t.Errorf("install cmd does not write ca.crt under $d/<host>; got:\n%s", cmd)
	}
	// It must target BOTH the CRI-O and docker trust dirs.
	for _, dir := range []string{"/host/etc/containers/certs.d", "/host/etc/docker/certs.d"} {
		if !strings.Contains(cmd, dir) {
			t.Errorf("install cmd missing trust dir %q; got:\n%s", dir, cmd)
		}
	}
	// A bare /<host> (the bug's signature — $d dropped) must NOT appear.
	if strings.Contains(cmd, ` /10.241.0.4/ca.crt`) || strings.Contains(cmd, `"/10.241.0.4`) {
		t.Errorf("install cmd writes to a $d-less absolute path (the bug); got:\n%s", cmd)
	}
}
