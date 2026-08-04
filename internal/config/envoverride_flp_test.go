package config

import (
	"testing"
)

// clearFLPEnv isolates from the ambient environment: every variable the
// FLP-VSI + supply-chain maps read is cleared, so each sub-case sets only what
// it exercises.
func clearFLPEnv(t *testing.T) {
	t.Helper()
	for _, e := range []string{
		"ROKSBNKCTL_FLP_MODE",
		"ROKSBNKCTL_FLP_VSI_VPC", "ROKSBNKCTL_FLP_VSI_ZONE", "ROKSBNKCTL_FLP_VSI_PROFILE",
		"ROKSBNKCTL_FLP_VSI_SSH_KEY", "ROKSBNKCTL_FLP_VSI_REACH",
		"ROKSBNKCTL_FLP_VSI_BOOT_SIZE_GB", "ROKSBNKCTL_FLP_VSI_FLOATING_IP",
		"ROKSBNKCTL_FLP_VSI_MANAGEMENT_ALLOWED_CIDRS", "ROKSBNKCTL_FLP_VSI_LICENSING_ALLOWED_CIDRS",
		"ROKSBNKCTL_FLP_VSI_STATUS_IMAGE", "ROKSBNKCTL_FLP_VSI_STATUS_REGISTRY_HOST",
		"ROKSBNKCTL_FLP_VSI_STATUS_REGISTRY_CA_B64",
		"ROKSBNKCTL_MANIFEST_VERSION",
		"ROKSBNKCTL_FAR_AUTH_LOCAL_FILE", "ROKSBNKCTL_SUBSCRIPTION_JWT_LOCAL_FILE",
		"ROKSBNKCTL_FAR_AUTH_FILE", "ROKSBNKCTL_SUBSCRIPTION_JWT_FILE",
		"ROKSBNKCTL_COS_INSTANCE", "ROKSBNKCTL_COS_BUCKET", "ROKSBNKCTL_COS_REGION",
	} {
		t.Setenv(e, "")
	}
}

// TestOverrideFLPVSIFromEnv_TestedDemoShape is the load-bearing case: the exact
// standalone-FLP-VSI config the END-TO-END TESTED reference deployment writes by
// heredoc (scripts/demos/disconnected-cluster-cli-demo, Phase 3) must be
// reproducible from environment variables alone — that is the whole point of the
// map, since an argv-only container runner has no shell to write a YAML file.
func TestOverrideFLPVSIFromEnv_TestedDemoShape(t *testing.T) {
	clearFLPEnv(t)
	t.Setenv("ROKSBNKCTL_FLP_MODE", "vsi")
	t.Setenv("ROKSBNKCTL_FLP_VSI_VPC", "r006-abc-vpc")
	t.Setenv("ROKSBNKCTL_FLP_VSI_ZONE", "us-south-1")
	t.Setenv("ROKSBNKCTL_FLP_VSI_PROFILE", "bx2-4x16")
	t.Setenv("ROKSBNKCTL_FLP_VSI_SSH_KEY", "demo-key")
	t.Setenv("ROKSBNKCTL_FLP_VSI_FLOATING_IP", "true")
	t.Setenv("ROKSBNKCTL_FLP_VSI_STATUS_IMAGE", "10.0.0.4/bnk-status/flp-status:v1")
	t.Setenv("ROKSBNKCTL_FLP_VSI_STATUS_REGISTRY_HOST", "10.0.0.4")
	t.Setenv("ROKSBNKCTL_FLP_VSI_STATUS_REGISTRY_CA_B64", "Q0FCNjQ=")
	t.Setenv("ROKSBNKCTL_MANIFEST_VERSION", "2.3.0-3.2598.3-0.0.170")
	t.Setenv("ROKSBNKCTL_FAR_AUTH_LOCAL_FILE", "/work/far-auth.tgz")
	t.Setenv("ROKSBNKCTL_SUBSCRIPTION_JWT_LOCAL_FILE", "/work/subscription.jwt")

	ws := &Workspace{} // BNK.FLP is nil — the whole proxy comes from env
	OverrideFromEnv(ws)

	flp := ws.BNK.FLP
	if flp == nil || flp.VSI == nil {
		t.Fatal("flp / flp.vsi block was not created")
	}
	if flp.Mode != "vsi" {
		t.Errorf("flp.mode = %q, want vsi", flp.Mode)
	}
	v := flp.VSI
	if v.VPC != "r006-abc-vpc" || v.Zone != "us-south-1" || v.Profile != "bx2-4x16" || v.SSHKey != "demo-key" {
		t.Errorf("vsi identity fields not applied: %+v", v)
	}
	if v.FloatingIP == nil || !*v.FloatingIP {
		t.Errorf("vsi.floating_ip = %v, want true", v.FloatingIP)
	}
	if v.StatusImage != "10.0.0.4/bnk-status/flp-status:v1" ||
		v.StatusRegistryHost != "10.0.0.4" || v.StatusRegistryCAB64 != "Q0FCNjQ=" {
		t.Errorf("vsi status-image fields not applied: %+v", v)
	}
	if ws.BNK.ManifestVersion != "2.3.0-3.2598.3-0.0.170" {
		t.Errorf("bnk.manifest_version = %q", ws.BNK.ManifestVersion)
	}
	if ws.BNK.FarAuthLocalFile != "/work/far-auth.tgz" ||
		ws.BNK.SubscriptionJWTLocalFile != "/work/subscription.jwt" {
		t.Errorf("local-file supply chain not applied: %+v", ws.BNK)
	}
}

