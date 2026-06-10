// Package bnkbom builds the bill-of-materials (BOM) of artifacts a BNK install
// needs — every helm chart and container image — by parsing the
// f5-bigip-k8s-manifest that FAR publishes per BNK release, and unioning the
// non-F5 dependencies roksbnkctl installs that the manifest does not cover
// (cert-manager from Jetstack, bitnami/kubectl for the node-labeler).
//
// The BOM is the input to the Sprint 29 registry mirror (PRD 11): every artifact
// is copied from its source registry into a private target registry, after which
// the BNK install is redirected to pull from there. Confirmed against the real
// 2.3.0 manifest, that document is a flat, complete enumeration (helm_charts[] +
// docker_images[], tag-pinned), so this is a pure YAML parse — no chart render.
package bnkbom

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Kind distinguishes a helm chart from a container image. They copy by different
// mechanisms (ORAS vs. crane) and, on the OpenShift internal-registry target,
// resolve to different pull endpoints (the host's helm provider over the route
// vs. the cluster's kubelet over the in-cluster service).
type Kind string

const (
	KindChart Kind = "chart"
	KindImage Kind = "image"
)

// Origin records how an artifact entered the BOM — for reporting and for the
// --include-deps toggle (the non-F5 deps can be excluded).
type Origin string

const (
	OriginManifest    Origin = "manifest"     // from the F5 f5-bigip-k8s-manifest
	OriginCertManager Origin = "cert-manager" // non-F5: Jetstack cert-manager
	OriginNodeLabeler Origin = "node-labeler" // non-F5: bitnami/kubectl
)

// Artifact is one chart or image to mirror: its source registry host, its
// repository path within that host, and its tag. Digest is empty in the BOM; the
// replication engine resolves the tag and fills it at copy time.
type Artifact struct {
	Kind       Kind   `json:"kind"`
	SourceHost string `json:"source_host"` // e.g. "repo.f5.com", "quay.io", "docker.io", "charts.jetstack.io"
	Name       string `json:"name"`        // repository path, e.g. "charts/f5-tmm", "images/tmm-img", "jetstack/cert-manager-controller"
	Tag        string `json:"tag"`
	Origin     Origin `json:"origin"`
	Digest     string `json:"digest,omitempty"` // set by the mirror at copy time
}

// Ref is the source pull reference, "<host>/<name>:<tag>".
func (a Artifact) Ref() string { return fmt.Sprintf("%s/%s:%s", a.SourceHost, a.Name, a.Tag) }

// BOM is the full, deterministically-ordered set of artifacts a BNK install
// needs.
type BOM struct {
	ManifestVersion string     `json:"manifest_version"`
	Artifacts       []Artifact `json:"artifacts"`
}

// Counts returns how many charts and images the BOM contains.
func (b *BOM) Counts() (charts, images int) {
	for _, a := range b.Artifacts {
		switch a.Kind {
		case KindChart:
			charts++
		case KindImage:
			images++
		}
	}
	return charts, images
}

// sortArtifacts orders the BOM deterministically (kind, host, name, tag) so
// `registry bom`, `diff`, and `verify` are stable across runs.
func (b *BOM) sortArtifacts() {
	sort.Slice(b.Artifacts, func(i, j int) bool {
		a, c := b.Artifacts[i], b.Artifacts[j]
		switch {
		case a.Kind != c.Kind:
			return a.Kind < c.Kind
		case a.SourceHost != c.SourceHost:
			return a.SourceHost < c.SourceHost
		case a.Name != c.Name:
			return a.Name < c.Name
		default:
			return a.Tag < c.Tag
		}
	})
}

// Options controls BOM assembly.
type Options struct {
	// ManifestVersion selects the release within the manifest. May be empty when
	// the manifest contains exactly one release.
	ManifestVersion string
	// IncludeDeps unions the non-F5 dependency artifacts (cert-manager + the
	// node-labeler image) the F5 manifest does not cover.
	IncludeDeps bool
	// CertManagerVersion tags the Jetstack cert-manager chart + its quay.io
	// images (the roksbnkctl-rendered cert_manager_version).
	CertManagerVersion string
	// NodeLabelerImageTag tags the bitnami/kubectl node-labeler image.
	NodeLabelerImageTag string
}

// Build parses the f5-bigip-k8s-manifest and, when opts.IncludeDeps is set,
// unions the non-F5 deps, returning the complete, deterministically-ordered BOM.
func Build(manifest []byte, opts Options) (*BOM, error) {
	bom, err := ParseManifest(manifest, opts.ManifestVersion)
	if err != nil {
		return nil, err
	}
	if opts.IncludeDeps {
		bom.Artifacts = append(bom.Artifacts, Deps(opts.CertManagerVersion, opts.NodeLabelerImageTag)...)
	}
	bom.sortArtifacts()
	return bom, nil
}

// ── f5-bigip-k8s-manifest parsing ───────────────────────────────────────────

