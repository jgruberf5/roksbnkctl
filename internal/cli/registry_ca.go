package cli

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
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

// Sentinel policy failures, distinguished from transport errors so the caller can
// print something actionable instead of a wrapped x509 message.
var (
	errUnpinnedPrivateCA = errors.New("unpinned private CA")
	errCAPinMismatch     = errors.New("CA pin mismatch")
)

// caCaptureOpts controls how much authority a captured CA is granted.
type caCaptureOpts struct {
	// PinSHA256 is the expected SHA-256 of a certificate in the served chain,
	// obtained OUT OF BAND (at mirror-build time). Any accepted form —
	// "sha256:ab:cd…", "AB:CD…", bare hex — normalises via normalizeCAPin.
	PinSHA256 string
	// AllowUnpinned permits trust-on-first-use with no pin (--insecure-capture-ca).
	AllowUnpinned bool
}

// normalizeCAPin reduces a user-supplied fingerprint to lowercase bare hex.
// Accepts the shapes people actually paste: `openssl x509 -fingerprint -sha256`
// output ("SHA256 Fingerprint=AB:CD:..."), a "sha256:" prefix, or bare hex.
func normalizeCAPin(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '='); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(s)), "sha256:")
	return strings.NewReplacer(":", "", " ", "", "-", "").Replace(s)
}

// caFingerprint is the lowercase hex SHA-256 of a certificate's DER — the value
// `openssl x509 -noout -fingerprint -sha256` prints (minus the colons).
func caFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// pemRootFingerprint returns the SHA-256 of the LAST certificate in a PEM bundle —
// the root, which is the one installed into node trust and therefore the one worth
// pinning. A single self-signed cert (the co-located Harbor case) is its own root.
func pemRootFingerprint(pemText string) (string, error) {
	var last []byte
	rest := []byte(pemText)
	for {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		if blk.Type == "CERTIFICATE" {
			last = blk.Bytes
		}
	}
	if last == nil {
		return "", errors.New("no CERTIFICATE block found")
	}
	if _, err := x509.ParseCertificate(last); err != nil {
		return "", fmt.Errorf("parsing the certificate: %w", err)
	}
	return caFingerprint(last), nil
}