// The cluster-less appliance is gated on mode==vsi AND a non-empty vsi.vpc
// (orchestration.StandaloneFLPVSI). Assert the env map produces exactly the
// combination that turns it on — a partial config must NOT.
func TestOverrideFLPVSIFromEnv_StandaloneGate(t *testing.T) {
	standalone := func(ws *Workspace) bool {
		return ws.BNK.FLP != nil && ws.BNK.FLP.Mode == "vsi" &&
			ws.BNK.FLP.VSI != nil && ws.BNK.FLP.VSI.VPC != ""
	}

	t.Run("mode + vpc arms it", func(t *testing.T) {
		clearFLPEnv(t)
		t.Setenv("ROKSBNKCTL_FLP_MODE", "vsi")
		t.Setenv("ROKSBNKCTL_FLP_VSI_VPC", "r006-abc")
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if !standalone(ws) {
			t.Fatalf("standalone gate not armed: %+v", ws.BNK.FLP)
		}
	})

	t.Run("mode alone does not", func(t *testing.T) {
		clearFLPEnv(t)
		t.Setenv("ROKSBNKCTL_FLP_MODE", "vsi")
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if standalone(ws) {
			t.Fatal("mode without a vpc must not arm the cluster-less path")
		}
	})

	t.Run("vpc alone does not", func(t *testing.T) {
		clearFLPEnv(t)
		t.Setenv("ROKSBNKCTL_FLP_VSI_VPC", "r006-abc")
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if standalone(ws) {
			t.Fatal("a vpc without mode: vsi must not arm the cluster-less path")
		}
	})
}

func TestOverrideFLPVSIFromEnv_TypedFields(t *testing.T) {
	t.Run("boot size parses; garbage is ignored, not fatal", func(t *testing.T) {
		clearFLPEnv(t)
		t.Setenv("ROKSBNKCTL_FLP_VSI_BOOT_SIZE_GB", "250")
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if ws.BNK.FLP == nil || ws.BNK.FLP.VSI == nil || ws.BNK.FLP.VSI.BootSizeGB != 250 {
			t.Fatalf("boot_size_gb not applied: %+v", ws.BNK.FLP)
		}

		clearFLPEnv(t)
		t.Setenv("ROKSBNKCTL_FLP_VSI_BOOT_SIZE_GB", "not-a-number")
		ws2 := &Workspace{}
		OverrideFromEnv(ws2)
		if ws2.BNK.FLP != nil && ws2.BNK.FLP.VSI != nil && ws2.BNK.FLP.VSI.BootSizeGB != 0 {
			t.Fatalf("unparseable boot_size_gb must leave the field zero, got %d", ws2.BNK.FLP.VSI.BootSizeGB)
		}
	})

	// floating_ip is a *bool where nil means "the module default (true)". An
	// unparseable value must leave it nil rather than pinning false — which would
	// silently strip the operator floating IP the status UI is reached on.
	t.Run("floating_ip false is honored; garbage leaves it nil", func(t *testing.T) {
		clearFLPEnv(t)
		t.Setenv("ROKSBNKCTL_FLP_VSI_FLOATING_IP", "false")
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if ws.BNK.FLP == nil || ws.BNK.FLP.VSI == nil || ws.BNK.FLP.VSI.FloatingIP == nil {
			t.Fatal("floating_ip=false must be recorded")
		}
		if *ws.BNK.FLP.VSI.FloatingIP {
			t.Error("floating_ip = true, want false")
		}

		clearFLPEnv(t)
		t.Setenv("ROKSBNKCTL_FLP_VSI_FLOATING_IP", "yes-please")
		ws2 := &Workspace{}
		OverrideFromEnv(ws2)
		if ws2.BNK.FLP != nil && ws2.BNK.FLP.VSI != nil && ws2.BNK.FLP.VSI.FloatingIP != nil {
			t.Error("unparseable floating_ip must leave the pointer nil (module default)")
		}
	})

	t.Run("CIDR lists split on commas and drop empties", func(t *testing.T) {
		clearFLPEnv(t)
		t.Setenv("ROKSBNKCTL_FLP_VSI_MANAGEMENT_ALLOWED_CIDRS", "203.0.113.4/32")
		t.Setenv("ROKSBNKCTL_FLP_VSI_LICENSING_ALLOWED_CIDRS", "10.0.0.0/8, 172.16.0.0/12 ,,192.168.0.0/16,")
		ws := &Workspace{}
		OverrideFromEnv(ws)
		v := ws.BNK.FLP.VSI
		if len(v.ManagementAllowedCIDRs) != 1 || v.ManagementAllowedCIDRs[0] != "203.0.113.4/32" {
			t.Errorf("management_allowed_cidrs = %v", v.ManagementAllowedCIDRs)
		}
		want := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
		if len(v.LicensingAllowedCIDRs) != len(want) {
			t.Fatalf("licensing_allowed_cidrs = %v, want %v", v.LicensingAllowedCIDRs, want)
		}
		for i, w := range want {
			if v.LicensingAllowedCIDRs[i] != w {
				t.Errorf("licensing_allowed_cidrs[%d] = %q, want %q", i, v.LicensingAllowedCIDRs[i], w)
			}
		}
	})
}

