package config

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// PER-COMPONENT ENV PASSTHROUGH FOR BNK 2.4 (#175).
//
// The 2.4 CNEInstance takes an `advanced.<component>.env[]` list for each of
// ~26 components (tmm, cneController, coremon, pseudoCNI, externalBigip,
// ipamController, …). A fixed override table cannot enumerate that and would go
// stale the first time F5 adds a component, so this follows the computed-family
// pattern the per-zone variables already established: one declaration that both
// the reader and SupportedOverrideNames derive from.
//
//	YAML:  bnk.advanced.<component>.env.<NAME>: value
//	Env:   ROKSBNKCTL_BNK_ADV_<COMPONENT>_ENV_<NAME>=value
//
// WHY THIS IS NOT OPTIONAL POLISH. `init --non-interactive` builds config.yaml
// from the environment alone, and every BNK Forge module configures the tool
// that way — so a field reachable only through YAML cannot be used by a
// blueprint at all.
//
// THE ADDITIVE GUARANTEE. Unset renders nothing: an empty map produces no
// `advanced` block, so a 2.3 workspace's plan is byte-identical to what it was.
// internal/tf/additive_guarantee_test.go enforces this, and it is the invariant
// most likely to break by accident with this many new fields.

// advEnvPrefix is the literal half of the computed variable names. Held apart so
// AdvancedEnvOverrideNames enumerates exactly what OverrideAdvancedEnvFromEnv
// reads.
const advEnvPrefix = "ROKSBNKCTL_BNK_ADV_"

// advEnvInfix separates the component from the variable name.
//
// The split is on the LAST occurrence, not the first: a component name cannot
// contain "_ENV_", but a variable name certainly can — TMM_ENV_OVERRIDE is a
// plausible F5 setting, and splitting on the first occurrence would file it
// under the wrong component with no error.
const advEnvInfix = "_ENV_"

// AdvancedEnvOverrideNames reports the computed family for the surface guard.
//
// Unlike the per-zone family this cannot be enumerated ahead of time — the
// component set belongs to the product, not to us. It reports what is actually
// SET in the environment, which is what the guard needs to check documentation
// and .env.example parity against.
func AdvancedEnvOverrideNames() []string {
	var out []string
	for _, kv := range os.Environ() {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, advEnvPrefix) {
			continue
		}
		if _, _, ok := splitAdvancedEnvName(name); ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// splitAdvancedEnvName pulls the component and variable name out of a computed
// override name. Returns ok=false for a name that carries the prefix but not a
// usable shape, so a typo is ignored rather than silently creating a component
// called "" with an empty env var.
func splitAdvancedEnvName(name string) (component, envName string, ok bool) {
	rest := strings.TrimPrefix(name, advEnvPrefix)
	i := strings.LastIndex(rest, advEnvInfix)
	if i <= 0 {
		return "", "", false
	}
	component = rest[:i]
	envName = rest[i+len(advEnvInfix):]
	if component == "" || envName == "" {
		return "", "", false
	}
	return component, envName, true
}

// OverrideAdvancedEnvFromEnv fills bnk.advanced from the computed family.
//
// Component names are lower-camelised from the SCREAMING_SNAKE env segment
// (CNECONTROLLER -> cnecontroller) and matched case-insensitively against what
// the CNEInstance expects, because the env-var spelling cannot preserve camel
// case. The rendered key uses the canonical spelling when one is known and the
// lowercased segment otherwise — an unknown component is passed through rather
// than dropped, since the component set belongs to the product and this tool
// should not be the reason a new one is unreachable.
func OverrideAdvancedEnvFromEnv(ws *Workspace) []string {
	var applied []string
	for _, name := range AdvancedEnvOverrideNames() {
		component, envName, ok := splitAdvancedEnvName(name)
		if !ok {
			continue
		}
		v := envValue(name)
		if v == "" {
			continue
		}
		key := canonicalAdvancedComponent(component)
		if ws.BNK.Advanced == nil {
			ws.BNK.Advanced = map[string]AdvancedComponentCfg{}
		}
		c := ws.BNK.Advanced[key]
		if c.Env == nil {
			c.Env = map[string]string{}
		}
		c.Env[envName] = v
		ws.BNK.Advanced[key] = c
		applied = append(applied, fmt.Sprintf("bnk.advanced.%s.env.%s (%s)", key, envName, name))
	}
	return applied
}

// knownAdvancedComponents maps the uppercased env segment to the CNEInstance's
// own camelCase spelling. Not exhaustive and not meant to be: it exists so the
// common components round-trip to the exact key the CR expects, while anything
// absent still passes through lowercased rather than being refused.
var knownAdvancedComponents = map[string]string{
	"TMM":            "tmm",
	"CNECONTROLLER":  "cneController",
	"COREMON":        "coremon",
	"PSEUDOCNI":      "pseudoCNI",
	"EXTERNALBIGIP":  "externalBigip",
	"IPAMCONTROLLER": "ipamController",
	"ENVDISCOVERY":   "envDiscovery",
	"CRDCONVERSION":  "crdConversion",
	"OBSERVER":       "observer",
	"FLUENTD":        "fluentd",
	"OTEL":           "otel",
	"RABBITMQ":       "rabbitmq",
	"CWC":            "cwc",
}

func canonicalAdvancedComponent(seg string) string {
	if c, ok := knownAdvancedComponents[strings.ToUpper(seg)]; ok {
		return c
	}
	return strings.ToLower(seg)
}
