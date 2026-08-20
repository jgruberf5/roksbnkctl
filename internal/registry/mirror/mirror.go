// Package mirror is the Sprint 29 replication engine: it copies the BNK
// bill-of-materials (charts + images) from their source registries into a single
// private target, idempotently and concurrently. Most artifacts are OCI and copy
// registry-to-registry by digest via go-containerregistry (crane); the one
// classic Helm HTTP source (charts.jetstack.io) is pulled and repackaged as an
// OCI artifact via the helm binary (PRD 11 §3).
package mirror

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
)

// classicHelmHost is the one BOM source that is a classic Helm HTTP repo (an
// index.yaml + chart tarballs), not an OCI registry — it must be pulled and
// repushed as an OCI artifact rather than crane-copied.
const classicHelmHost = "charts.jetstack.io"

// Target is the push destination the engine copies into. Sprint 29 Stage 3's
// RegistryTarget satisfies this (its PushEndpoint host + an artifact's path);
// the fake-registry test supplies its own.
type Target interface {
	// PushRef returns the destination reference for an artifact, e.g.
	// "<host>/<ns>/images/tmm-img:<tag>" or "<host>/<ns>/charts/f5-tmm:<tag>".
	PushRef(a bnkbom.Artifact) string
	// PushHost is the destination registry host (for keychain routing + the
	// classic-helm `helm push`).
	PushHost() string
	// PushAuth authenticates pushes to the target.
	PushAuth() authn.Authenticator
}

// SourceAuth resolves a pull authenticator for a source registry host (FAR
// credentials for repo.f5.com; anonymous for quay.io / docker.io). Returning nil
// means anonymous.
type SourceAuth func(host string) authn.Authenticator

// Result records the outcome of mirroring one artifact.
type Result struct {
	Artifact bnkbom.Artifact
	Digest   string // the resolved/pushed digest
	Skipped  bool   // already present at the target at the same digest
	Err      error
}

// Engine copies BOM artifacts from their sources into Target.
type Engine struct {
	Target      Target
	SourceAuth  SourceAuth
	Concurrency int    // bounded worker pool; <=0 means a default
	HelmBin     string // helm binary for the classic-helm path; "" means "helm"
	ScratchDir  string // working dir for helm pull/push; "" means a temp dir
	Insecure    bool   // allow plain-HTTP registries (tests; never for production targets)
	// RegistryCA is the PEM CA (chain) the TARGET mirror serves TLS with, when it
	// is a private/self-signed registry (a co-located Harbor by private IP). The
	// operator running replicate — e.g. the roksbnkctl-tools-runner CONTAINER in a
	// CI/GitOps flow — has no OS trust for it, so crane's default transport fails
	// the push with x509 "unknown authority". When set, it is added to the system
	// root pool for the copy transport so the push (and any TLS pull) trusts it.
	// Empty for public targets (their chain is covered by the default roots).
	RegistryCA string
}

func (e *Engine) concurrency() int {
	if e.Concurrency > 0 {
		return e.Concurrency
	}
	return 2 // the OpenShift internal registry 500s under heavier concurrency
}

func (e *Engine) helmBin() string {
	if e.HelmBin != "" {
		return e.HelmBin
	}
	return "helm"
}

// keychain routes auth per registry host: the target host gets the push
// credential, known source hosts get their pull credential, everything else is
// anonymous.
type keychain struct {
	targetHost string
	targetAuth authn.Authenticator
	sourceAuth SourceAuth
}

func (k keychain) Resolve(res authn.Resource) (authn.Authenticator, error) {
	host := res.RegistryStr()
	if host == k.targetHost && k.targetAuth != nil {
		return k.targetAuth, nil
	}
	if k.sourceAuth != nil {
		if a := k.sourceAuth(host); a != nil {
			return a, nil
		}
	}
	return authn.Anonymous, nil
}

func (e *Engine) keychain() keychain {
	return keychain{targetHost: e.Target.PushHost(), targetAuth: e.Target.PushAuth(), sourceAuth: e.SourceAuth}
}

