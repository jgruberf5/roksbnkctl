package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A teardown runs when things are already partly gone, so the cases that matter
// are the absent ones: they must succeed, not error.
func TestUnregisterCluster(t *testing.T) {
	for _, tc := range []struct {
		name       string
		clusters   string
		deleteCode int
		wantID     int
		wantErr    bool
		wantDelete bool
	}{
		{name: "removes the named cluster", clusters: `{"clusters":[{"id":7,"name":"fdisco"}]}`,
			deleteCode: 200, wantID: 7, wantDelete: true},
		{name: "no cluster of that name is not an error", clusters: `{"clusters":[{"id":9,"name":"other"}]}`,
			wantID: 0},
		{name: "empty project is not an error", clusters: `{"clusters":[]}`, wantID: 0},
		{name: "already deleted (404) is success", clusters: `{"clusters":[{"id":7,"name":"fdisco"}]}`,
			deleteCode: 404, wantID: 7, wantDelete: true},
		{name: "a real delete failure surfaces", clusters: `{"clusters":[{"id":7,"name":"fdisco"}]}`,
			deleteCode: 500, wantErr: true, wantDelete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deleted := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet:
					_, _ = w.Write([]byte(tc.clusters))
				case r.Method == http.MethodDelete:
					deleted = true
					w.WriteHeader(tc.deleteCode)
				}
			}))
			defer srv.Close()

			id, err := New(srv.URL, true).UnregisterCluster(context.Background(), 1, "fdisco")
			if tc.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.wantErr && id != tc.wantID {
				t.Fatalf("id: got %d, want %d", id, tc.wantID)
			}
			if deleted != tc.wantDelete {
				t.Fatalf("delete issued=%v, want %v", deleted, tc.wantDelete)
			}
		})
	}
}

// Looking up a project must never create one — a destroy asking "is this still
// here?" should not bring it into being.
func TestProjectIDByNameNeverCreates(t *testing.T) {
	posted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posted = true
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": []any{}})
	}))
	defer srv.Close()

	id, err := New(srv.URL, true).ProjectIDByName(context.Background(), "nope")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 0 {
		t.Fatalf("id: got %d, want 0", id)
	}
	if posted {
		t.Fatal("ProjectIDByName created a project")
	}
}
