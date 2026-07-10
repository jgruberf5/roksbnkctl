// Package mirror is the Sprint 29 replication engine: it copies the BNK
// bill-of-materials (charts + images) from their source registries into a single
// private target, idempotently and concurrently. Most artifacts are OCI and copy
// registry-to-registry by digest via go-containerregistry (crane); the one
// classic Helm HTTP source (charts.jetstack.io) is pulled and repackaged as an
// OCI artifact via the helm binary (PRD 11 §3).
package mirror

import (
	"context"
	"fmt"
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
	return opts
}

// Replicate copies every BOM artifact into the target, bounded by Concurrency,
// and returns one Result per artifact (in BOM order). It never returns an error
// itself; per-artifact failures are carried in Result.Err so a partial run is
// fully reported.
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
			login := exec.CommandContext(ctx, e.helmBin(), "registry", "login", e.Target.PushHost(), "-u", ac.Username, "-p", ac.Password)
			if out, lerr := login.CombinedOutput(); lerr != nil {
				return Result{Artifact: a, Err: fmt.Errorf("helm registry login %s: %w: %s", e.Target.PushHost(), lerr, strings.TrimSpace(string(out)))}
			}
		}
	}
	push := exec.CommandContext(ctx, e.helmBin(), "push", tgz, dstRepo)
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
	for _, a := range bom.Artifacts {
		opts := e.craneOpts(ctx)
		if a.Kind == bnkbom.KindImage {
			// Match copyOCI: compare the amd64 platform digest, not the index.
			opts = append(opts, crane.WithPlatform(&v1.Platform{OS: "linux", Architecture: "amd64"}))
		}
		if a.SourceHost == classicHelmHost {
			// Classic-helm charts are repackaged; presence is verified by a
			// target-side existence check rather than a source-digest match.
			if _, err := crane.Digest(sanitizeRef(e.Target.PushRef(a)), opts...); err != nil {
				bad = append(bad, Result{Artifact: a, Err: fmt.Errorf("missing at target: %w", err)})
			}
			continue
		}
		srcDigest, err := crane.Digest(sanitizeRef(a.Ref()), opts...)
		if err != nil {
			bad = append(bad, Result{Artifact: a, Err: fmt.Errorf("resolve source: %w", err)})
			continue
		}
		dstDigest, err := crane.Digest(sanitizeRef(e.Target.PushRef(a)), opts...)
		if err != nil {
			bad = append(bad, Result{Artifact: a, Err: fmt.Errorf("missing at target: %w", err)})
			continue
		}
		if dstDigest != srcDigest {
			bad = append(bad, Result{Artifact: a, Digest: dstDigest, Err: fmt.Errorf("digest mismatch: source %s, target %s", srcDigest, dstDigest)})
		}
	}
	return bad
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
