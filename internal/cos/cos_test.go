package cos

import (
	"strings"
	"testing"
)

// Sprint 15 code deliverable 3(c) — fold in internal/cos coverage
// (previously 0%) as a low-cost win while the consolidation is open.
// No live IBM Cloud calls: the bucket/object ops require a real S3
// endpoint, so these tests cover the pure endpoint/location-constraint
// helpers and the New() constructor's validation + non-dialing happy
// path (s3.New / session.NewSession do not perform network I/O).

func TestEndpointForRegion(t *testing.T) {
	cases := []struct {
		region string
		want   string
	}{
		{"us-south", "https://s3.us-south.cloud-object-storage.appdomain.cloud"},
		{"ca-tor", "https://s3.ca-tor.cloud-object-storage.appdomain.cloud"},
		{"eu-de", "https://s3.eu-de.cloud-object-storage.appdomain.cloud"},
		{"", "https://s3..cloud-object-storage.appdomain.cloud"},
	}
	for _, c := range cases {
		if got := EndpointForRegion(c.region); got != c.want {
			t.Errorf("EndpointForRegion(%q) = %q, want %q", c.region, got, c.want)
		}
	}
}

func TestLocationConstraint(t *testing.T) {
	cases := []struct {
		region, class, want string
	}{
		{"us-south", "standard", "us-south-standard"},
		{"ca-tor", "smart", "ca-tor-smart"},
		// Empty class defaults to "standard".
		{"us-south", "", "us-south-standard"},
		{"eu-gb", "cold", "eu-gb-cold"},
	}
	for _, c := range cases {
		if got := LocationConstraint(c.region, c.class); got != c.want {
			t.Errorf("LocationConstraint(%q,%q) = %q, want %q",
				c.region, c.class, got, c.want)
		}
	}
}

func TestNew_ValidationErrors(t *testing.T) {
	cases := []struct {
		name                        string
		apiKey, region, instanceCRN string
		wantErrSubstr               string
	}{
		{"empty api key", "", "us-south", "crn:v1:...", "api key is empty"},
		{"empty region", "key", "", "crn:v1:...", "region is empty"},
		{"empty instance CRN", "key", "us-south", "", "COS instance CRN is empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl, err := New(c.apiKey, c.region, c.instanceCRN)
			if err == nil {
				t.Fatalf("New(%q,%q,%q) = nil error, want %q",
					c.apiKey, c.region, c.instanceCRN, c.wantErrSubstr)
			}
			if cl != nil {
				t.Errorf("New returned a non-nil client alongside an error")
			}
			if !strings.Contains(err.Error(), c.wantErrSubstr) {
				t.Errorf("New error = %q, want substring %q", err, c.wantErrSubstr)
			}
		})
	}
}

func TestNew_HappyPath(t *testing.T) {
	// Valid args: constructs an S3 client. No network I/O happens until
	// an operation is invoked, so this is a deterministic unit test.
	c, err := New("dummy-api-key", "us-south", "crn:v1:bluemix:public:cloud-object-storage:global:a/acct:guid::")
	if err != nil {
		t.Fatalf("New(valid args) returned error: %v", err)
	}
	if c == nil {
		t.Fatal("New(valid args) returned nil client")
	}
	if c.region != "us-south" {
		t.Errorf("client region = %q, want %q", c.region, "us-south")
	}
	if c.s3 == nil {
		t.Error("client s3 handle is nil")
	}
	if c.instanceCRN == "" {
		t.Error("client instanceCRN not retained")
	}
}
