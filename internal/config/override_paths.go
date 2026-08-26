package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// OverridePaths reports, for every supported ROKSBNKCTL_* override, the dotted
// config.yaml path it writes — e.g. ROKSBNKCTL_TMM_REPLICAS -> bnk.tmm_replicas.
//
// It is DERIVED, not declared. Each override is applied to an empty workspace on
// its own, the result is marshalled, and the path that appears is the answer.
// The alternative — a second table mapping names to paths — is the defect this
// codebase keeps finding: a parallel list that every grep confirms and nothing
// keeps true. A probe cannot drift, because the thing it interrogates is the
// override machinery itself.
//
// Overrides that write no single scalar are reported with an empty path rather
// than guessed at: the computed per-zone family, and anything whose value is
// rejected by its own parser (an int override probed with a non-numeric sentinel
// sets nothing). Callers decide what to do with those; silently omitting them
// would make the map look complete when it is not.
func OverridePaths() map[string]string {
	// Overrides that reject a probe warn on stderr, which is correct for a real
	// run and pure noise here -- and this function's output is piped to a file,
	// so a warning about a sentinel would land in the user's .env. Silenced for
	// the duration rather than made conditional, so the warning stays unqualified
	// in the path that matters.
	if devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
		real := os.Stderr
		os.Stderr = devnull
		defer func() { os.Stderr = real; devnull.Close() }()
	}

	base := marshalPaths(&Workspace{})
	out := make(map[string]string, len(SupportedOverrideNames()))

	// The per-zone family has to be probed as a GROUP. A zone entry is only built
	// when ALL of its fields are present -- overrideNetworkZonesFromEnv skips a
	// partially-specified zone, which is correct behaviour and means a
	// single-variable probe can never move one. Probing them one at a time
	// reported all eighteen as writing nothing, which read like eighteen broken
	// overrides and was really one wrong probe.
	//
	// Multi-zone is the only shape this tool deploys, so leaving them unmapped
	// would drop the network configuration from every conversion.
	for name, path := range zoneFamilyPaths() {
		out[name] = path
	}

	for _, name := range SupportedOverrideNames() {
		if _, done := out[name]; done {
			continue
		}
		var changed, exact []string
		for _, probe := range probeCandidates {
			var ws Workspace
			OverrideFromMap(&ws, map[string]string{name: probe})
			changed, exact = nil, nil
			for path, val := range marshalPaths(&ws) {
				if base[path] == val {
					continue
				}
				changed = append(changed, path)
				// The path whose VALUE is the probe is the one the override
				// actually writes. The others changed as a SIDE EFFECT: setting
				// bnk.hugepages.count materialises the enclosing block, so
				// bnk.hugepages.enabled appears too and the override looks like it
				// writes two fields. Reporting both loses the setting, because a
				// caller with two paths and one value cannot tell which to use.
				if val == probe {
					exact = append(exact, path)
				}
			}
			if len(changed) > 0 {
				break
			}
		}
		if len(exact) == 1 {
			out[name] = exact[0]
			continue
		}
		sort.Strings(changed)
		switch len(changed) {
		case 0:
			out[name] = "" // writes nothing a marshal can see
		case 1:
			out[name] = changed[0]
		default:
			// One variable that writes several keys (a block toggle). Record them
			// all rather than picking one and being subtly wrong.
			out[name] = strings.Join(changed, ",")
		}
	}
	return out
}

// probeCandidates are tried in order until one moves the workspace.
//
// Guessing a type from the variable's NAME was the first attempt and it was
// wrong: a string sentinel fed to a bool override is rejected by its parser, the
// field never moves, and the override is reported as writing nothing — a false
// negative indistinguishable from a real defect. Thirty-nine overrides looked
// broken that way, all of them booleans.
//
// Trying every shape removes the guess. Whichever the parser accepts is the one
// that tells the truth about the field.
var probeCandidates = []string{
	"__probe__",    // strings
	"true",         // booleans
	"97",           // integers
	"cHJvYmU=",     // base64 fields, which reject an undecodable probe
	"10.97.0.0/16", // CIDRs validated on the way in
	"24",           // a LEGAL prefix length; 97 is not, and is rejected
	probeCertB64(), // a REAL certificate, for fields that parse the decoded PEM
	"LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUJwcm9iZQotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0tCg==", // base64 of a PEM, for fields that validate the decoded bytes
}

