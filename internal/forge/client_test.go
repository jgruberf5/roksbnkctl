package forge

import (
	"encoding/json"
	"testing"
)

// TestCreateProjectResponse_UnmarshalJSON validates that both the live flat
// shape forge currently emits and the legacy nested shape are parsed correctly.
func TestCreateProjectResponse_UnmarshalJSON(t *testing.T) {
	cases := []struct {
		name      string
		json      string
		wantID    int
		wantName  string
		wantError bool
	}{
		{
			// Exact payload captured live from the forge MCP server (2026-05-26).
			name:     "live flat shape",
			json:     `{"success":true,"project_id":39,"name":"awsbnkctl-syd-tracer","message":"Project created successfully"}`,
			wantID:   39,
			wantName: "awsbnkctl-syd-tracer",
		},
		{
			// Legacy nested envelope — must remain parseable for forward-compat.
			name:     "legacy nested shape",
			json:     `{"project":{"id":11,"name":"awsbnkctl-default"},"success":true}`,
			wantID:   11,
			wantName: "awsbnkctl-default",
		},
		{
			// Both shapes present — flat project_id must win.
			name:     "both shapes present flat wins",
			json:     `{"project":{"id":5,"name":"nested-name"},"project_id":39,"name":"flat-name","success":true}`,
			wantID:   39,
			wantName: "nested-name", // nested project.name is populated, so it is used
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r CreateProjectResponse
			err := json.Unmarshal([]byte(tc.json), &r)
			if (err != nil) != tc.wantError {
				t.Fatalf("Unmarshal error = %v, wantError = %v", err, tc.wantError)
			}
			if err != nil {
				return
			}
			if r.Project.ID != tc.wantID {
				t.Errorf("Project.ID = %d, want %d", r.Project.ID, tc.wantID)
			}
			if r.Project.Name != tc.wantName {
				t.Errorf("Project.Name = %q, want %q", r.Project.Name, tc.wantName)
			}
		})
	}
}

// TestCreateClusterResponse_UnmarshalJSON validates that both the live bare
// top-level shape forge currently emits and the legacy nested shape are parsed.
func TestCreateClusterResponse_UnmarshalJSON(t *testing.T) {
	cases := []struct {
		name      string
		json      string
		wantID    int
		wantName  string
		wantError bool
	}{
		{
			// Exact payload captured live from the forge MCP server (2026-05-26).
			name:     "live bare top-level shape",
			json:     `{"id":23,"name":"syd-tracer","context":"arn:aws:eks:ap-southeast-2:123456789012:cluster/syd-tracer","api_server":"https://ABCDEF1234567890.gr7.ap-southeast-2.eks.amazonaws.com","cloud_provider":"aws","region":"ap-southeast-2","status":"active","project_id":39,"default_namespace":"default"}`,
			wantID:   23,
			wantName: "syd-tracer",
		},
		{
			// Legacy nested envelope — must remain parseable for forward-compat.
			name:     "legacy nested shape",
			json:     `{"cluster":{"id":99,"name":"bnk-prod"},"success":true}`,
			wantID:   99,
			wantName: "bnk-prod",
		},
		{
			// Both shapes present — top-level id must win.
			name:     "both shapes present top-level wins",
			json:     `{"cluster":{"id":5,"name":"nested-cluster"},"id":23,"name":"flat-cluster","success":true}`,
			wantID:   23,
			wantName: "nested-cluster", // nested cluster.name is populated, so it is used
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r CreateClusterResponse
			err := json.Unmarshal([]byte(tc.json), &r)
			if (err != nil) != tc.wantError {
				t.Fatalf("Unmarshal error = %v, wantError = %v", err, tc.wantError)
			}
			if err != nil {
				return
			}
			if r.Cluster.ID != tc.wantID {
				t.Errorf("Cluster.ID = %d, want %d", r.Cluster.ID, tc.wantID)
			}
			if r.Cluster.Name != tc.wantName {
				t.Errorf("Cluster.Name = %q, want %q", r.Cluster.Name, tc.wantName)
			}
		})
	}
}

// TestCreateProjectResponse_ZeroIDGuardedAfterParsing verifies that a response
// with no ID field (neither flat nor nested) still yields ID==0 so the caller's
// zero-ID guard can fire.
func TestCreateProjectResponse_ZeroIDGuardedAfterParsing(t *testing.T) {
	var r CreateProjectResponse
	if err := json.Unmarshal([]byte(`{"success":true,"message":"ok"}`), &r); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Project.ID != 0 {
		t.Errorf("Project.ID = %d, want 0 (so caller zero-ID guard fires)", r.Project.ID)
	}
}

// TestCreateClusterResponse_ZeroIDGuardedAfterParsing mirrors the above for clusters.
func TestCreateClusterResponse_ZeroIDGuardedAfterParsing(t *testing.T) {
	var r CreateClusterResponse
	if err := json.Unmarshal([]byte(`{"success":true,"message":"ok"}`), &r); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Cluster.ID != 0 {
		t.Errorf("Cluster.ID = %d, want 0 (so caller zero-ID guard fires)", r.Cluster.ID)
	}
}
