package config

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestOverrideFromEnv(t *testing.T) {
	// Isolate from the ambient environment: clear every mapped var, then set
	// only what each sub-case exercises.
	clearAll := func(t *testing.T) {
		for _, e := range []string{
			"IBMCLOUD_API_KEY", "ROKSBNKCTL_API_KEY_B64", "ROKSBNKCTL_PREFIX",
			"ROKSBNKCTL_REGION", "ROKSBNKCTL_RESOURCE_GROUP", "ROKSBNKCTL_TESTING_SSH_KEY_NAME",
			"ROKSBNKCTL_GENERIC_PASSWORD", "ROKSBNKCTL_LICENSE_MODE", "ROKSBNKCTL_FLP_NAMESPACE",
			"ROKSBNKCTL_REGISTRY_TARGET", "ROKSBNKCTL_GENERIC_HOST",
			"ROKSBNKCTL_GENERIC_REPO_PREFIX", "ROKSBNKCTL_GENERIC_USERNAME",
			"ROKSBNKCTL_FLP_EXTERNAL_URL", "ROKSBNKCTL_FLP_ROOT_CA_B64",
			"ROKSBNKCTL_BNKFORGE_CA_B64",
		} {
			t.Setenv(e, "")
		}
	}

	t.Run("raw api key is base64-encoded, label hides the value", func(t *testing.T) {
		clearAll(t)
		t.Setenv("IBMCLOUD_API_KEY", "raw-secret")
		ws := &Workspace{}
		applied := OverrideFromEnv(ws)
		want := base64.StdEncoding.EncodeToString([]byte("raw-secret"))
		if ws.IBMCloud.APIKeyB64 != want {
			t.Fatalf("api_key_b64 = %q, want %q", ws.IBMCloud.APIKeyB64, want)
		}
		for _, a := range applied {
			if strings.Contains(a, "raw-secret") || strings.Contains(a, want) {
				t.Errorf("override label leaks the secret: %q", a)
			}
		}
	})

	t.Run("pre-encoded ROKSBNKCTL_API_KEY_B64 wins over raw", func(t *testing.T) {
		clearAll(t)
		t.Setenv("IBMCLOUD_API_KEY", "raw")
		t.Setenv("ROKSBNKCTL_API_KEY_B64", "PREENC==")
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if ws.IBMCloud.APIKeyB64 != "PREENC==" {
			t.Fatalf("api_key_b64 = %q, want PREENC==", ws.IBMCloud.APIKeyB64)
		}
	})

	t.Run("scalars + ssh key (nil Resources gets allocated)", func(t *testing.T) {
		clearAll(t)
		t.Setenv("ROKSBNKCTL_PREFIX", "p1")
		t.Setenv("ROKSBNKCTL_REGION", "eu-de")
		t.Setenv("ROKSBNKCTL_RESOURCE_GROUP", "rg1")
		t.Setenv("ROKSBNKCTL_TESTING_SSH_KEY_NAME", "k1")
		ws := &Workspace{} // Resources is nil
		OverrideFromEnv(ws)
		if ws.Prefix != "p1" || ws.IBMCloud.Region != "eu-de" || ws.IBMCloud.ResourceGroup != "rg1" {
			t.Fatalf("scalars not applied: %+v", ws)
		}
		if ws.Resources == nil || ws.Resources.TestingSSHKeyName != "k1" {
			t.Fatalf("ssh key not applied: %+v", ws.Resources)
		}
	})

	t.Run("generic registry password is base64-encoded into a nil Registry", func(t *testing.T) {
		clearAll(t)
		t.Setenv("ROKSBNKCTL_GENERIC_PASSWORD", "art-token")
		ws := &Workspace{} // Registry is nil
		OverrideFromEnv(ws)
		want := base64.StdEncoding.EncodeToString([]byte("art-token"))
		if ws.Registry == nil || ws.Registry.GenericPasswordB64 != want {
			t.Fatalf("generic_password_b64 not applied: %+v", ws.Registry)
		}
	})

	t.Run("bnkforge CA: line-wrapped base64 is normalized and applied", func(t *testing.T) {
		clearAll(t)
		// GNU `base64` wraps at 76 columns, so $(base64 ca.pem) arrives with
		// embedded newlines — the value must be accepted and stored single-line.
		wrapped := wrap76(base64.StdEncoding.EncodeToString(testCAPEM(t)))
		if !strings.Contains(wrapped, "\n") {
			t.Fatal("test cert too small to exercise wrapping")
		}
		t.Setenv("ROKSBNKCTL_BNKFORGE_CA_B64", wrapped)
		ws := &Workspace{} // BNKForge is nil
		applied := OverrideFromEnv(ws)
		if ws.BNKForge == nil || ws.BNKForge.CAB64 == "" {
			t.Fatal("bnkforge.ca_b64 not applied")
		}
		if strings.ContainsAny(ws.BNKForge.CAB64, " \n\t") {
			t.Errorf("stored value still contains whitespace: %q", ws.BNKForge.CAB64)
		}
		if !strings.Contains(strings.Join(applied, ","), "bnkforge.ca_b64") {
			t.Fatalf("override not reported: %v", applied)
		}
	})

	t.Run("bnkforge CA: a non-certificate value is rejected at seed time", func(t *testing.T) {
		clearAll(t)
		t.Setenv("ROKSBNKCTL_BNKFORGE_CA_B64", base64.StdEncoding.EncodeToString([]byte("not a certificate")))
		ws := &Workspace{}
		applied := OverrideFromEnv(ws)
		if ws.BNKForge != nil {
			t.Fatalf("an invalid CA must not be stored: %+v", ws.BNKForge)
		}
		if strings.Contains(strings.Join(applied, ","), "bnkforge.ca_b64") {
			t.Fatalf("a rejected override must not be reported as applied: %v", applied)
		}
	})

	t.Run("license mode f5licenseproxy seeds an flp block", func(t *testing.T) {
		clearAll(t)
		t.Setenv("ROKSBNKCTL_LICENSE_MODE", "f5licenseproxy")
		t.Setenv("ROKSBNKCTL_FLP_NAMESPACE", "flp-ns")
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if ws.BNK.LicenseMode != "f5licenseproxy" {
			t.Fatalf("license_mode = %q, want f5licenseproxy", ws.BNK.LicenseMode)
		}
		if ws.BNK.FLP == nil || ws.BNK.FLP.Namespace != "flp-ns" {
			t.Fatalf("flp block not seeded: %+v", ws.BNK.FLP)
		}
	})

	t.Run("no license-mode env leaves BNK untouched (JWT default)", func(t *testing.T) {
		clearAll(t)
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if ws.BNK.LicenseMode != "" || ws.BNK.FLP != nil {
			t.Fatalf("absent env must not touch licensing: mode=%q flp=%+v", ws.BNK.LicenseMode, ws.BNK.FLP)
		}
	})

	t.Run("env wins over a seeded value", func(t *testing.T) {
		clearAll(t)
		t.Setenv("ROKSBNKCTL_PREFIX", "fromenv")
		ws := &Workspace{Prefix: "fromfile"}
		OverrideFromEnv(ws)
		if ws.Prefix != "fromenv" {
			t.Fatalf("prefix = %q, want fromenv (env must win)", ws.Prefix)
		}
	})

	t.Run("unset vars are inert", func(t *testing.T) {
		clearAll(t)
		ws := &Workspace{Prefix: "keep"}
		applied := OverrideFromEnv(ws)
		if len(applied) != 0 {
			t.Fatalf("applied = %v, want none", applied)
		}
		if ws.Prefix != "keep" {
			t.Fatalf("prefix clobbered to %q", ws.Prefix)
		}
	})
}

