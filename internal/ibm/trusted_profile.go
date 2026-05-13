package ibm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/IBM/platform-services-go-sdk/iamidentityv1"
)

// TrustedProfile is a typed view of the IBM Cloud IAM trusted profile
// roksbnkctl provisions for the k8s ops pod. Sprint 9 / PRD 04
// §"Resolved in Sprint 9" §"Trusted-profile auto-provisioning" pins
// the design: `roksbnkctl ops install --trusted-profile=auto` (the new
// default) creates `roksbnkctl-ops-<workspace>` and links it to the
// ops pod's ServiceAccount via the projected SA token's OIDC issuer,
// so the pod assumes the profile at runtime and the static IBM Cloud
// API key never lands in a Kubernetes Secret.
//
// One TrustedProfile per ops-pod / workspace pair. The struct holds
// the canonical fields the caller needs after Create — the IAM ID
// (for annotating the SA), the CRN, the friendly name. Not safe for
// concurrent use across goroutines (the underlying SDK client isn't).
type TrustedProfile struct {
	// ID is the trusted profile's unique identifier
	// (Profile-<uuid>). Stamped as a SA annotation by the ops-install
	// path so subsequent `ops show` / `ops uninstall` runs can find
	// it without re-listing IAM.
	ID string

	// IAMID is the trusted profile's IAM ID
	// (profile-<id-without-prefix>). The IBM SDK auth path uses this
	// to mint short-lived IAM tokens against the profile when the
	// ops pod calls Assume.
	IAMID string

	// CRN is the cloud resource name. Logged by `ops show` so users
	// can grep IAM audit logs for the profile.
	CRN string

	// Name is the friendly name (`roksbnkctl-ops-<workspace>`).
	// Stable across runs so re-installs find an existing profile
	// rather than racing to create a duplicate.
	Name string

	// AccountID the profile belongs to. Same account the API key
	// authenticates against.
	AccountID string
}

// ErrIAMPermDenied is returned by trusted-profile operations when the
// caller's API key lacks the `iam-identity` service authority needed
// to create / read / delete trusted profiles. The auto-fallback path
// in `roksbnkctl ops install --trusted-profile=auto` switches on this
// sentinel to degrade gracefully to the v1.0.x static-key Secret path
// with a stderr warning.
//
// Surfaced via errors.Is so callers can match without depending on
// the underlying SDK error type. Wraps the raw SDK error for context
// (logs, debugging) without exposing IBM-specific error strings.
var ErrIAMPermDenied = errors.New("IAM perm 'iam-identity' missing")

// TrustedProfileClient is the typed wrapper around the SDK's
// iamidentityv1 client for trusted-profile lifecycle operations. The
// shape mirrors the other typed clients in this package (ResolveResourceGroup,
// CreateCOSInstance) — constructor wires through Client.New, methods
// hang off the wrapper.
type TrustedProfileClient struct {
	c *Client
}

// TrustedProfiles returns a TrustedProfileClient backed by this
// Client's API key + region. Cheap — no IAM round-trip; defer
// the I/O to the per-method calls.
func (c *Client) TrustedProfiles() *TrustedProfileClient {
	return &TrustedProfileClient{c: c}
}

