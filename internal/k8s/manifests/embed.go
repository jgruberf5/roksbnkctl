// Package manifests exposes the embedded k8s manifest directories as an fs.FS.
// Phases use this FS to read manifest files without requiring them to be on disk
// at runtime — the manifests are baked into the binary at compile time.
//
// Layout:
//
//	cert-manager/          — cert-manager v1.16.1 static install YAML (upstream verbatim)
//	multus/                — Multus CNI v4.2.4 daemonset YAML (upstream verbatim, slice 7+)
//	shared/                — BNK cert chain template (applied to every cluster/pattern)
//	host-device/           — variant manifests for host-device pattern (slice 6+ content)
//	sriov-external/        — variant manifests for sriov-external pattern (vfio/DPDK dataplane)
//	nvidia-device-plugin/  — NVIDIA k8s-device-plugin v0.17.1 DaemonSet (GPU node groups)
//	envoy-gateway/         — EnvoyProxy CRD (v1.7.1) + pre-seed manifests for NodePort shootout
package manifests

import "embed"

// FS is the embedded manifest filesystem.
//
//go:embed all:cert-manager all:multus all:shared all:host-device all:sriov-external all:nvidia-device-plugin all:envoy-gateway
var FS embed.FS
