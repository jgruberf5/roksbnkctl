package jumphost

// AiperfSSHExecFn exposes the aiperf SSH-exec seam for unit tests.
// Tests replace it to assert command construction without network.
var AiperfSSHExecFn = &aiperfSSHExecFn

// BuildAiperfCmd exposes buildAiperfCmd for command-construction tests.
var BuildAiperfCmd = buildAiperfCmd
