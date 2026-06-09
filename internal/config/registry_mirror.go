package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RegistryMirror is the persisted record of a populated air-gap mirror (Sprint
// 29 / PRD 11). The Registry phase writes it after `registry replicate`; the BNK
// phase reads it to redirect the install off far_repo_url. It is a sibling of
// cluster-outputs.json. The single FAR host splits into two: ChartHost (the
// registry route, host-reachable — the helm_release repository) and ImageHost
// (the in-cluster service, pod-reachable — image.repository + the CNEInstance
// spec.registry.uri).
type RegistryMirror struct {
	Target          string           `json:"target"`     // e.g. "openshift"
	Namespace       string           `json:"namespace"`  // the mirror project
	ChartHost       string           `json:"chart_host"` // "<route>/<ns>" — host-reachable
	ImageHost       string           `json:"image_host"` // "<svc:5000>/<ns>" — pod-reachable
	ManifestVersion string           `json:"manifest_version"`
	RecordedAt      time.Time        `json:"recorded_at"`
	Artifacts       []MirrorArtifact `json:"artifacts,omitempty"`
}

// MirrorArtifact records one mirrored artifact + the digest it resolved to, so
// `registry verify` and the image-by-digest pull refs are reproducible.
type MirrorArtifact struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Tag    string `json:"tag"`
	Digest string `json:"digest,omitempty"`
}

// ErrNoRegistryMirror — the workspace has no registry-mirror.json. Sentinel so
// callers can distinguish "no mirror configured" (install from FAR directly)
// from a real I/O error.
var ErrNoRegistryMirror = errors.New("workspace has no registry-mirror.json — run `roksbnkctl registry replicate` to populate the mirror")

// WorkspaceRegistryMirrorPath is ~/.roksbnkctl/<workspace>/registry-mirror.json.
func WorkspaceRegistryMirrorPath(name string) (string, error) {
	dir, err := WorkspaceDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "registry-mirror.json"), nil
}

// ReadRegistryMirror loads the record for workspace, or ErrNoRegistryMirror if
// the file does not exist.
func ReadRegistryMirror(workspace string) (*RegistryMirror, error) {
	p, err := WorkspaceRegistryMirrorPath(workspace)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoRegistryMirror
		}
		return nil, fmt.Errorf("reading %s: %w", p, err)
	}
	var m RegistryMirror
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", p, err)
	}
	return &m, nil
}

// WriteRegistryMirror persists the record for workspace, stamping RecordedAt if
// zero.
func WriteRegistryMirror(workspace string, m *RegistryMirror) error {
	if m == nil {
		return errors.New("nil RegistryMirror")
	}
	if m.RecordedAt.IsZero() {
		m.RecordedAt = time.Now().UTC()
	}
	p, err := WorkspaceRegistryMirrorPath(workspace)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if err := os.WriteFile(p, body, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", p, err)
	}
	return nil
}
