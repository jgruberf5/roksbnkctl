package phases

// Host-device pattern constants — architecture-level constants that are NOT
// operator knobs. These are baked into the manifests and persisted to state.env
// for observability by downstream phases (doctor, inspect) and Pass 3.
//
// Architect categorisation (slice-07 reviews/architect.md):
//   - ExternalIFName / InternalIFName: hardcoded into template (not cluster.yaml).
//   - ExternalNAD / InternalNAD: hardcoded NAD names.
//   - CloudHostDeviceName / CloudHostDeviceTag: cne-controller matches ENIs by these.
const (
	// InstanceNamespace is the k8s namespace where the CNE instance resources live
	// (cloud-network-mapping CM, IRSA SA, CNEInstance CR, NADs).
	InstanceNamespace = "f5-cne-system"

	// OperatorNamespace is the k8s namespace where FLO/OTEL/License resources live.
	// Already used in phase14/phase15 as operatorNS — duplicated here for host-device
	// constant set completeness.
	OperatorNamespace = "f5-cne-core"

	// ExternalNAD is the NAD name for the external (client-side) TMM data-path interface.
	ExternalNAD = "external-netdevice"

	// InternalNAD is the NAD name for the internal (backend-side) TMM data-path interface.
	InternalNAD = "internal-netdevice"

	// ExternalIFName is the Linux interface name for the external TMM NIC (device index 3).
	ExternalIFName = "ens8"

	// InternalIFName is the Linux interface name for the internal TMM NIC (device index 2).
	InternalIFName = "ens7"

	// ExternalPCI is the PCI bus address for the external TMM NIC.
	ExternalPCI = "0000:00:08.0"

	// InternalPCI is the PCI bus address for the internal TMM NIC.
	InternalPCI = "0000:00:07.0"

	// CloudHostDeviceTag is the EC2 tag key used to mark ENIs for the cne-controller.
	CloudHostDeviceTag = "f5-cne-device"

	// CloudHostDeviceName is the interface name the cne-controller uses to match ENIs.
	// Equals ExternalIFName — the controller matches by this name.
	CloudHostDeviceName = "ens8"
)
