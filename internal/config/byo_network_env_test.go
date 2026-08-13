package config

import "testing"

// #64: these shipped in v1.43.0 with a config surface and no env override, which
// made them unreachable from BNK Forge — every module runs
// `init --override-from-env --non-interactive` and there is no config.yaml to
// edit, so a YAML-only field cannot be used by a blueprint at all.

func TestExistingSubnetIDsFromEnv(t *testing.T) {
	t.Setenv("ROKSBNKCTL_EXISTING_SUBNET_IDS", "0717-aaa, 0717-bbb ,0717-ccc")
	ws := &Workspace{}
	OverrideFromEnv(ws)

	got := ws.Cluster.ExistingSubnetIDs
	want := []string{"0717-aaa", "0717-bbb", "0717-ccc"}
	if len(got) != len(want) {
		t.Fatalf("ids = %#v, want %#v", got, want)
	}
	for i := range want {
		// Zone ORDER is load-bearing — each subnet's zone is read from the subnet,
		// so a reordered list silently places the cluster differently.
		if got[i] != want[i] {
			t.Errorf("id %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// A trailing comma must not render an empty subnet id into the terraform list.
func TestExistingSubnetIDsDropsBlanks(t *testing.T) {
	t.Setenv("ROKSBNKCTL_EXISTING_SUBNET_IDS", "0717-aaa,,  ,0717-bbb,")
	ws := &Workspace{}
	OverrideFromEnv(ws)
	if len(ws.Cluster.ExistingSubnetIDs) != 2 {
		t.Errorf("ids = %#v, want 2 entries", ws.Cluster.ExistingSubnetIDs)
	}
}

func TestFLPVSIBYONetworkFromEnv(t *testing.T) {
	t.Setenv("ROKSBNKCTL_FLP_VSI_CREATE_VPC", "true")
	t.Setenv("ROKSBNKCTL_FLP_VSI_VPC_NAME", "flp-own-vpc")
	t.Setenv("ROKSBNKCTL_FLP_VSI_SUBNET_CIDR", "10.250.0.0/24")
	ws := &Workspace{}
	applied := OverrideFromEnv(ws)

	vsi := ws.BNK.FLP.VSI
	if vsi == nil {
		t.Fatal("the vsi block was not created")
	}
	if !vsi.CreateVPC {
		t.Error("create_vpc not applied")
	}
	if vsi.VPCName != "flp-own-vpc" {
		t.Errorf("vpc_name = %q", vsi.VPCName)
	}
	if vsi.SubnetCIDR != "10.250.0.0/24" {
		t.Errorf("subnet_cidr = %q", vsi.SubnetCIDR)
	}
	if len(applied) < 3 {
		t.Errorf("all three must be reported as applied: %v", applied)
	}
}

// An unparseable bool must leave create_vpc FALSE — false is the
// existing-workspace behaviour, and pinning true would build a VPC nobody asked
// for out of a typo.
func TestFLPVSICreateVPCIgnoresGarbage(t *testing.T) {
	for _, bad := range []string{"yes please", "1.5", "maybe"} {
		t.Setenv("ROKSBNKCTL_FLP_VSI_CREATE_VPC", bad)
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if ws.BNK.FLP != nil && ws.BNK.FLP.VSI != nil && ws.BNK.FLP.VSI.CreateVPC {
			t.Errorf("%q was accepted as true", bad)
		}
	}
}

// Setting one FLP VSI field must not wipe the others already in config.yaml.
func TestFLPVSIOverridePreservesSiblings(t *testing.T) {
	t.Setenv("ROKSBNKCTL_FLP_VSI_SUBNET_CIDR", "10.251.0.0/24")
	ws := &Workspace{BNK: BNKCfg{FLP: &BNKFLPCfg{VSI: &BNKFLPVSICfg{
		Profile: "bx2-8x32",
		Zone:    "us-east-1",
	}}}}
	OverrideFromEnv(ws)

	vsi := ws.BNK.FLP.VSI
	if vsi.SubnetCIDR != "10.251.0.0/24" {
		t.Errorf("subnet_cidr = %q", vsi.SubnetCIDR)
	}
	if vsi.Profile != "bx2-8x32" || vsi.Zone != "us-east-1" {
		t.Errorf("siblings clobbered: profile=%q zone=%q", vsi.Profile, vsi.Zone)
	}
}