// TestOverrideFromEnv_CIPipelineSurface covers the variables a CI pipeline needs
// to run the shared-licensing topology with NO config file to template: where the
// registry is, and the proxy handoff from the job that owns the proxy.
func TestOverrideFromEnv_CIPipelineSurface(t *testing.T) {
	t.Run("registry target comes entirely from env", func(t *testing.T) {
		t.Setenv("ROKSBNKCTL_REGISTRY_TARGET", "generic")
		t.Setenv("ROKSBNKCTL_GENERIC_HOST", "harbor.example.com")
		t.Setenv("ROKSBNKCTL_GENERIC_REPO_PREFIX", "bnk-mirror")
		t.Setenv("ROKSBNKCTL_GENERIC_USERNAME", "admin")
		t.Setenv("ROKSBNKCTL_GENERIC_PASSWORD", "s3cret")

		// A workspace with NO registry block at all — the normal CI case.
		ws := &Workspace{}
		applied := OverrideFromEnv(ws)

		if ws.Registry == nil {
			t.Fatal("registry block was not created")
		}
		if ws.Registry.Target != "generic" {
			t.Errorf("target = %q, want generic", ws.Registry.Target)
		}
		if ws.Registry.GenericHost != "harbor.example.com" {
			t.Errorf("generic_host = %q", ws.Registry.GenericHost)
		}
		if ws.Registry.GenericRepoPrefix != "bnk-mirror" {
			t.Errorf("generic_repo_prefix = %q", ws.Registry.GenericRepoPrefix)
		}
		if ws.Registry.GenericUsername != "admin" {
			t.Errorf("generic_username = %q", ws.Registry.GenericUsername)
		}
		want := base64.StdEncoding.EncodeToString([]byte("s3cret"))
		if ws.Registry.GenericPasswordB64 != want {
			t.Errorf("password not base64-encoded: %q", ws.Registry.GenericPasswordB64)
		}
		for _, a := range applied {
			if strings.Contains(a, "s3cret") || strings.Contains(a, want) {
				t.Errorf("override label leaks the registry password: %q", a)
			}
		}
	})

	t.Run("foreign-proxy handoff on a config that never mentioned the FLP", func(t *testing.T) {
		// The nil-pointer case: bnk.flp and bnk.flp.external are both pointers, so
		// a pipeline setting ONLY these two must not panic.
		t.Setenv("ROKSBNKCTL_LICENSE_MODE", "f5licenseproxy")
		t.Setenv("ROKSBNKCTL_FLP_EXTERNAL_URL", "https://10.242.0.9:30001")
		t.Setenv("ROKSBNKCTL_FLP_ROOT_CA_B64", "TFMwdExTMUNSVWRK")

		ws := &Workspace{}
		OverrideFromEnv(ws)

		if ws.BNK.LicenseMode != "f5licenseproxy" {
			t.Fatalf("license_mode = %q", ws.BNK.LicenseMode)
		}
		if ws.BNK.FLP == nil || ws.BNK.FLP.External == nil {
			t.Fatal("bnk.flp.external was not created")
		}
		if got := ws.BNK.FLP.External.URL; got != "https://10.242.0.9:30001" {
			t.Errorf("external.url = %q", got)
		}
		// Already base64 (that is how `flp output flp_root_ca` emits it) — must be
		// stored VERBATIM, not re-encoded, or the CA the CWC gets is garbage.
		if got := ws.BNK.FLP.External.RootCAB64; got != "TFMwdExTMUNSVWRK" {
			t.Errorf("root_ca_b64 = %q, want the value verbatim (double-encoding it corrupts the CA)", got)
		}
	})

	t.Run("the handoff vars work with no license_mode set", func(t *testing.T) {
		// Order-independence: flpExternal must create the blocks itself, not rely
		// on ROKSBNKCTL_LICENSE_MODE having seeded them first.
		t.Setenv("ROKSBNKCTL_FLP_EXTERNAL_URL", "https://10.0.0.1:30001")
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if ws.BNK.FLP == nil || ws.BNK.FLP.External == nil {
			t.Fatal("bnk.flp.external must be created without license_mode")
		}
	})
}

