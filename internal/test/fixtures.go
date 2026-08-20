package test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// This file renders the ephemeral, runner-owned k8s fixtures the matrix
// deploys before a campaign and removes after. They are *additive* to a
// deployed cluster and never mutate the operator's gateway-phase
// objects: the route fixtures attach to the EXISTING Gateway by name
// (parentRefs), so the runner only ever creates its own backend +
// Routes + TLS Secret. Listener provisioning stays with the operator.
//
// Every function here is a pure string builder so the manifests are
// unit-testable without a cluster; the CLI layer SSA-applies the
// concatenated stream via internal/k8s.ApplyOptions.

// FixtureLabels is the label every fixture object carries, so teardown
// is a single label-selected delete and a stray fixture is obvious.
const (
	FixtureLabelKey   = "app.kubernetes.io/managed-by"
	FixtureLabelValue = "roksbnkctl-matrix"
)

// FixtureNames are the stable object names the runner uses.
const (
	HTTPBackendName = "matrix-http-backend"
	TLSSecretName   = "matrix-tls"
	HTTPRouteName   = "matrix-httproute"
	// HTTPSRouteName is the HTTPS-listener HTTPRoute. It was called
	// TLSRouteName, which described a kind it has never been: renderHTTPRoute
	// produces it, and a TLSRoute is a different object in a channel BNK does
	// not install.
	HTTPSRouteName = "matrix-httproute-tls"
	// L4RouteName is BNK's own L4 route. NOT a Gateway API TCPRoute — see
	// renderL4Route.
	L4RouteName = "matrix-l4route"

	httpBackendImage = "nginx:stable"
)

// FixturePlan is the set of fixtures to render, derived from the
// matrix's FixturesSpec + GatewayIdentity.
type FixturePlan struct {
	Gateway     GatewayIdentity
	HTTPBackend bool
	Routes      bool

	// Backend service the routes point at. For routes==true with an HTTP
	// backend this defaults to HTTPBackendName; an operator can override
	// to target their own app service.
	BackendService string
	BackendPort    int

	// TLSHost is the SAN/CN baked into the self-signed cert the TLS route
	// presents. Cosmetic for a load test (h2load doesn't verify), but a
	// real hostname keeps logs sane.
	TLSHost string

	// GatewayName + listener section names the Routes attach to. These
	// must already exist on the operator's Gateway.
	GatewayName    string
	HTTPSection    string
	HTTPSSection   string
	TCPSection     string
	TCPBackendSvc  string
	TCPBackendPort int
}

// RenderFixtures returns the multi-document YAML for every enabled
// fixture, ready for `kubectl apply -f -`. Order matters: the Secret
// precedes the TLS route that references it; the backend precedes the
// routes that target it.
func RenderFixtures(p FixturePlan) (string, error) {
	ns := p.Gateway.AppNamespace
	if ns == "" {
		ns = "default"
	}
	var docs []string

	if p.HTTPBackend {
		docs = append(docs, renderHTTPBackend(ns))
	}

	if p.Routes {
		svc := p.BackendService
		if svc == "" {
			svc = HTTPBackendName
		}
		port := p.BackendPort
		if port == 0 {
			port = 80
		}
		gwName := p.GatewayName
		if gwName == "" {
			return "", fmt.Errorf("fixtures: routes require a gateway name to attach to")
		}

		// HTTP route.
		docs = append(docs, renderHTTPRoute(HTTPRouteName, ns, gwName, p.HTTPSection, svc, port))

		// TLS route + secret (only if an HTTPS section is named).
		if p.HTTPSSection != "" {
			host := p.TLSHost
			if host == "" {
				host = "matrix.local"
			}
			certPEM, keyPEM, err := GenSelfSignedCert(host)
			if err != nil {
				return "", fmt.Errorf("fixtures: generating TLS cert: %w", err)
			}
			docs = append(docs, renderTLSSecret(TLSSecretName, ns, certPEM, keyPEM))
			docs = append(docs, renderHTTPRoute(HTTPSRouteName, ns, gwName, p.HTTPSSection, svc, port))
		}

		// TCP route (only if a TCP section is named).
		if p.TCPSection != "" {
			tsvc := p.TCPBackendSvc
			tport := p.TCPBackendPort
			if tsvc == "" {
				tsvc, tport = "iperf3-server", 5201 // the throughput fixture's service
			}
			docs = append(docs, renderL4Route(L4RouteName, ns, gwName, p.TCPSection, tsvc, tport))
		}
	}

	return strings.Join(docs, "\n---\n"), nil
}

func labelsBlock(indent string) string {
	return fmt.Sprintf("%slabels:\n%s  %s: %s", indent, indent, FixtureLabelKey, FixtureLabelValue)
}