// CreateForOpsPod creates a trusted profile named `name` in the
// caller's IBM Cloud account, and links it to the named Kubernetes
// ServiceAccount via a ROKS_SA compute-resource link. Returns the
// fully-populated TrustedProfile on success.
//
// `name` is the trusted profile's friendly name; convention is
// `roksbnkctl-ops-<workspace>` (the caller stamps the workspace in).
// `clusterCRN` is the IBM Cloud cluster CRN (used as the link's
// compute resource CRN). `saNamespace` + `saName` identify the
// in-cluster ServiceAccount the profile binds to — Sprint 9 uses
// the well-known ("roksbnkctl-ops", "roksbnkctl-ops") pair from
// `k8s_install.yaml`.
//
// Idempotency: if a profile with `name` already exists in the
// account, returns the existing profile (the caller's ops-install
// re-run hits this path — annotation says "already provisioned",
// no second profile is created). The link is created best-effort;
// duplicate-link errors are swallowed (the existing link is reused).
//
// Permission requirements: the caller's API key needs the
// `iam-identity` Service Authority. When perms are missing, the IBM
// API returns 403 — translated to ErrIAMPermDenied for the
// auto-fallback path in `roksbnkctl ops install`.
func (tpc *TrustedProfileClient) CreateForOpsPod(ctx context.Context, name, clusterCRN, saNamespace, saName string) (*TrustedProfile, error) {
	if name == "" {
		return nil, errors.New("trusted profile name is empty")
	}
	if clusterCRN == "" {
		return nil, errors.New("cluster CRN is empty (required for ROKS_SA link)")
	}
	if saNamespace == "" || saName == "" {
		return nil, errors.New("ServiceAccount namespace and name are required")
	}
	if tpc.c.identity == nil {
		if _, err := tpc.c.Verify(ctx); err != nil {
			return nil, fmt.Errorf("verifying credentials before trusted profile create: %w", err)
		}
	}
	accountID := tpc.c.identity.AccountID
	if accountID == "" {
		return nil, errors.New("could not determine account ID after Verify; cannot scope trusted profile")
	}

	// Idempotency: look up by name first. Treat a 403 here the same
	// as a 403 on Create — the caller's API key needs `iam-identity`
	// perms either way.
	existing, err := tpc.findByName(ctx, accountID, name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		// Best-effort link re-create. Duplicates are tolerated by the
		// link helper.
		if linkErr := tpc.ensureLink(ctx, existing.ID, clusterCRN, saNamespace, saName); linkErr != nil {
			return nil, linkErr
		}
		return existing, nil
	}

	createOpts := tpc.c.iam.NewCreateProfileOptions(name, accountID).
		SetDescription("Managed by roksbnkctl — ops pod trusted profile (Sprint 9 / PRD 04)")
	created, resp, err := tpc.c.iam.CreateProfileWithContext(ctx, createOpts)
	if err != nil {
		return nil, classifyIAMErr(resp, err, "create trusted profile")
	}

	tp := unmarshalTrustedProfile(created)
	if linkErr := tpc.ensureLink(ctx, tp.ID, clusterCRN, saNamespace, saName); linkErr != nil {
		return nil, linkErr
	}
	return tp, nil
}

// Get fetches an existing trusted profile by its ID. Returns a typed
// `*TrustedProfile` or ErrIAMPermDenied / a wrapped SDK error.
func (tpc *TrustedProfileClient) Get(ctx context.Context, profileID string) (*TrustedProfile, error) {
	if profileID == "" {
		return nil, errors.New("trusted profile ID is empty")
	}
	opts := tpc.c.iam.NewGetProfileOptions(profileID)
	got, resp, err := tpc.c.iam.GetProfileWithContext(ctx, opts)
	if err != nil {
		return nil, classifyIAMErr(resp, err, "get trusted profile")
	}
	return unmarshalTrustedProfile(got), nil
}

// Delete removes the trusted profile with the given ID. No-ops on
// 404 (already gone — desirable for `ops uninstall` idempotency).
// Surfaces ErrIAMPermDenied if perms are missing.
func (tpc *TrustedProfileClient) Delete(ctx context.Context, profileID string) error {
	if profileID == "" {
		return errors.New("trusted profile ID is empty")
	}
	opts := tpc.c.iam.NewDeleteProfileOptions(profileID)
	resp, err := tpc.c.iam.DeleteProfileWithContext(ctx, opts)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil
		}
		return classifyIAMErr(resp, err, "delete trusted profile")
	}
	return nil
}