// TestOverrideFromEnv_ClusterTopology covers the fields an argv+env runner needs
// to build a cluster it cannot answer prompts for. public_gateway is the one
// that decides connected vs disconnected, so its tri-state matters: unset must
// stay nil (inherit the terraform default) rather than collapse to false.
func TestOverrideFromEnv_ClusterTopology(t *testing.T) {
	clear := func(t *testing.T) {
		for _, e := range []string{
			"ROKSBNKCTL_CLUSTER_PUBLIC_GATEWAY",
			"ROKSBNKCTL_TGW_JUMPHOST_CREATE",
			"ROKSBNKCTL_CLIENT_VPC_CREATE",
		} {
			t.Setenv(e, "")
		}
	}

	t.Run("public_gateway unset stays nil", func(t *testing.T) {
		clear(t)
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if ws.Cluster.PublicGateway != nil {
			t.Fatalf("unset must leave nil so terraform's default applies, got %v", *ws.Cluster.PublicGateway)
		}
	})

	t.Run("public_gateway=false makes a disconnected cluster", func(t *testing.T) {
		clear(t)
		t.Setenv("ROKSBNKCTL_CLUSTER_PUBLIC_GATEWAY", "false")
		ws := &Workspace{}
		applied := OverrideFromEnv(ws)
		if ws.Cluster.PublicGateway == nil || *ws.Cluster.PublicGateway {
			t.Fatal("expected an explicit false")
		}
		if !strings.Contains(strings.Join(applied, ","), "cluster.public_gateway") {
			t.Fatalf("override not reported: %v", applied)
		}
	})

	t.Run("public_gateway=true is explicit, not merely nil", func(t *testing.T) {
		clear(t)
		t.Setenv("ROKSBNKCTL_CLUSTER_PUBLIC_GATEWAY", "true")
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if ws.Cluster.PublicGateway == nil || !*ws.Cluster.PublicGateway {
			t.Fatal("expected an explicit true")
		}
	})

	t.Run("garbage is ignored rather than guessed at", func(t *testing.T) {
		clear(t)
		t.Setenv("ROKSBNKCTL_CLUSTER_PUBLIC_GATEWAY", "yes-please")
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if ws.Cluster.PublicGateway != nil {
			t.Fatal("unparseable value must not silently pick a topology")
		}
	})

	t.Run("testing client can be opted into from env", func(t *testing.T) {
		clear(t)
		t.Setenv("ROKSBNKCTL_TGW_JUMPHOST_CREATE", "true")
		t.Setenv("ROKSBNKCTL_CLIENT_VPC_CREATE", "true")
		ws := &Workspace{Resources: DefaultResources()}
		OverrideFromEnv(ws)
		if !ws.Resources.TGWJumphost.Create || !ws.Resources.ClientVPC.Create {
			t.Fatal("expected both toggles on")
		}
	})

	t.Run("toggles reach a nil Resources block", func(t *testing.T) {
		clear(t)
		t.Setenv("ROKSBNKCTL_TGW_JUMPHOST_CREATE", "true")
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if ws.Resources == nil || !ws.Resources.TGWJumphost.Create {
			t.Fatal("must allocate Resources rather than panic or no-op")
		}
	})
}

