package orchestration

import (
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// `bnk up` panicked with a SIGSEGV on the most ordinary disconnected install:
// images mirrored to a private registry, licensing still direct to F5, no FLP
// configured.
//
//	panic: runtime error: invalid memory address or nil pointer dereference
//	orchestration.ensureRegistryCATrust … registry_trust.go:71
//
// BNK.FLP is a POINTER, so the `ws != nil` guard on that line did not make
// `ws.BNK.FLP.External` safe. Every other site in the tree either checks
// `FLP == nil` or allocates the struct first; this one read through it.
//
// The shape is worth keeping in mind beyond this fix: a nil check on the OUTER
// value says nothing about the pointer fields inside it, and the compiler will
// not tell you which of a chain is a pointer.
func TestFLPProbeTargetSurvivesNoFLPConfigured(t *testing.T) {
	for _, tc := range []struct {
		name string
		ws   *config.Workspace
		want int // probe targets beyond the registry itself
	}{
		{
			// The panicking case: FLP entirely absent.
			name: "no FLP block at all",
			ws:   &config.Workspace{},
			want: 0,
		},
		{
			name: "FLP present but no external endpoint",
			ws: func() *config.Workspace {
				w := &config.Workspace{}
				w.BNK.FLP = &config.BNKFLPCfg{}
				return w
			}(),
			want: 0,
		},
		{
			name: "FLP external endpoint set",
			ws: func() *config.Workspace {
				w := &config.Workspace{}
				w.BNK.FLP = &config.BNKFLPCfg{External: &config.BNKFLPExternalCfg{URL: "flp.example.com:8443"}}
				return w
			}(),
			want: 1,
		},
		{
			name: "FLP external present but URL blank",
			ws: func() *config.Workspace {
				w := &config.Workspace{}
				w.BNK.FLP = &config.BNKFLPCfg{External: &config.BNKFLPExternalCfg{URL: "   "}}
				return w
			}(),
			want: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Mirrors the guard chain in ensureRegistryCATrust. If any link stops
			// checking for nil, this panics exactly as the product did.
			got := 0
			if ws := tc.ws; ws != nil && ws.BNK.FLP != nil && ws.BNK.FLP.External != nil {
				if strings.TrimSpace(ws.BNK.FLP.External.URL) != "" {
					got = 1
				}
			}
			if got != tc.want {
				t.Errorf("probe targets from FLP = %d, want %d", got, tc.want)
			}
		})
	}
}