func (e *Engine) craneOpts(ctx context.Context) []crane.Option {
	opts := []crane.Option{crane.WithContext(ctx), crane.WithAuthFromKeychain(e.keychain())}
	if e.Insecure {
		opts = append(opts, crane.Insecure)
	}
	if tr := e.caTransport(); tr != nil {
		opts = append(opts, crane.WithTransport(tr))
	}
	return opts
}

// caTransport returns an http transport that trusts RegistryCA on top of the
// system roots, or nil when no private CA is configured (use crane's default).
// The same transport serves both the source pull (public CAs, from the system
// pool) and the target push (the private mirror CA), so a container operator
// with no OS trust for the mirror can still replicate into it. The additive
// pool is deliberate — contrast forge.New, which pins EXCLUSIVELY because that
// client talks to exactly one server; do not "harmonize" the two.
func (e *Engine) caTransport() http.RoundTripper {
	if strings.TrimSpace(e.RegistryCA) == "" {
		return nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM([]byte(e.RegistryCA)) {
		return nil // unparseable CA — fall back to the default transport
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if tr.TLSClientConfig == nil {
		tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	tr.TLSClientConfig.RootCAs = pool
	return tr
}

// PreflightAuth checks the target credential ONCE, before any copying starts.
//
// isTransient treats 401 as retryable, because Harbor's token service really does
// intermittently 401 a single repository in an otherwise-healthy run. But that
// makes a genuinely WRONG credential retryable too: every artifact in the BOM
// would 401, be retried copyMaxAttempts times with a backoff, and the command
// would grind for minutes before reporting a wall of failures — instead of saying
// "your mirror password is wrong" in one second.
//
// A HEAD against a destination ref distinguishes the two cleanly: a VALID
// credential answers 404/403 for an artifact that is not there yet, while an
// INVALID one answers 401. So a 401 here means the credential itself is bad.
// Nothing is pushed.
//
// Returns nil when the BOM is empty or the target is reachable+authorized.
func (e *Engine) PreflightAuth(ctx context.Context, bom *bnkbom.BOM) error {
	if bom == nil || len(bom.Artifacts) == 0 {
		return nil
	}
	// Probe with a real destination ref so the auth scope matches the pushes.
	probe := e.Target.PushRef(bom.Artifacts[0])
	_, err := crane.Head(sanitizeRef(probe), e.craneOpts(ctx)...)
	if err == nil {
		return nil // already present + authorized
	}
	if isAuthRejection(err) {
		return fmt.Errorf("the mirror rejected the credential for %s: %w\n"+
			"  check `roksbnkctl registry target` — set the password with "+
			"`registry target generic_password --password-stdin`", e.Target.PushHost(), err)
	}
	// 404 / "not found" / anything else: the credential worked (or the failure is
	// not an auth failure). Let the copy path handle it with its own retries.
	return nil
}

// isAuthRejection reports whether err is the registry refusing the credential, as
// opposed to the artifact simply not being there yet.
func isAuthRejection(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for _, sig := range []string{
		"status code 401", "401 Unauthorized", "UNAUTHORIZED", "unauthorized",
		"authentication required", "incorrect username or password",
	} {
		if strings.Contains(s, sig) {
			return true
		}
	}
	return false
}

// Replicate copies every BOM artifact into the target, bounded by Concurrency,
// and returns one Result per artifact (in BOM order). It never returns an error
// itself; per-artifact failures are carried in Result.Err so a partial run is
// fully reported. Call PreflightAuth first to fail fast on a bad credential.
func (e *Engine) Replicate(ctx context.Context, bom *bnkbom.BOM) []Result {
	results := make([]Result, len(bom.Artifacts))
	sem := make(chan struct{}, e.concurrency())
	var wg sync.WaitGroup
	for i := range bom.Artifacts {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = e.copyOne(ctx, bom.Artifacts[i])
		}(i)
	}
	wg.Wait()
	return results
}

func (e *Engine) copyOne(ctx context.Context, a bnkbom.Artifact) Result {
	if a.SourceHost == classicHelmHost {
		return e.copyClassicHelmChart(ctx, a)
	}
	return e.copyOCI(ctx, a)
}

// copyOCI copies an OCI artifact (any image, and the repo.f5.com helm OCI charts)
// by digest. Idempotent: if the destination already resolves to the same digest
// as the source, the copy is skipped.
func (e *Engine) copyOCI(ctx context.Context, a bnkbom.Artifact) Result {
	src := sanitizeRef(a.Ref())
	dst := sanitizeRef(e.Target.PushRef(a))
	opts := e.craneOpts(ctx)
	if a.Kind == bnkbom.KindImage {
		// Flatten multi-arch images to the cluster's platform: the OpenShift
		// internal registry 500s on manifest-list (image-index) pushes, and ROKS
		// is linux/amd64, so a single-platform image is all the cluster needs.
		opts = append(opts, crane.WithPlatform(&v1.Platform{OS: "linux", Architecture: "amd64"}))
	}

	srcDigest, err := crane.Digest(src, opts...)
	if err != nil {
		return Result{Artifact: a, Err: fmt.Errorf("resolve source %s: %w", src, err)}
	}
	if dstDigest, err := crane.Digest(dst, opts...); err == nil && dstDigest == srcDigest {
		return Result{Artifact: a, Digest: srcDigest, Skipped: true}
	}
	// Registries intermittently fail under concurrent pushes (OpenShift 5xx,
	// Harbor 401 from its token service); retry transient failures with a linear
	// backoff.
	var copyErr error
	for attempt := 1; attempt <= copyMaxAttempts; attempt++ {
		if copyErr = crane.Copy(src, dst, opts...); copyErr == nil {
			return Result{Artifact: a, Digest: srcDigest}
		}
		if !isTransient(copyErr) {
			break
		}
		select {
		case <-ctx.Done():
			return Result{Artifact: a, Err: ctx.Err()}
		case <-time.After(time.Duration(attempt) * 2 * time.Second):
		}
	}
	return Result{Artifact: a, Err: fmt.Errorf("copy %s -> %s: %w", src, dst, copyErr)}
}

const copyMaxAttempts = 4

// sanitizeRef makes an OCI reference valid: helm-OCI stores a chart version's "+"
// build metadata as "_" (OCI tags can't contain "+"), so mirror the same.
func sanitizeRef(ref string) string { return strings.ReplaceAll(ref, "+", "_") }

// isTransient reports whether a copy error is worth retrying — the OpenShift
// registry returns intermittent 5xx under concurrent pushes, and Harbor's token
// service intermittently 401s one repository in an otherwise-succeeding run.
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for _, p := range []string{
		"Internal Server Error", "status code 500", "status code 502",
		"status code 503", "status code 504", "TOOMANYREQUESTS", "toomanyrequests",
		"connection reset", "unexpected EOF", "i/o timeout",
		"unexpected end of JSON input", "end of JSON input", "EOF",
		// Harbor's token service intermittently 401s a single repository under
		// concurrent pushes, while every other artifact in the same run succeeds
		// on the same credential. A genuinely bad credential still fails: it just
		// exhausts copyMaxAttempts first.
		"status code 401", "401 Unauthorized",
	} {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// copyClassicHelmChart handles the one non-OCI source: pull the chart tarball
// from the classic Helm HTTP repo, then `helm push` it as an OCI artifact to the
// target. Requires the helm binary + a prior `helm registry login` to the target
// (the caller arranges target auth).
func (e *Engine) copyClassicHelmChart(ctx context.Context, a bnkbom.Artifact) Result {
	scratch := e.ScratchDir
	if scratch == "" {
		d, err := os.MkdirTemp("", "bnk-mirror-helm-*")
		if err != nil {
			return Result{Artifact: a, Err: fmt.Errorf("scratch dir: %w", err)}
		}
		defer os.RemoveAll(d)
		scratch = d
	}
	// helm shells out and uses its OWN trust store (not crane's caTransport), so a
	// private/self-signed target mirror must be handed the CA explicitly or
	// `helm registry login` / `helm push` fail x509 "unknown authority" — the one
	// artifact (the classic-Helm cert-manager chart) that goes through helm, not crane.
	var caArgs []string
	if strings.TrimSpace(e.RegistryCA) != "" {
		caPath := filepath.Join(scratch, "mirror-ca.pem")
		if err := os.WriteFile(caPath, []byte(e.RegistryCA), 0o600); err != nil {
			return Result{Artifact: a, Err: fmt.Errorf("writing mirror CA for helm: %w", err)}
		}
		caArgs = []string{"--ca-file", caPath}
	}
	chart := pathBase(a.Name) // "cert-manager" from "cert-manager" (or a path)
	// helm pull <chart> --repo https://<host> --version <tag> -d <scratch>
	pull := exec.CommandContext(ctx, e.helmBin(), "pull", chart,
		"--repo", "https://"+a.SourceHost, "--version", a.Tag, "-d", scratch)
	if out, err := pull.CombinedOutput(); err != nil {
		return Result{Artifact: a, Err: fmt.Errorf("helm pull %s: %w: %s", a.Ref(), err, strings.TrimSpace(string(out)))}
	}
	tgz := filepath.Join(scratch, fmt.Sprintf("%s-%s.tgz", chart, a.Tag))
	// helm push <chart>.tgz oci://<targetHost>/<ns>/charts
	dstRepo := ociDir(e.Target.PushRef(a)) // strip the ":<tag>" + "/<chart>" → the OCI dir
	// helm shells out for the push, so (unlike crane, which uses the keychain) it
	// needs its own authenticated session to the target route — log it in first.
	if auth := e.Target.PushAuth(); auth != nil {
		if ac, aerr := auth.Authorization(); aerr == nil && ac != nil && (ac.Username != "" || ac.Password != "") {
			// --password-stdin, NOT -p — see the note in registry/source: current
			// helm exits non-zero on a CLI password instead of warning.
			loginArgs := append([]string{"registry", "login", e.Target.PushHost(), "-u", ac.Username, "--password-stdin"}, caArgs...)
			login := exec.CommandContext(ctx, e.helmBin(), loginArgs...)
			login.Stdin = strings.NewReader(ac.Password)
			if out, lerr := login.CombinedOutput(); lerr != nil {
				return Result{Artifact: a, Err: fmt.Errorf("helm registry login %s: %w: %s", e.Target.PushHost(), lerr, strings.TrimSpace(string(out)))}
			}
		}
	}
	push := exec.CommandContext(ctx, e.helmBin(), append([]string{"push", tgz, dstRepo}, caArgs...)...)
	if out, err := push.CombinedOutput(); err != nil {
		return Result{Artifact: a, Err: fmt.Errorf("helm push %s -> %s: %w: %s", tgz, dstRepo, err, strings.TrimSpace(string(out)))}
	}
	return Result{Artifact: a}
}

// pathBase returns the last path element of a chart name ("cert-manager").
func pathBase(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// ociDir turns a push ref "<host>/<ns>/charts/<chart>:<tag>" into the helm push
// target "oci://<host>/<ns>/charts" (helm push appends the chart name itself).
func ociDir(pushRef string) string {
	ref := pushRef
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i] // drop ":<tag>"
	}
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		ref = ref[:i] // drop "/<chart>"
	}
	return "oci://" + ref
}

// Verify checks that every BOM artifact is present at the target with a digest
// matching the source. Returns the artifacts that are missing or mismatched.
func (e *Engine) Verify(ctx context.Context, bom *bnkbom.BOM) []Result {
	var bad []Result
	for _, r := range e.VerifyAll(ctx, bom) {
		if r.Err != nil {
			bad = append(bad, r)
		}
	}
	return bad
}

// VerifyAll is Verify but returns one Result per BOM artifact, in order, with
// Result.Digest carrying the TARGET digest of each artifact that checked out.
//
// Verify discards those digests, which is fine when the answer is just pass/fail
// — but `registry adopt --verify-contents` records an artifact inventory, and an
// inventory without digests drives a TAG-based `registry delete` instead of the
// digest-based form the rest of the tool relies on. Adoption has just resolved
// every digest to do its comparison; throwing them away only to record a weaker
// record would be gratuitous.
func (e *Engine) VerifyAll(ctx context.Context, bom *bnkbom.BOM) []Result {
	results := make([]Result, 0, len(bom.Artifacts))
	for _, a := range bom.Artifacts {
		opts := e.craneOpts(ctx)
		if a.Kind == bnkbom.KindImage {
			// Match copyOCI: compare the amd64 platform digest, not the index.
			opts = append(opts, crane.WithPlatform(&v1.Platform{OS: "linux", Architecture: "amd64"}))
		}
		if a.SourceHost == classicHelmHost {
			// Classic-helm charts are repackaged; presence is verified by a
			// target-side existence check rather than a source-digest match.
			dstDigest, err := crane.Digest(sanitizeRef(e.Target.PushRef(a)), opts...)
			if err != nil {
				results = append(results, Result{Artifact: a, Err: fmt.Errorf("missing at target: %w", err)})
				continue
			}
			results = append(results, Result{Artifact: a, Digest: dstDigest})
			continue
		}
		srcDigest, err := crane.Digest(sanitizeRef(a.Ref()), opts...)
		if err != nil {
			results = append(results, Result{Artifact: a, Err: fmt.Errorf("resolve source: %w", err)})
			continue
		}
		dstDigest, err := crane.Digest(sanitizeRef(e.Target.PushRef(a)), opts...)
		if err != nil {
			results = append(results, Result{Artifact: a, Err: fmt.Errorf("missing at target: %w", err)})
			continue
		}
		if dstDigest != srcDigest {
			results = append(results, Result{Artifact: a, Digest: dstDigest, Err: fmt.Errorf("digest mismatch: source %s, target %s", srcDigest, dstDigest)})
			continue
		}
		results = append(results, Result{Artifact: a, Digest: dstDigest})
	}
	return results
}

// Delete removes the given artifacts from the target registry — by digest when
// the artifact carries one (the reliable form for a registry manifest DELETE),
// else by the push tag. It returns one Result per artifact (in order); a
// per-artifact failure is carried in Result.Err so a partial delete is fully
// reported (a registry that disallows deletes, or an artifact already gone,
// surfaces there). Sequential — delete is light, and ordered output reads
// cleanly.
func (e *Engine) Delete(ctx context.Context, artifacts []bnkbom.Artifact) []Result {
	opts := e.craneOpts(ctx)
	results := make([]Result, 0, len(artifacts))
	for _, a := range artifacts {
		ref := sanitizeRef(e.Target.PushRef(a))
		if a.Digest != "" {
			if parsed, perr := name.ParseReference(ref); perr == nil {
				ref = parsed.Context().Digest(a.Digest).String()
			}
		}
		results = append(results, Result{Artifact: a, Digest: a.Digest, Err: crane.Delete(ref, opts...)})
	}
	return results
}

// ProbeNamespace counts the repositories the mirror already holds under the
// target's repo prefix, WITHOUT consulting the source. It is the check `registry
// adopt` uses: adoption records a mirror this workspace did not populate, and the
// whole point is that it must work when the FAR source is unreachable — so it
// cannot build a BOM and cannot compare digests. What it can do is ask the mirror
// what it holds.
//
// This is a sanity check, not a proof. It catches the mistakes adoption actually
// invites — a typo in the repo prefix, an empty registry, a credential that
// cannot read — and it deliberately does not attempt to establish that the
// contents are correct or complete. Use Verify for that, when the source is
// reachable.
//
// Returns the number of matching repositories. A registry that does not support
// the catalog endpoint (or forbids it) returns an error the caller can downgrade
// to a warning rather than a failure — not every registry exposes _catalog, and
// being unable to look is different from looking and finding nothing.
func (e *Engine) ProbeNamespace(ctx context.Context, prefix string) (int, error) {
	host := e.Target.PushHost()
	if host == "" {
		return 0, fmt.Errorf("the target has no push host configured")
	}
	repos, err := crane.Catalog(host, e.craneOpts(ctx)...)
	if err != nil {
		if isAuthRejection(err) {
			return 0, fmt.Errorf("the mirror rejected the credential for %s: %w", host, err)
		}
		return 0, fmt.Errorf("listing repositories on %s: %w", host, err)
	}
	if prefix == "" {
		return len(repos), nil
	}
	n := 0
	for _, r := range repos {
		if r == prefix || strings.HasPrefix(r, prefix+"/") {
			n++
		}
	}
	return n, nil
}