// TestDefaultResourcesMatchesInterview pins the default that regressed: the
// interview asks "Add a testing client?" and defaults to no, so the
// non-interactive path must not build one unasked. The client VPC costs a
// Transit Gateway connection, which is a quota'd resource.
func TestDefaultResourcesMatchesInterview(t *testing.T) {
	r := DefaultResources()
	if r.TGWJumphost.Create {
		t.Error("tgw_jumphost must default off, as the interview does")
	}
	if r.ClientVPC.Create {
		t.Error("client_vpc must default off, as the interview does")
	}
	// The toggles this function exists to protect stay on.
	for name, on := range map[string]bool{
		"transit_gateway": r.TransitGateway.Create, "registry_cos": r.RegistryCOS.Create,
		"cert_manager": r.CertManager.Create, "bnk": r.BNK.Create,
		"cluster_vpc": r.ClusterVPC.Create,
	} {
		if !on {
			t.Errorf("%s must stay on: zero-value toggles silently disable the deploy", name)
		}
	}
}

// The jumphost lives IN a client VPC, so the env surface must be able to express
// the interview's "use an existing one" branch — not just "create a new one".
func TestOverrideFromEnv_ClientVPCExisting(t *testing.T) {
	t.Setenv("ROKSBNKCTL_TGW_JUMPHOST_CREATE", "true")
	t.Setenv("ROKSBNKCTL_CLIENT_VPC_NAME", "shared-client-vpc")

	var ws Workspace
	ws.Resources = DefaultResources()
	applied := OverrideFromEnv(&ws)

	if !ws.Resources.TGWJumphost.Create {
		t.Error("tgw_jumphost.create should be true")
	}
	if ws.Resources.ClientVPC.Create {
		t.Error("client_vpc.create should stay false — we are adopting one")
	}
	if got := ws.Resources.ClientVPC.Existing; got != "shared-client-vpc" {
		t.Errorf("client_vpc.existing = %q, want %q", got, "shared-client-vpc")
	}
	var found bool
	for _, a := range applied {
		if strings.Contains(a, "ROKSBNKCTL_CLIENT_VPC_NAME") {
			found = true
		}
	}
	if !found {
		t.Errorf("the applied list should name ROKSBNKCTL_CLIENT_VPC_NAME, got %v", applied)
	}
}

