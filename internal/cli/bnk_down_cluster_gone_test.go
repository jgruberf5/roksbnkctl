package cli

import (
	"errors"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/ibm"
)

// #79: `bnk down` failed when the cluster it targets no longer exists. It cannot
// uninstall from a cluster that is not there, so the useful outcome is the same
// as the existing "no trial state" case — report and exit 0.
//
// The probe is deliberately conservative, and these pin that: only an explicit
// ErrClusterNotFound counts as absence. A false negative costs the status quo;
// a false positive silently skips a real uninstall.

// The decisive property: a TRANSIENT failure must never read as absence, or a
// network blip would leave BNK running on a cluster the operator believes clean.
func TestClusterGoneOnlyForExplicitNotFound(t *testing.T) {
	out := &config.ClusterOutputs{ClusterID: "d9abc", ClusterName: "prod"}
	for _, c := range []struct {
		name string
		err  error
		gone bool
	}{
		{"explicit provider not-found", ibm.ErrClusterNotFound, true},
		{"wrapped not-found", errWrap(ibm.ErrClusterNotFound), true},
		{"timeout", errors.New("context deadline exceeded"), false},
		{"connection refused", errors.New("dial tcp 10.0.0.1:443: connect: connection refused"), false},
		{"auth failure", errors.New("401 Unauthorized"), false},
		{"dns failure", errors.New("no such host"), false},
		{"nil — the cluster is there", nil, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			gone, name := clusterGoneFromLookup(out, c.err)
			if gone != c.gone {
				t.Errorf("gone=%v, want %v", gone, c.gone)
			}
			if gone && name != "prod" {
				t.Errorf("name=%q, want the recorded cluster name", name)
			}
		})
	}
}

func errWrap(e error) error { return errors.Join(errors.New("looking up cluster: "), e) }

// No usable record means nothing to probe: the destroy must proceed exactly as
// before rather than be skipped.
func TestClusterGoneFalseWithoutAUsableRecord(t *testing.T) {
	for _, out := range []*config.ClusterOutputs{
		nil,
		{},
		{ClusterName: "named-but-no-id"},
	} {
		if gone, _ := clusterGoneFromLookup(out, ibm.ErrClusterNotFound); gone {
			t.Errorf("skipped the destroy with no usable cluster record: %+v", out)
		}
	}
}

// Falls back to the id when no name was recorded, so the message never says
// cluster "".
func TestClusterGoneFallsBackToID(t *testing.T) {
	gone, name := clusterGoneFromLookup(&config.ClusterOutputs{ClusterID: "d9abc"}, ibm.ErrClusterNotFound)
	if !gone || name != "d9abc" {
		t.Errorf("gone=%v name=%q", gone, name)
	}
}
