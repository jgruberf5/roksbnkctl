package forge

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// PrivateKeyFingerprint returns the OpenSSH SHA256 fingerprint of the PUBLIC
// half of a private key, in the `SHA256:...` form `ssh-keygen -l -E sha256`
// prints and IBM Cloud reports for a VPC key.
//
// WHY THIS EXISTS: a credential that cannot log in is worse than no credential.
// Forge then reports infrastructure access as configured, and every later
// failure points somewhere other than the key (#222). Comparing the private
// key's fingerprint against the VPC key named in the workspace catches a
// mismatched key at the moment it is offered, rather than at the moment
// something else breaks.
func PrivateKeyFingerprint(pem []byte) (string, error) {
	signer, err := ssh.ParsePrivateKey(pem)
	if err != nil {
		// A passphrase-protected key parses only with the passphrase. Say which
		// of the two problems it is: "invalid key" sends the reader looking for
		// corruption that is not there.
		if strings.Contains(strings.ToLower(err.Error()), "passphrase") {
			return "", fmt.Errorf("the private key is passphrase-protected; Forge stores the key "+
				"itself and cannot prompt, so supply an unencrypted key: %w", err)
		}
		return "", fmt.Errorf("parsing private key: %w", err)
	}
	sum := sha256.Sum256(signer.PublicKey().Marshal())
	// OpenSSH prints the base64 UNPADDED, and so does IBM Cloud. Keeping the
	// padding would make every comparison fail on the trailing '='.
	return "SHA256:" + strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "="), nil
}

// FingerprintsMatch compares two fingerprints tolerantly of the `SHA256:`
// prefix and of padding, either of which may or may not be present depending on
// which tool produced the value.
func FingerprintsMatch(a, b string) bool {
	return normalizeFingerprint(a) == normalizeFingerprint(b)
}

func normalizeFingerprint(f string) string {
	f = strings.TrimSpace(f)
	f = strings.TrimPrefix(f, "SHA256:")
	f = strings.TrimPrefix(f, "sha256:")
	return strings.TrimRight(f, "=")
}
