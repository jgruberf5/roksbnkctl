package ibm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/IBM/platform-services-go-sdk/iamidentityv1"
)

// Sprint 9 / PRD 04 — trusted profile client unit tests.
//
// The IBM SDK doesn't expose a typed mock seam, so we exercise the
// HTTP layer via httptest. Two patterns covered:
//
//  1. Happy path — fake IAM endpoint returns the expected JSON shapes
//     for the IAM-token exchange, ListProfiles (idempotency lookup),
//     CreateProfile, CreateLink. Asserts the typed TrustedProfile is
//     fully populated.
//  2. Permission failure — fake IAM endpoint returns 403 on the
//     trusted-profile endpoint. Asserts errors.Is(err, ErrIAMPermDenied)
//     so the auto-fallback path in ops install matches cleanly.

// newTestProfileClient wires a Client at the test server's URL. Sets a
// cached Identity so the trusted-profile methods skip the IAM-token
// exchange round-trip (we test the trusted-profile endpoints in
// isolation; the identity-verify path has its own integration
// coverage in client_test.go).
func newTestProfileClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	// Use a no-auth authenticator so the SDK doesn't try to mint an
	// IAM token. The test server doesn't care about Authorization
	// headers.
	auth, err := core.NewNoAuthAuthenticator()
	if err != nil {
		t.Fatalf("noauth: %v", err)
	}
	iam, err := iamidentityv1.NewIamIdentityV1(&iamidentityv1.IamIdentityV1Options{
		URL:           srv.URL,
		Authenticator: auth,
	})
	if err != nil {
		t.Fatalf("NewIamIdentityV1: %v", err)
	}
	c := &Client{
		apiKey: "test-api-key",
		region: "us-south",
		iam:    iam,
		identity: &Identity{
			IAMID:     "IBMid-TEST",
			AccountID: "acct-12345",
		},
	}
	return c
}

func TestTrustedProfile_CreateForOpsPod_HappyPath(t *testing.T) {
	const (
		profileName = "roksbnkctl-ops-canada-roks"
		profileID   = "Profile-abc123"
		iamID       = "profile-abc123"
		crn         = "crn:v1:bluemix:public:iam-identity:us-south:a/acct-12345::profile:Profile-abc123"
		clusterCRN  = "crn:v1:bluemix:public:containers-kubernetes:us-south:a/acct-12345::cluster:cluster123"
	)
	listCalls := 0
	createCalls := 0
	linkCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/profiles") && !strings.Contains(r.URL.Path, "Profile-"):
			// ListProfiles call — return empty so create path runs.
			listCalls++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"profiles": []map[string]any{},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/v1/profiles"):
			createCalls++
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["name"] != profileName {
				t.Errorf("CreateProfile body name: got %v, want %s", body["name"], profileName)
			}
			if body["account_id"] != "acct-12345" {
				t.Errorf("CreateProfile body account_id: got %v, want acct-12345", body["account_id"])
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         profileID,
				"entity_tag": "etag-1",
				"crn":        crn,
				"name":       profileName,
				"iam_id":     iamID,
				"account_id": "acct-12345",
			})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/v1/profiles/"+profileID+"/links"):
			linkCalls++
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["cr_type"] != "ROKS_SA" {
				t.Errorf("CreateLink cr_type: got %v, want ROKS_SA", body["cr_type"])
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         "ProfileLink-link1",
				"entity_tag": "etag-1",
				"cr_type":    "ROKS_SA",
				"link": map[string]any{
					"crn":       clusterCRN,
					"namespace": "roksbnkctl-ops",
					"name":      "roksbnkctl-ops",
				},
			})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newTestProfileClient(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tp, err := c.TrustedProfiles().CreateForOpsPod(ctx, profileName, clusterCRN, "roksbnkctl-ops", "roksbnkctl-ops")
	if err != nil {
		t.Fatalf("CreateForOpsPod: %v", err)
	}
	if tp == nil {
		t.Fatal("expected non-nil TrustedProfile")
	}
	if tp.ID != profileID {
		t.Errorf("ID: got %q, want %q", tp.ID, profileID)
	}
	if tp.IAMID != iamID {
		t.Errorf("IAMID: got %q, want %q", tp.IAMID, iamID)
	}
	if tp.CRN != crn {
		t.Errorf("CRN: got %q, want %q", tp.CRN, crn)
	}
	if tp.Name != profileName {
		t.Errorf("Name: got %q, want %q", tp.Name, profileName)
	}
	if tp.AccountID != "acct-12345" {
		t.Errorf("AccountID: got %q, want acct-12345", tp.AccountID)
	}
	if listCalls != 1 {
		t.Errorf("listCalls: got %d, want 1", listCalls)
	}
	if createCalls != 1 {
		t.Errorf("createCalls: got %d, want 1", createCalls)
	}
	if linkCalls != 1 {
		t.Errorf("linkCalls: got %d, want 1", linkCalls)
	}
}