// A probe is only as good as the validators it satisfies. Two of these exist
// because a plausible-looking sentinel was rejected and the override then looked
// like it wrote nothing: 97 is not a legal VLAN prefix length, and the Forge CA
// field decodes its base64 and expects a certificate.

// marshalPaths flattens a workspace's YAML form to dotted-path -> scalar, so two
// workspaces can be compared by what they actually serialise rather than by
// reflection over unexported shapes.
func marshalPaths(ws *Workspace) map[string]string {
	b, err := yaml.Marshal(ws)
	if err != nil {
		return nil
	}
	var tree map[string]any
	if err := yaml.Unmarshal(b, &tree); err != nil {
		return nil
	}
	out := map[string]string{}
	flatten("", tree, out)
	return out
}

func flatten(prefix string, v any, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flatten(key, sub, out)
		}
	case []any:
		for i, sub := range t {
			flatten(fmt.Sprintf("%s[%d]", prefix, i), sub, out)
		}
	default:
		if prefix != "" {
			out[prefix] = fmt.Sprint(t)
		}
	}
}

// zoneFamilyPaths maps every ROKSBNKCTL_ZONE<n>_* variable to its config path by
// filling one zone completely with DISTINCT values and matching each value back
// to the path it landed on. Distinct values are what make the attribution exact:
// six identical probes would produce six indistinguishable fields.
func zoneFamilyPaths() map[string]string {
	out := map[string]string{}
	base := marshalPaths(&Workspace{})

	// EVERY zone at once, not one at a time. Filling a single zone makes it the
	// only entry in the list, so it lands at zones[0] whichever zone it is --
	// ZONE2 and ZONE3 both mapped to zones[0], which is true of that probe and
	// false of every real configuration.
	env := map[string]string{}
	valueOf := map[string]string{}
	for zone := 1; zone <= maxNetworkZones; zone++ {
		for i, f := range zoneFields {
			name := zoneOverridePrefix + strconv.Itoa(zone) + "_" + f.suffix
			// Distinct per zone AND per field, so each value identifies exactly one
			// path. Shaped like the CIDRs and IPs these fields hold, so any
			// per-field validation still accepts them.
			v := fmt.Sprintf("10.%d.%d.0/24", zone, i+1)
			if strings.HasSuffix(f.suffix, "SELFIP") {
				v = fmt.Sprintf("10.%d.%d.101", zone, i+1)
			}
			env[name] = v
			valueOf[name] = v
		}
	}

	var ws Workspace
	OverrideFromMap(&ws, env)
	got := marshalPaths(&ws)

	for name, want := range valueOf {
		for path, val := range got {
			if val == want && base[path] != val {
				out[name] = path
				break
			}
		}
	}
	return out
}

// probeCertB64 returns base64 of a genuine self-signed certificate.
//
// One override needs it: bnkforge.ca_b64 decodes its value and requires
// x509.AppendCertsFromPEM to accept it, so no sentinel string can satisfy it and
// the field looked like it wrote nothing. That validation is worth keeping --
// a malformed CA silently accepted means the pin does not happen and the session
// token travels unauthenticated -- so the probe rises to meet it rather than the
// check being loosened to make a probe convenient.
//
// Generated once, lazily, and only ever compared against; it authenticates
// nothing.
var probeCertOnce = sync.OnceValue(func() string {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return ""
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "roksbnkctl-override-probe"},
		NotBefore:             time.Unix(0, 0),
		NotAfter:              time.Unix(1<<31-1, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
})

func probeCertB64() string { return probeCertOnce() }
