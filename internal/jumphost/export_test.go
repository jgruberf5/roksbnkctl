package jumphost

// BuildCurlCmd exposes buildCurlCmd for unit tests in the external _test package.
var BuildCurlCmd = buildCurlCmd

// BuildCurlBodyCmd exposes buildCurlBodyCmd for unit tests in the external _test package.
var BuildCurlBodyCmd = buildCurlBodyCmd

// BuildHTTPResponderCmd exposes buildHTTPResponderCmd for unit tests.
var BuildHTTPResponderCmd = buildHTTPResponderCmd

// BuildHTTPResponderStopCmd exposes buildHTTPResponderStopCmd for unit tests.
var BuildHTTPResponderStopCmd = buildHTTPResponderStopCmd

// BuildGrpcurlInstallCmd is an alias for GrpcurlInstallCmd for unit tests.
// Uses the same var pattern as the other test exports for consistency.
var BuildGrpcurlInstallCmd = GrpcurlInstallCmd

// Seam overrides — tests swap these to avoid real ssh/aws subprocess calls.

// PrepareEICEKeyFn is the overridable seam for prepareEICEKey consumed by
// RunStagingCommands and CopyFileViaEICE. Tests replace it to assert
// mint-once behaviour without network.
var PrepareEICEKeyFn = &prepareEICEKeyFn

// SSHRunViaEICEFn is the overridable seam for SSHRunViaEICE consumed by
// RunStagingCommands and CopyFileViaEICE. Tests replace it to capture
// commands without network.
var SSHRunViaEICEFn = &sshRunViaEICEFn

// PushSSHPublicKeyFn is the overridable seam for PushSSHPublicKey consumed by
// RunStagingCommands and CopyFileViaEICE. Tests replace it to assert
// re-push-per-step behaviour without network.
var PushSSHPublicKeyFn = &pushSSHPublicKeyFn