func TestTrustedProfile_CreateForOpsPod_Idempotent(t *testing.T) {
	const (
		profileName = "roksbnkctl-ops-existing"
		profileID   = "Profile-existing"
		iamID       = "profile-existing"
		crn         = "crn:v1:bluemix:public:iam-identity:us-south:a/acct-12345::profile:Profile-existing"
		clusterCRN  = "crn:v1:bluemix:public:containers-kubernetes:us-south:a/acct-12345::cluster:cluster123"
	)
	listCalls := 0
	createCalls := 0
	linkCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/profiles") && !strings.Contains(r.URL.Path, "Profile-"):
			listCalls++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"profiles": []map[string]any{
					{
						"id":         profileID,
						"entity_tag": "etag-1",
						"crn":        crn,
						"name":       profileName,
						"iam_id":     iamID,
						"account_id": "acct-12345",
					},
				},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/v1/profiles"):
			createCalls++
			w.WriteHeader(http.StatusConflict)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/v1/profiles/"+profileID+"/links"):
			linkCalls++
			// Simulate a duplicate-link response (409 conflict) →
			// ensureLink should swallow this.
			w.WriteHeader(http.StatusConflict)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newTestProfileClient(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tp, err := c.TrustedProfiles().CreateForOpsPod(ctx, profileName, clusterCRN, "roksbnkctl-ops", "roksbnkctl-ops")
	if err != nil {
		t.Fatalf("CreateForOpsPod (idempotent): %v", err)
	}
	if tp.ID != profileID {
		t.Errorf("idempotent ID: got %q, want %q", tp.ID, profileID)
	}
	if createCalls != 0 {
		t.Errorf("createCalls (idempotent path): got %d, want 0", createCalls)
	}
	if listCalls != 1 || linkCalls != 1 {
		t.Errorf("listCalls=%d linkCalls=%d (want 1 each)", listCalls, linkCalls)
	}
}

func TestTrustedProfile_CreateForOpsPod_IAMPermDenied(t *testing.T) {
	// 403 on the first endpoint we hit (ListProfiles) → must surface
	// as ErrIAMPermDenied so the auto-fallback path picks it up.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":[{"code":"insufficient_permissions","message":"caller missing iam-identity authority"}]}`))
	}))
	defer srv.Close()

	c := newTestProfileClient(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := c.TrustedProfiles().CreateForOpsPod(ctx, "roksbnkctl-ops-permdeny",
		"crn:v1:bluemix:public:containers-kubernetes:us-south:a/acct-12345::cluster:cluster123",
		"roksbnkctl-ops", "roksbnkctl-ops")
	if err == nil {
		t.Fatal("expected error on 403, got nil")
	}
	if !errors.Is(err, ErrIAMPermDenied) {
		t.Errorf("expected errors.Is(err, ErrIAMPermDenied); got %v", err)
	}
}

func TestTrustedProfile_Get(t *testing.T) {
	const (
		profileID = "Profile-get-test"
		iamID     = "profile-get-test"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/v1/profiles/"+profileID) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         profileID,
				"entity_tag": "etag-2",
				"crn":        "crn:profile",
				"name":       "roksbnkctl-ops-get",
				"iam_id":     iamID,
				"account_id": "acct-12345",
			})
			return
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestProfileClient(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tp, err := c.TrustedProfiles().Get(ctx, profileID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if tp.ID != profileID {
		t.Errorf("ID: got %q, want %q", tp.ID, profileID)
	}
	if tp.IAMID != iamID {
		t.Errorf("IAMID: got %q, want %q", tp.IAMID, iamID)
	}
}

func TestTrustedProfile_Delete_NotFoundIsNoOp(t *testing.T) {
	const profileID = "Profile-delete-404"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, profileID) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestProfileClient(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.TrustedProfiles().Delete(ctx, profileID); err != nil {
		t.Errorf("Delete on 404 should be no-op; got %v", err)
	}
}

func TestTrustedProfile_Delete_IAMPermDenied(t *testing.T) {
	const profileID = "Profile-delete-403"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := newTestProfileClient(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := c.TrustedProfiles().Delete(ctx, profileID)
	if !errors.Is(err, ErrIAMPermDenied) {
		t.Errorf("expected ErrIAMPermDenied; got %v", err)
	}
}

func TestClassifyIAMErr(t *testing.T) {
	cases := []struct {
		name        string
		resp        *core.DetailedResponse
		err         error
		wantPermErr bool
	}{
		{
			name:        "403 forbidden → ErrIAMPermDenied",
			resp:        &core.DetailedResponse{StatusCode: http.StatusForbidden},
			err:         errors.New("forbidden"),
			wantPermErr: true,
		},
		{
			name:        "401 with iam-identity body → ErrIAMPermDenied",
			resp:        &core.DetailedResponse{StatusCode: http.StatusUnauthorized},
			err:         errors.New("missing service authority iam-identity"),
			wantPermErr: true,
		},
		{
			name:        "401 without iam-identity body → wrapped, not perm-denied",
			resp:        &core.DetailedResponse{StatusCode: http.StatusUnauthorized},
			err:         errors.New("token expired"),
			wantPermErr: false,
		},
		{
			name:        "500 → wrapped, not perm-denied",
			resp:        &core.DetailedResponse{StatusCode: http.StatusInternalServerError},
			err:         errors.New("server fault"),
			wantPermErr: false,
		},
		{
			name:        "nil err → nil",
			resp:        nil,
			err:         nil,
			wantPermErr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyIAMErr(tc.resp, tc.err, "test op")
			if tc.err == nil {
				if got != nil {
					t.Errorf("got %v, want nil", got)
				}
				return
			}
			isPermErr := errors.Is(got, ErrIAMPermDenied)
			if isPermErr != tc.wantPermErr {
				t.Errorf("errors.Is(err, ErrIAMPermDenied)=%v, want %v (got err=%v)", isPermErr, tc.wantPermErr, got)
			}
		})
	}
}

// silence-the-linter helper if fmt is unused on some build paths.
var _ = fmt.Sprintf