// The COS coordinates had no env surface at all, so a runner that could not write
// a config.yaml was pinned to the built-in defaults — which were RENAMED in
// v1.22.0 (bnk-orchestration → bnk-supply-chain, bnk-schematics-resources →
// bnk-artifacts, trial.jwt → subscription.jwt). An account still on the old names
// had no way to say so.
func TestOverrideSupplyChainFromEnv_COS(t *testing.T) {
	clearFLPEnv(t)
	t.Setenv("ROKSBNKCTL_COS_INSTANCE", "bnk-orchestration")
	t.Setenv("ROKSBNKCTL_COS_BUCKET", "bnk-schematics-resources")
	t.Setenv("ROKSBNKCTL_COS_REGION", "eu-de")
	t.Setenv("ROKSBNKCTL_SUBSCRIPTION_JWT_FILE", "trial.jwt")
	t.Setenv("ROKSBNKCTL_FAR_AUTH_FILE", "f5-far-auth-key.tgz")

	ws := &Workspace{} // COS is nil
	OverrideFromEnv(ws)

	if ws.COS == nil {
		t.Fatal("cos block was not created")
	}
	if ws.COS.Instance != "bnk-orchestration" || ws.COS.Bucket != "bnk-schematics-resources" || ws.COS.Region != "eu-de" {
		t.Errorf("cos coordinates not applied: %+v", ws.COS)
	}
	if ws.BNK.SubscriptionJWTFile != "trial.jwt" || ws.BNK.FarAuthFile != "f5-far-auth-key.tgz" {
		t.Errorf("object names not applied: %+v", ws.BNK)
	}
}

// Env WINS over whatever the seed/interview produced — the same precedence the
// core map documents — and an unset variable must leave the existing value alone
// rather than blanking it.
func TestOverrideFLPVSIFromEnv_PrecedenceAndPreservation(t *testing.T) {
	clearFLPEnv(t)
	t.Setenv("ROKSBNKCTL_FLP_VSI_ZONE", "us-south-2")

	ws := &Workspace{}
	ws.BNK.FLP = &BNKFLPCfg{
		Mode: "vsi",
		VSI: &BNKFLPVSICfg{
			VPC:     "from-config-file",
			Zone:    "us-south-1",
			Profile: "bx2-8x32",
		},
	}
	OverrideFromEnv(ws)

	v := ws.BNK.FLP.VSI
	if v.Zone != "us-south-2" {
		t.Errorf("env must win: zone = %q, want us-south-2", v.Zone)
	}
	if v.VPC != "from-config-file" || v.Profile != "bx2-8x32" {
		t.Errorf("unset variables must preserve existing fields: %+v", v)
	}
}