// manifestDoc mirrors the f5-bigip-k8s-manifest YAML: a helm/docker source-host
// pair plus a list of releases, each enumerating its charts + images by
// {name, version}. The chart/image names already carry their repository path
// (charts/…, images/…, utils/…); the source host comes from the f5_helm_repo /
// f5_docker_repo fields.
type manifestDoc struct {
	F5HelmRepo   string `yaml:"f5_helm_repo"`   // e.g. "oci://repo.f5.com"
	F5DockerRepo string `yaml:"f5_docker_repo"` // e.g. "repo.f5.com"
	Releases     []struct {
		Version      string        `yaml:"version"`
		HelmCharts   []nameVersion `yaml:"helm_charts"`
		DockerImages []nameVersion `yaml:"docker_images"`
	} `yaml:"releases"`
}

type nameVersion struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

// ParseManifest parses an f5-bigip-k8s-manifest YAML document into the F5 portion
// of the BOM. version selects the release; when empty, the sole release is used
// (an error if the document carries more than one). Names/versions are trimmed —
// the real manifest has trailing whitespace on some entries.
func ParseManifest(data []byte, version string) (*BOM, error) {
	var doc manifestDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse f5-bigip-k8s-manifest: %w", err)
	}
	if len(doc.Releases) == 0 {
		return nil, fmt.Errorf("f5-bigip-k8s-manifest has no releases")
	}

	rel := doc.Releases[0]
	if version != "" {
		found := false
		for _, r := range doc.Releases {
			if strings.TrimSpace(r.Version) == version {
				rel, found = r, true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("f5-bigip-k8s-manifest has no release %q", version)
		}
	} else if len(doc.Releases) > 1 {
		return nil, fmt.Errorf("f5-bigip-k8s-manifest has %d releases; specify a version", len(doc.Releases))
	}

	helmHost := stripOCIScheme(doc.F5HelmRepo)
	dockerHost := strings.TrimSpace(doc.F5DockerRepo)
	if helmHost == "" || dockerHost == "" {
		return nil, fmt.Errorf("f5-bigip-k8s-manifest missing f5_helm_repo/f5_docker_repo")
	}

	bom := &BOM{ManifestVersion: strings.TrimSpace(rel.Version)}
	for _, c := range rel.HelmCharts {
		bom.Artifacts = append(bom.Artifacts, Artifact{
			Kind:       KindChart,
			SourceHost: helmHost,
			Name:       strings.TrimSpace(c.Name),
			Tag:        strings.TrimSpace(c.Version),
			Origin:     OriginManifest,
		})
	}
	for _, i := range rel.DockerImages {
		bom.Artifacts = append(bom.Artifacts, Artifact{
			Kind:       KindImage,
			SourceHost: dockerHost,
			Name:       strings.TrimSpace(i.Name),
			Tag:        strings.TrimSpace(i.Version),
			Origin:     OriginManifest,
		})
	}
	return bom, nil
}

func stripOCIScheme(s string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "oci://"))
}

// ── Non-F5 dependencies ─────────────────────────────────────────────────────

// certManagerImages are the standard Jetstack cert-manager container images
// (quay.io/jetstack), all tagged with the cert-manager version. roksbnkctl
// installs the Jetstack chart rather than the manifest's F5-packaged
// f5-cert-manager (Sprint 29 decision — PRD 11), so these are mirrored as
// non-manifest deps.
var certManagerImages = []string{
	"cert-manager-controller",
	"cert-manager-webhook",
	"cert-manager-cainjector",
	"cert-manager-startupapicheck",
	"cert-manager-acmesolver",
}

// Deps returns the non-F5 dependency artifacts the F5 manifest does not cover:
// the Jetstack cert-manager chart + its quay.io images at certManagerVersion, and
// the bitnami/kubectl node-labeler image at nodeLabelerImageTag. Versions are the
// roksbnkctl-rendered defaults (cert_manager_version + the node-labeler tag).
func Deps(certManagerVersion, nodeLabelerImageTag string) []Artifact {
	out := []Artifact{{
		Kind:       KindChart,
		SourceHost: "charts.jetstack.io",
		// charts/<name> so it carries a category like the F5 charts — the classic-
		// helm pull uses only the basename (pathBase), and a flat target maps it to
		// project "charts" while a nested target gets "<ns>/charts/cert-manager".
		Name:   "charts/cert-manager",
		Tag:    certManagerVersion,
		Origin: OriginCertManager,
	}}
	for _, img := range certManagerImages {
		out = append(out, Artifact{
			Kind:       KindImage,
			SourceHost: "quay.io",
			Name:       "jetstack/" + img,
			Tag:        certManagerVersion,
			Origin:     OriginCertManager,
		})
	}
	out = append(out, Artifact{
		Kind:       KindImage,
		SourceHost: "docker.io",
		Name:       "bitnami/kubectl",
		Tag:        nodeLabelerImageTag,
		Origin:     OriginNodeLabeler,
	})
	return out
}
