package k8s

import "testing"

// The real shape of an IBM ROKS kubeconfig, and the reason context selection
// is credential-aware. Note BOTH traps:
//
//   - the "flpair/<id>" context NAMES the cluster but has an EMPTY user — it
//     authenticates as system:anonymous and every request is forbidden;
//   - the context that actually carries the token points at a cluster entry
//     named after the HOST, which does not contain the cluster id at all.
//
// A name-only match picks the decoy. A server-only match is also wrong across
// files, because every cluster in a region shares the host and differs only by
// port (see the two-cluster case below).
const roksKubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://c100-e.eu-gb.containers.cloud.ibm.com:32394
  name: flpair/d9am7idl05o9f4dqni4g
- cluster:
    server: https://c100-e.eu-gb.containers.cloud.ibm.com:32394
  name: c100-e-eu-gb-containers-cloud-ibm-com:32394
contexts:
- context:
    cluster: flpair/d9am7idl05o9f4dqni4g
    user: ""
  name: flpair/d9am7idl05o9f4dqni4g
- context:
    cluster: c100-e-eu-gb-containers-cloud-ibm-com:32394
    user: IAM#j.gruber@f5.com/c100-e-eu-gb-containers-cloud-ibm-com:32394
  name: default/c100-e-eu-gb-containers-cloud-ibm-com:32394/IAM#j.gruber@f5.com
current-context: default/c100-e-eu-gb-containers-cloud-ibm-com:32394/IAM#j.gruber@f5.com
users:
- name: IAM#j.gruber@f5.com/c100-e-eu-gb-containers-cloud-ibm-com:32394
  user:
    token: t
`

// TestContextForCluster_SkipsCredentiallessDecoy: the id-named context must
// NOT win, or every `roksbnkctl k …` call is system:anonymous.
func TestContextForCluster_SkipsCredentiallessDecoy(t *testing.T) {
	got := ContextForCluster([]byte(roksKubeconfig), "d9am7idl05o9f4dqni4g")
	want := "default/c100-e-eu-gb-containers-cloud-ibm-com:32394/IAM#j.gruber@f5.com"
	if got != want {
		t.Errorf("ContextForCluster() = %q, want the credentialed context %q\n"+
			"(picking the id-named context authenticates as system:anonymous)", got, want)
	}
	if !CanAddressCluster([]byte(roksKubeconfig), "d9am7idl05o9f4dqni4g") {
		t.Error("CanAddressCluster() = false, want true")
	}
}

// Two clusters in one region: same host, different ports. `-w flpsvc` must
// select the flpsvc context even though current-context selects flpair.
const twoClusterKubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://c100-e.eu-gb.containers.cloud.ibm.com:32394
  name: flpair/d9am7idl05o9f4dqni4g
- cluster:
    server: https://c100-e.eu-gb.containers.cloud.ibm.com:31877
  name: flpsvc/d9al0dgl06ut5bv26mbg
contexts:
- context:
    cluster: flpair/d9am7idl05o9f4dqni4g
    user: iam
  name: flpair/d9am7idl05o9f4dqni4g
- context:
    cluster: flpsvc/d9al0dgl06ut5bv26mbg
    user: iam
  name: flpsvc/d9al0dgl06ut5bv26mbg
current-context: flpair/d9am7idl05o9f4dqni4g
users:
- name: iam
  user:
    token: t
`

func TestContextForCluster_IgnoresCurrentContext(t *testing.T) {
	data := []byte(twoClusterKubeconfig)

	got := ContextForCluster(data, "d9al0dgl06ut5bv26mbg")
	if want := "flpsvc/d9al0dgl06ut5bv26mbg"; got != want {
		t.Errorf("ContextForCluster(flpsvc) = %q, want %q — current-context (flpair) must not win", got, want)
	}
	// current-context IS the flpair one, and it is usable: prefer it verbatim.
	got = ContextForCluster(data, "d9am7idl05o9f4dqni4g")
	if want := "flpair/d9am7idl05o9f4dqni4g"; got != want {
		t.Errorf("ContextForCluster(flpair) = %q, want %q", got, want)
	}
}

func TestContextForCluster_Rejects(t *testing.T) {
	data := []byte(twoClusterKubeconfig)
	if got := ContextForCluster(data, "notacluster"); got != "" {
		t.Errorf("unknown cluster = %q, want \"\"", got)
	}
	if got := ContextForCluster(data, ""); got != "" {
		t.Errorf("empty id = %q, want \"\" — an empty id must never match", got)
	}
	if got := ContextForCluster([]byte("not a kubeconfig"), "d9am7idl05o9f4dqni4g"); got != "" {
		t.Errorf("garbage = %q, want \"\"", got)
	}
	if CanAddressCluster(data, "notacluster") {
		t.Error("CanAddressCluster(unknown) = true, want false")
	}
}
