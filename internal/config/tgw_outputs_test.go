package config

import (
	"errors"
	"testing"
	"time"
)

func TestTGWOutputs_RoundTrip(t *testing.T) {
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())
	if err := SaveWorkspace("ws", &Workspace{}); err != nil {
		t.Fatal(err)
	}

	// Missing → sentinel, distinct from a read error.
	if _, err := ReadTGWOutputs("ws"); !errors.Is(err, ErrTGWOutputsMissing) {
		t.Fatalf("ReadTGWOutputs(missing) = %v, want ErrTGWOutputsMissing", err)
	}

	in := &TGWOutputs{
		GatewayID:      "r018-aaaa",
		GatewayName:    "shared-tgw",
		GatewayCRN:     "crn:v1:...:transit-gateway:r018-aaaa",
		ConnectionID:   "conn-123",
		ConnectionName: "ws-prefix",
		VPCID:          "r018-vpc",
		VPCCRN:         "crn:v1:...:vpc:r018-vpc",
		RecordedAt:     time.Now().UTC().Truncate(time.Second),
	}
	if err := WriteTGWOutputs("ws", in); err != nil {
		t.Fatal(err)
	}

	got, err := ReadTGWOutputs("ws")
	if err != nil {
		t.Fatalf("ReadTGWOutputs: %v", err)
	}
	if got.GatewayID != in.GatewayID || got.GatewayName != in.GatewayName ||
		got.ConnectionID != in.ConnectionID || got.VPCCRN != in.VPCCRN {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", got, in)
	}

	// Delete is idempotent — used on `tgw disconnect`, and a double-run must not error.
	if err := DeleteTGWOutputs("ws"); err != nil {
		t.Fatalf("DeleteTGWOutputs: %v", err)
	}
	if err := DeleteTGWOutputs("ws"); err != nil {
		t.Fatalf("DeleteTGWOutputs (second) must be a no-op: %v", err)
	}
	if _, err := ReadTGWOutputs("ws"); !errors.Is(err, ErrTGWOutputsMissing) {
		t.Fatalf("after delete = %v, want ErrTGWOutputsMissing", err)
	}
}