// captureRegistryCA determines whether the mirror at host serves TLS with a CA
// the cluster nodes won't already trust, and if so returns it PEM-encoded so
// `bnk up` can install it into each node's certs.d before pulling images.
//
// Returns:
//   - (pem, nil)  a private/self-signed CA the nodes must be taught to trust
//   - ("", nil)   the host chains to a separate (public) issuer — no CA needed
//   - ("", err)   unreachable, or the served CA failed the trust policy below
//
// WHY THE SYSTEM ROOTS ARE NOT THE ORACLE HERE. Two independent reasons, and both
// matter:
//
//  1. A private root is by definition in no root store, so a verified dial cannot
//     succeed against the mirror we are trying to learn about.
//  2. On the operator host that CA is OFTEN already in local trust (cloud-init
//     runs `update-ca-certificates`), so a verified dial would succeed and tell us
//     "public, nothing to do" — while the air-gapped CLUSTER NODES still do not
//     trust it. That is the bug this heuristic exists to avoid.
//
// So the chain is inspected directly, and authentication is supplied by an
// OUT-OF-BAND PIN rather than by the root store. The whole policy lives in
// VerifyPeerCertificate so it runs during the handshake, before any bytes are
// exchanged, and so the decision is made on the chain the peer actually presented:
//
//   - chains to a separate issuer  → public; accept the handshake but return "",
//     granting the peer no trust whatsoever;
//   - self-signed + pin matches    → the operator vouched for this CA out of band;
//   - self-signed + no pin         → REFUSED, unless --insecure-capture-ca.
//
// The refusal is the point: a self-signed CA captured here is installed into every
// node's certs.d and persisted in registry-mirror.json, so an unauthenticated
// capture would hand cluster-wide, durable trust to whoever won a race on one dial.
func captureRegistryCA(host string, opts caCaptureOpts) (string, error) {
	if !strings.Contains(host, ":") {
		host += ":443"
	}
	pin := normalizeCAPin(opts.PinSHA256)

	// policyErr carries the verifier's verdict out of the handshake: the TLS stack
	// wraps a VerifyPeerCertificate error, so matching on it afterwards is unreliable.
	// servedFP is recorded in the same pass so a refusal can name the fingerprint the
	// host actually presented — without a second, unpinned dial to go fetch it.
	var policyErr error
	var servedFP string
	cfg := &tls.Config{
		// NOT "skip verification": the system roots are the wrong oracle for a
		// private root (see above), so VerifyPeerCertificate below IS the
		// verification — it authenticates the chain against the operator's pin.
		InsecureSkipVerify: true, //nolint:gosec // replaced by the pinned check in VerifyPeerCertificate
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if n := len(rawCerts); n > 0 {
				servedFP = caFingerprint(rawCerts[n-1]) // the root the operator would pin
			}
			policyErr = verifyCapturedChain(rawCerts, pin, opts.AllowUnpinned)
			return policyErr
		},
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", host, cfg)
	if err != nil {
		if policyErr != nil {
			return "", describeCAPolicyError(policyErr, host, servedFP)
		}
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

// verifyCapturedChain is the trust policy, applied to the raw chain during the
// handshake. A pin matches against ANY certificate in the chain: for a co-located
// Harbor the chain is a single self-signed cert (leaf == root), but a private
// two-cert chain lets the operator pin either the root they generated or the leaf
// they read off the host. Matching is constant-time and on the DER, so a peer must
// hold the corresponding private key to satisfy it — presenting a copied public
// certificate cannot complete the handshake.
func verifyCapturedChain(rawCerts [][]byte, pin string, allowUnpinned bool) error {
	if len(rawCerts) == 0 {
		return errors.New("peer served no certificates")
	}
	certs := make([]*x509.Certificate, 0, len(rawCerts))
	for _, der := range rawCerts {
		c, err := x509.ParseCertificate(der)
		if err != nil {
			return fmt.Errorf("parsing the served certificate chain: %w", err)
		}
		certs = append(certs, c)
	}
	top := certs[len(certs)-1]
	if !bytes.Equal(top.RawIssuer, top.RawSubject) {
		// Public chain: we grant it no trust and return "" to the caller, so there
		// is nothing here to authenticate. Not requiring a pin keeps ICR and other
		// publicly-issued mirrors working untouched.
		return nil
	}
	if pin == "" {
		if allowUnpinned {
			return nil
		}
		return errUnpinnedPrivateCA
	}
	want, err := hex.DecodeString(pin)
	if err != nil || len(want) != sha256.Size {
		return fmt.Errorf("%w: pin %q is not a SHA-256 hex digest", errCAPinMismatch, pin)
	}
	for _, c := range certs {
		sum := sha256.Sum256(c.Raw)
		if subtle.ConstantTimeCompare(sum[:], want) == 1 {
			return nil
		}
	}
	return errCAPinMismatch
}

// describeCAPolicyError turns a capture refusal into operator guidance naming the
// exact remedies, including the fingerprint actually served so a first-time
// operator can record the pin without a second tool.
func describeCAPolicyError(err error, host, served string) error {
	// Each branch WRAPS its sentinel (%w). resolveMirrorCA keys on errors.Is to
	// decide that a refusal is fatal rather than a best-effort miss — losing the
	// wrap would silently degrade to "no CA", which is the exact failure this
	// change exists to prevent.
	switch {
	case errors.Is(err, errUnpinnedPrivateCA):
		return fmt.Errorf(`%w: %s serves a PRIVATE (self-signed) CA, and roksbnkctl will not
adopt it over an unauthenticated connection — that CA is installed into every node's
trust and persisted in the workspace. Supply it from the source instead:

  roksbnkctl -w <ws> registry target generic_ca <harbor.crt>   (preferred: the file you generated)
  roksbnkctl -w <ws> registry target generic_ca_sha256 sha256:<fingerprint>
  registry replicate --registry-ca <harbor.crt> | --registry-ca-fingerprint sha256:<fp>

or accept trust-on-first-use explicitly with --insecure-capture-ca%s`, errUnpinnedPrivateCA, host, servedHint(served))
	case errors.Is(err, errCAPinMismatch):
		return fmt.Errorf(`%w: the CA served by %s does NOT match the configured fingerprint.
Either the mirror was rebuilt (re-record the pin) or the connection is not reaching the
mirror you think it is — treat a surprise here as hostile until proven otherwise%s`, errCAPinMismatch, host, servedHint(served))
	}
	return err
}

func servedHint(served string) string {
	if served == "" {
		return "."
	}
	return fmt.Sprintf(".\n\nThe host currently serves SHA-256 %s", served)
}
