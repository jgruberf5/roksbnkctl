// Package flp holds the F5 License Proxy install's chart fix-ups.
//
// The f5-license-proxy chart cannot be installed onto ROKS as shipped, so the
// helm release runs it through a POST-RENDERER: helm hands the rendered
// manifests to a binary on stdin and installs whatever comes back on stdout.
// This package is that transform, and `roksbnkctl flp postrender` is the binary
// helm calls — roksbnkctl post-renders its own chart.
//
// It used to be a generated python script. That made python3 an undeclared
// runtime dependency of the FLP phase: fine on most laptops, fatal in the
// tools-runner container (no python at all), where `flp up` died with
// "error while running post render". Keeping the transform in the binary that is
// definitionally present removes the dependency instead of documenting it.
package flp

import (
	"bytes"
	"regexp"
	"strings"
)

// The chart's manifest stream is split on this exactly the way helm emits it.
const docSep = "\n---\n"

var (
	reKindPV      = regexp.MustCompile(`(?m)^[ \t]*kind:[ \t]*PersistentVolume[ \t]*$`)
	reKindPVC     = regexp.MustCompile(`(?m)^[ \t]*kind:[ \t]*PersistentVolumeClaim[ \t]*$`)
	reKindService = regexp.MustCompile(`(?m)^[ \t]*kind:[ \t]*Service[ \t]*$`)

	reStorageClass = regexp.MustCompile(`(storageClassName:[ \t]*).*`)
	// The chart binds its PVCs to its own hostPath PVs by label. Once those PVs
	// are dropped the selector matches nothing, and a PVC with an unsatisfiable
	// selector stays Pending forever — the dynamic provisioner is never asked.
	reVolumeSelector = regexp.MustCompile(`\n[ \t]*selector:[ \t]*\n[ \t]*matchLabels:[ \t]*\n[ \t]*volumeType:[^\n]*`)
	reTrafficLocal   = regexp.MustCompile(`(externalTrafficPolicy:[ \t]*)Local`)
)

// Options controls the transform.
type Options struct {
	// StorageClass the PVCs are repointed at. The chart ships hostPath
	// PersistentVolumes — a single-node/dev model that cannot bind on a
	// multi-node, non-root ROKS cluster — so those PVs are dropped and the PVCs
	// are provisioned dynamically instead.
	StorageClass string

	// NodePortCluster rewrites the Service's externalTrafficPolicy from Local to
	// Cluster. The chart hardcodes Local and runs ONE replica, so only the node
	// currently hosting the pod answers on the NodePort — and which node that is
	// changes whenever the pod reschedules. Cluster makes every node forward to
	// the pod, so any worker IP is a valid endpoint, which is what a licensing
	// client in another cluster needs. (Local exists to preserve the client
	// source IP; licensing does not care about it.)
	NodePortCluster bool
}

// Render applies the transform to a rendered helm manifest stream.
func Render(in []byte, o Options) []byte {
	docs := strings.Split(string(in), docSep)
	out := make([]string, 0, len(docs))

	for _, d := range docs {
		// Drop the chart's hostPath PVs. Guard on hostPath so a PV the operator
		// legitimately supplied some other way is left alone.
		if reKindPV.MatchString(d) && strings.Contains(d, "hostPath:") {
			continue
		}
		if reKindPVC.MatchString(d) {
			d = reStorageClass.ReplaceAllString(d, "${1}"+o.StorageClass)
			d = reVolumeSelector.ReplaceAllString(d, "")
		}
		if o.NodePortCluster && reKindService.MatchString(d) {
			d = reTrafficLocal.ReplaceAllString(d, "${1}Cluster")
		}
		out = append(out, d)
	}

	var buf bytes.Buffer
	buf.WriteString(strings.Join(out, docSep))
	return buf.Bytes()
}