// Reaching a nil Resources block must not panic.
func TestOverrideFromEnv_ClientVPCNameNilResources(t *testing.T) {
	t.Setenv("ROKSBNKCTL_CLIENT_VPC_NAME", "adopted")
	var ws Workspace
	OverrideFromEnv(&ws)
	if ws.Resources == nil || ws.Resources.ClientVPC.Existing != "adopted" {
		t.Fatalf("nil Resources should be created and populated, got %+v", ws.Resources)
	}
}

// The reachability tunables need an env surface for the same reason everything else
// here does: a CI runner building a workspace from argv alone has no config.yaml to
// edit, and these are exactly the values a pipeline raises when its fabric programs
// routes slowly (issue #57).
func TestOverrideFromEnv_Reachability(t *testing.T) {
	t.Setenv("ROKSBNKCTL_REACHABILITY_RETRY_SECONDS", "600")
	t.Setenv("ROKSBNKCTL_REACHABILITY_TIMEOUT_SECONDS", "900")

	ws := &Workspace{}
	OverrideFromEnv(ws)

	if ws.BNK.Preflight == nil {
		t.Fatal("the preflight block must be created when only env names it")
	}
	if got := ws.ReachabilityRetrySeconds(); got != 600 {
		t.Errorf("retry = %d, want 600", got)
	}
	if got, want := ws.ReachabilityTimeout(), 900*time.Second; got != want {
		t.Errorf("timeout = %s, want %s", got, want)
	}
}

// 0 means one-shot and is a legitimate choice for a static environment. It must be
// distinguishable from "unset", which is why the fields are pointers — a plain int
// would silently reinstate the 180s default.
func TestOverrideFromEnv_ReachabilityZeroIsNotUnset(t *testing.T) {
	t.Setenv("ROKSBNKCTL_REACHABILITY_RETRY_SECONDS", "0")
	ws := &Workspace{}
	OverrideFromEnv(ws)
	if got := ws.ReachabilityRetrySeconds(); got != 0 {
		t.Errorf("an explicit 0 must survive the env round-trip, got %d", got)
	}
}

// Garbage must leave the default in place rather than land as a 0 budget — a typo
// silently turning the retry off is the failure mode this guards.
func TestOverrideFromEnv_ReachabilityRejectsGarbage(t *testing.T) {
	t.Setenv("ROKSBNKCTL_REACHABILITY_RETRY_SECONDS", "later")
	t.Setenv("ROKSBNKCTL_REACHABILITY_TIMEOUT_SECONDS", "-1")
	ws := &Workspace{}
	OverrideFromEnv(ws)
	if got := ws.ReachabilityRetrySeconds(); got != DefaultReachabilityRetrySeconds {
		t.Errorf("an unparseable value must leave the default, got %d", got)
	}
	if got, want := ws.ReachabilityTimeout(), time.Duration(DefaultReachabilityTimeoutSeconds)*time.Second; got != want {
		t.Errorf("a negative timeout must leave the default, got %s", got)
	}
}
