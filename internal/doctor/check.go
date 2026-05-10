package doctor

// CheckStatus is the outcome of a single Check.
type CheckStatus string

const (
	StatusOK      CheckStatus = "ok"
	StatusWarning CheckStatus = "warning"
	StatusError   CheckStatus = "error"
	StatusSkipped CheckStatus = "skipped"
)

// Check is a single doctor diagnostic. Future per-backend checks
// (Phase 3, see docs/prd/03-EXECUTION-BACKENDS.md) will be expressed
// as Check values with BackendName set so the same rendering logic
// covers them.
type Check struct {
	Name        string
	Status      CheckStatus
	Detail      string
	Optional    bool
	BackendName string // empty for general; "docker"|"k8s"|"ssh" later
}
