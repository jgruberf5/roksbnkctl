package k8s

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// KubeconfigClass classifies a kubeconfig by how its users authenticate,
// which decides the refresh action and the staleness skew.
type KubeconfigClass int

const (
	// ClassUnknown — no recognizable credential (or unparseable).
	ClassUnknown KubeconfigClass = iota
	// ClassToken — at least one user authenticates with a bearer token
	// (the forge kubeconfig). Cheap to refresh: re-mint the IAM token.
	ClassToken
	// ClassCert — users authenticate with client certs (the admin
	// kubeconfig). Expensive to refresh: re-fetch the admin config.
	ClassCert
)

// Classify reports how a kubeconfig's users authenticate. A token user wins
// over a cert user (the forge kubeconfig is token-based); falls back to
// ClassCert if any user carries a client cert, else ClassUnknown.
func Classify(kubeconfig []byte) KubeconfigClass {
	var doc map[string]any
	if err := yaml.Unmarshal(kubeconfig, &doc); err != nil {
		return ClassUnknown
	}
	users, _ := doc["users"].([]any)
	hasCert := false
	for _, u := range users {
		inner := userInner(u)
		if inner == nil {
			continue
		}
		if s, _ := inner["token"].(string); s != "" {
			return ClassToken
		}
		if s, _ := inner["client-certificate-data"].(string); s != "" {
			hasCert = true
		}
	}
	if hasCert {
		return ClassCert
	}
	return ClassUnknown
}

// MinExpiry returns the earliest expiry across all credentials in the
// kubeconfig (token `exp` claim and/or client-certificate NotAfter). It is
// a pure local parse — no network. An unparseable credential is treated as
// already expired (zero time) so the caller refreshes rather than trusting
// a config it can't read. Returns an error only when there are NO
// recognizable credentials at all.
func MinExpiry(kubeconfig []byte) (time.Time, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(kubeconfig, &doc); err != nil {
		return time.Time{}, fmt.Errorf("parsing kubeconfig: %w", err)
	}
	users, _ := doc["users"].([]any)

	var min time.Time
	found := false
	consider := func(t time.Time) {
		if !found || t.Before(min) {
			min = t
			found = true
		}
	}
	for _, u := range users {
		inner := userInner(u)
		if inner == nil {
			continue
		}
		if tok, _ := inner["token"].(string); tok != "" {
			exp, err := tokenExpiry(tok)
			if err != nil {
				consider(time.Time{}) // unreadable → treat as expired
			} else {
				consider(exp)
			}
		}
		if cert, _ := inner["client-certificate-data"].(string); cert != "" {
			exp, err := certExpiry(cert)
			if err != nil {
				consider(time.Time{})
			} else {
				consider(exp)
			}
		}
	}
	if !found {
		return time.Time{}, errors.New("kubeconfig has no token or client-certificate credentials")
	}
	return min, nil
}

// tokenExpiry decodes an IAM access token (a JWT) and returns its `exp`
// claim as a time. JWTs are base64url (no padding) with three
// dot-separated segments; we decode the middle (payload) segment.
func tokenExpiry(jwt string) (time.Time, error) {
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return time.Time{}, errors.New("token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return time.Time{}, fmt.Errorf("decoding token payload: %w", err)
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, fmt.Errorf("parsing token claims: %w", err)
	}
	if claims.Exp == 0 {
		return time.Time{}, errors.New("token has no exp claim")
	}
	return time.Unix(claims.Exp, 0), nil
}

// certExpiry decodes base64 client-certificate-data (PEM) and returns the
// certificate's NotAfter.
func certExpiry(b64 string) (time.Time, error) {
	der, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return time.Time{}, fmt.Errorf("decoding certificate-data: %w", err)
	}
	block, _ := pem.Decode(der)
	if block == nil {
		return time.Time{}, errors.New("certificate-data is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing certificate: %w", err)
	}
	return cert.NotAfter, nil
}

// userInner returns the `user` map of a kubeconfig users[] entry, or nil.
func userInner(u any) map[string]any {
	um, _ := u.(map[string]any)
	if um == nil {
		return nil
	}
	inner, _ := um["user"].(map[string]any)
	return inner
}