// findByName looks up a trusted profile by friendly name within the
// caller's account. Returns nil, nil when no profile matches (so the
// caller's idempotent-create path can branch to Create). Translates
// 403 to ErrIAMPermDenied.
func (tpc *TrustedProfileClient) findByName(ctx context.Context, accountID, name string) (*TrustedProfile, error) {
	opts := tpc.c.iam.NewListProfilesOptions(accountID).SetName(name)
	res, resp, err := tpc.c.iam.ListProfilesWithContext(ctx, opts)
	if err != nil {
		return nil, classifyIAMErr(resp, err, "list trusted profiles")
	}
	if res == nil {
		return nil, nil
	}
	for i := range res.Profiles {
		p := &res.Profiles[i]
		if p.Name != nil && *p.Name == name {
			return unmarshalTrustedProfile(p), nil
		}
	}
	return nil, nil
}

// ensureLink creates (or no-ops on duplicate) the ROKS_SA compute
// resource link binding the profile to a Kubernetes ServiceAccount.
//
// The CrType is ROKS_SA (Red Hat OpenShift Kubernetes Service Service
// Account) — appropriate for ops pods running on ROKS clusters. IKS
// (vanilla Kubernetes Service) clusters use IKS_SA; the link
// validates server-side based on the cluster CRN, so callers don't
// need to pre-distinguish.
func (tpc *TrustedProfileClient) ensureLink(ctx context.Context, profileID, clusterCRN, saNamespace, saName string) error {
	link, err := tpc.c.iam.NewCreateProfileLinkRequestLink(clusterCRN, saNamespace)
	if err != nil {
		return fmt.Errorf("building trusted profile link request: %w", err)
	}
	link.Name = core.StringPtr(saName)

	opts := tpc.c.iam.NewCreateLinkOptions(profileID, "ROKS_SA", link).
		SetName("roksbnkctl-ops-sa")
	_, resp, err := tpc.c.iam.CreateLinkWithContext(ctx, opts)
	if err != nil {
		// 409 conflict means a link with the same CRN+namespace+name
		// already exists — treat as success for idempotency.
		if resp != nil && resp.StatusCode == http.StatusConflict {
			return nil
		}
		return classifyIAMErr(resp, err, "create trusted profile link")
	}
	return nil
}

// classifyIAMErr translates an SDK error into ErrIAMPermDenied when
// the underlying HTTP status indicates a permission problem (403, or
// 401 with an IAM-perm-shaped message), and wraps the raw error
// otherwise. Keeping the classifier in one place means the
// auto-fallback path in `roksbnkctl ops install` has a single sentinel
// to switch on.
func classifyIAMErr(resp *core.DetailedResponse, err error, op string) error {
	if err == nil {
		return nil
	}
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	msg := err.Error()
	// 403 is the canonical perm-denied shape. Some IAM endpoints
	// return 401 with a perm-denied body when the API key lacks the
	// `iam-identity` service authority; sniff the body string for
	// the IAM_SERVICE_AUTHORITY marker.
	if status == http.StatusForbidden ||
		(status == http.StatusUnauthorized && strings.Contains(msg, "iam-identity")) {
		return fmt.Errorf("%s: %w (raw: %v)", op, ErrIAMPermDenied, err)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// unmarshalTrustedProfile copies the pointer-dense SDK shape into the
// flat typed struct callers actually use. Nil-safe; missing required
// fields fall back to empty strings (the caller treats empty IAMID
// as "couldn't link the SA").
func unmarshalTrustedProfile(p *iamidentityv1.TrustedProfile) *TrustedProfile {
	if p == nil {
		return nil
	}
	out := &TrustedProfile{}
	if p.ID != nil {
		out.ID = *p.ID
	}
	if p.IamID != nil {
		out.IAMID = *p.IamID
	}
	if p.CRN != nil {
		out.CRN = *p.CRN
	}
	if p.Name != nil {
		out.Name = *p.Name
	}
	if p.AccountID != nil {
		out.AccountID = *p.AccountID
	}
	return out
}