// renderHTTPBackend is an nginx pod that serves fixed-size bodies at
// /128, /5k, /512k (created at startup with dd-from-/dev/zero), plus a
// ClusterIP service. The L7 cells select a payload by URL path.
func renderHTTPBackend(ns string) string {
	return fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %[1]s
  namespace: %[2]s
%[4]s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: %[1]s
  template:
    metadata:
      labels:
        app: %[1]s
        %[5]s: %[6]s
    spec:
      containers:
      - name: http
        image: %[3]s
        command: ["/bin/sh", "-c"]
        args:
        - |
          d=/usr/share/nginx/html
          mkdir -p "$d"
          head -c 128 /dev/zero | tr '\0' 'a' > "$d/128"
          head -c 5120 /dev/zero | tr '\0' 'a' > "$d/5k"
          head -c 524288 /dev/zero | tr '\0' 'a' > "$d/512k"
          exec nginx -g 'daemon off;'
        ports:
        - containerPort: 80
---
apiVersion: v1
kind: Service
metadata:
  name: %[1]s
  namespace: %[2]s
%[4]s
spec:
  selector:
    app: %[1]s
  ports:
  - port: 80
    targetPort: 80
`, HTTPBackendName, ns, httpBackendImage, labelsBlock("  "), FixtureLabelKey, FixtureLabelValue)
}

func renderHTTPRoute(name, ns, gateway, section, svc string, port int) string {
	sec := ""
	if section != "" {
		sec = fmt.Sprintf("\n    sectionName: %s", section)
	}
	return fmt.Sprintf(`apiVersion: %[8]s
kind: HTTPRoute
metadata:
  name: %[1]s
  namespace: %[2]s
%[7]s
spec:
  parentRefs:
  - name: %[3]s
    namespace: %[2]s%[4]s
  rules:
  - backendRefs:
    - name: %[5]s
      port: %[6]d
`, name, ns, gateway, sec, svc, port, labelsBlock("  "), HTTPRouteKind.APIVersion())
}

// renderL4Route emits BNK's L4Route, not a Gateway API TCPRoute.
//
// This used to render `gateway.networking.k8s.io/v1alpha2 TCPRoute` — a CRD BNK
// NEVER installs. BNK 2.3 requires Gateway API 1.4.1 STANDARD, and the standard
// channel has no TCPRoute at all (verified on a live cluster: six CRDs, all
// annotated channel=standard). So the object could not be created, the iperf3
// L4 leg of the matrix never ran, and because the fixture apply is best-effort
// the failure was silent — it produced no L4 result rather than an error.
//
// BNK provides L4 through its own CRD instead. `protocol` carries no enum on
// that CRD, so its accepted values are controller-validated.
func renderL4Route(name, ns, gateway, section, svc string, port int) string {
	sec := ""
	if section != "" {
		sec = fmt.Sprintf("\n    sectionName: %s", section)
	}
	return fmt.Sprintf(`apiVersion: %[8]s
kind: L4Route
metadata:
  name: %[1]s
  namespace: %[2]s
%[7]s
spec:
  parentRefs:
  - name: %[3]s
    namespace: %[2]s%[4]s
  protocol: TCP
  rules:
  - backendRefs:
    - name: %[5]s
      port: %[6]d
`, name, ns, gateway, sec, svc, port, labelsBlock("  "), L4RouteKind.APIVersion())
}

func renderTLSSecret(name, ns string, certPEM, keyPEM []byte) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %[1]s
  namespace: %[2]s
%[5]s
type: kubernetes.io/tls
stringData:
  tls.crt: |
%[3]s
  tls.key: |
%[4]s
`, name, ns, indentPEM(certPEM), indentPEM(keyPEM), labelsBlock("  "))
}

func indentPEM(b []byte) string {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	for i, l := range lines {
		lines[i] = "    " + l
	}
	return strings.Join(lines, "\n")
}

// GenSelfSignedCert returns a PEM cert+key for host, valid for a year.
// ECDSA P-256 — small and universally accepted; the load test never
// validates the chain, so a self-signed leaf is sufficient to exercise
// the TLS-terminate-at-TMM path.
func GenSelfSignedCert(host string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host},
		DNSNames:              []string{host},
		NotBefore:             selfSignedNotBefore(),
		NotAfter:              selfSignedNotBefore().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// selfSignedNotBefore is a seam so the cert isn't rejected for clock
// skew; backdated five minutes. A var (not time.Now inline) keeps the
// generator deterministic enough to test the PEM shape.
func selfSignedNotBefore() time.Time { return time.Now().Add(-5 * time.Minute) }
