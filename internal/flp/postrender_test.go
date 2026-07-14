package flp

import (
	"strings"
	"testing"
)

// A cut-down but faithful slice of what `helm template f5-license-proxy` emits:
// a hostPath PV, a PVC bound to it by label selector, and the NodePort Service
// with externalTrafficPolicy hardcoded to Local.
const chartStream = `apiVersion: v1
kind: PersistentVolume
metadata:
  name: postgresql-data-pv
  labels:
    volumeType: postgresql-data
spec:
  capacity:
    storage: 8Gi
  hostPath:
    path: /mnt/data/postgresql
  storageClassName: manual
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: postgresql-data-pvc
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: manual
  selector:
    matchLabels:
      volumeType: postgresql-data
  resources:
    requests:
      storage: 8Gi
---
apiVersion: v1
kind: Service
metadata:
  name: f5-license-proxy
spec:
  type: NodePort
  externalTrafficPolicy: Local
  ports:
    - port: 8443
      nodePort: 30001
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: f5-license-proxy
spec:
  replicas: 1
`

func TestRender_DropsHostPathPVAndRepointsPVC(t *testing.T) {
	got := string(Render([]byte(chartStream), Options{StorageClass: "ibmc-vpc-block-metro-10iops-tier"}))

	// The hostPath PV cannot bind on a multi-node, non-root ROKS cluster.
	if strings.Contains(got, "hostPath:") {
		t.Error("hostPath PV survived; it cannot bind on ROKS")
	}
	if strings.Contains(got, "kind: PersistentVolume\n") {
		t.Error("the chart's PersistentVolume should have been dropped")
	}

	// The PVC must stay, repointed at a dynamic class.
	if !strings.Contains(got, "kind: PersistentVolumeClaim") {
		t.Fatal("the PVC must survive — it is what gets provisioned")
	}
	if !strings.Contains(got, "storageClassName: ibmc-vpc-block-metro-10iops-tier") {
		t.Error("PVC storageClassName was not repointed at the dynamic class")
	}
	if strings.Contains(got, "storageClassName: manual") {
		t.Error("the chart's `manual` storageClassName survived")
	}

	// The label selector bound the PVC to the hostPath PV we just deleted. Left
	// in place it matches nothing, the dynamic provisioner is never asked, and
	// the PVC sits Pending forever.
	if strings.Contains(got, "volumeType: postgresql-data") {
		t.Error("the PVC's volumeType selector survived — it would stay Pending forever")
	}

	// Unrelated documents must pass through untouched.
	if !strings.Contains(got, "kind: Deployment") {
		t.Error("the Deployment was dropped")
	}
}

// externalTrafficPolicy is only rewritten when the proxy has to be reachable from
// OUTSIDE its cluster. Left as Local (with replicas: 1) only the node currently
// hosting the pod answers on the NodePort — and that node moves.
func TestRender_ExternalTrafficPolicy(t *testing.T) {
	t.Run("untouched by default", func(t *testing.T) {
		got := string(Render([]byte(chartStream), Options{StorageClass: "sc"}))
		if !strings.Contains(got, "externalTrafficPolicy: Local") {
			t.Error("Local must be preserved when the proxy is not exposed externally")
		}
	})

	t.Run("Local becomes Cluster when exposed", func(t *testing.T) {
		got := string(Render([]byte(chartStream), Options{StorageClass: "sc", NodePortCluster: true}))
		if !strings.Contains(got, "externalTrafficPolicy: Cluster") {
			t.Error("externalTrafficPolicy was not flipped to Cluster")
		}
		if strings.Contains(got, "externalTrafficPolicy: Local") {
			t.Error("Local survived — only the pod's own node would answer on the NodePort")
		}
	})
}

// The stream must remain a valid multi-document stream: same separator, and every
// surviving document still present exactly once.
func TestRender_PreservesStreamShape(t *testing.T) {
	got := string(Render([]byte(chartStream), Options{StorageClass: "sc", NodePortCluster: true}))

	// 4 docs in, 1 dropped → 3 out → 2 separators.
	if n := strings.Count(got, "\n---\n"); n != 2 {
		t.Errorf("document separators = %d, want 2 (4 docs in, the hostPath PV dropped)", n)
	}
	for _, kind := range []string{"kind: PersistentVolumeClaim", "kind: Service", "kind: Deployment"} {
		if n := strings.Count(got, kind); n != 1 {
			t.Errorf("%q appears %d times, want 1", kind, n)
		}
	}
}

// An empty stream (helm can hand one over on an upgrade that renders nothing)
// must not panic or emit junk.
func TestRender_Empty(t *testing.T) {
	if got := Render(nil, Options{StorageClass: "sc"}); len(got) != 0 {
		t.Errorf("empty input produced %q, want empty", got)
	}
}
