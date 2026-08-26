package mirror

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"

	"github.com/jgruberf5/roksbnkctl/internal/registry/ocireg"
)

// #224. `registry adopt` refused a fully populated Artifactory mirror because
// its registry-wide /v2/_catalog answers with an empty body — the repositories
// live under the per-repository catalogue at /v2/<repo>/_catalog. The mirror
// held 94 artifacts and the probe reported zero.

// catalogServer serves /v2/_catalog and /v2/<seg>/_catalog from a table, so a
// test can model a registry that answers one, the other, or neither.
func catalogServer(t *testing.T, byPath map[string][]string) (string, func()) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/_catalog") {
			w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
			w.WriteHeader(http.StatusOK)
			return
		}
		repos, served := byPath[r.URL.Path]
		if !served {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"repositories": repos})
	})
	srv := httptest.NewServer(mux)
	return strings.TrimPrefix(srv.URL, "http://"), srv.Close
}

func probeEngine(host string) *Engine {
	return &Engine{Target: &ocireg.Target{Host: host, Namespace: "bnk-mirror", Auth: authn.Anonymous}}
}

// The straightforward case must keep working: a registry whose ROOT catalogue
// lists the repositories is answered from the root, with no fallback.
func TestTheRootCatalogueIsUsedWhenItAnswers(t *testing.T) {
	host, stop := catalogServer(t, map[string][]string{
		"/v2/_catalog": {"bnk-mirror/images/tmm-img", "bnk-mirror/charts/f5-tmm", "other/thing"},
	})
	defer stop()

	n, err := probeEngine(host).ProbeNamespace(context.Background(), "bnk-mirror")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if n != 2 {
		t.Errorf("counted %d repositories under bnk-mirror, want 2 (the third is another namespace)", n)
	}
}

// THE REPORTED CASE. Artifactory answers the root catalogue with nothing and
// serves the real listing under /v2/<repo>/_catalog. Believing the empty root
// made `adopt` reject a mirror holding 94 artifacts unless --force.
func TestAnEmptyRootCatalogueFallsBackToTheScopedOne(t *testing.T) {
	host, stop := catalogServer(t, map[string][]string{
		"/v2/_catalog":            {}, // Artifactory: empty body
		"/v2/bnk-mirror/_catalog": {"images/tmm-img", "images/f5ingress", "charts/f5-tmm"},
	})
	defer stop()

	n, err := probeEngine(host).ProbeNamespace(context.Background(), "bnk-mirror")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if n == 0 {
		t.Fatal("the probe still reported an empty mirror.\n" +
			"Artifactory serves its repositories under /v2/<repo>/_catalog, not the registry-wide " +
			"one, so `adopt` refused a fully populated mirror unless --force (#224).")
	}
	if n != 3 {
		t.Errorf("counted %d, want 3 from the scoped catalogue", n)
	}
}

// Some builds return the scoped listing with the repository name still on the
// front. Both shapes must count, or the fallback fixes one Artifactory and not
// another.
func TestTheScopedCatalogueCountsAbsoluteNamesToo(t *testing.T) {
	host, stop := catalogServer(t, map[string][]string{
		"/v2/_catalog":            {},
		"/v2/bnk-mirror/_catalog": {"bnk-mirror/images/tmm-img", "bnk-mirror/charts/f5-tmm"},
	})
	defer stop()

	n, err := probeEngine(host).ProbeNamespace(context.Background(), "bnk-mirror")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if n != 2 {
		t.Errorf("counted %d, want 2 — the scoped listing used absolute names", n)
	}
}

// A genuinely empty mirror must still read as empty. The fallback is there to
// stop a false zero, not to manufacture a non-zero.
func TestAGenuinelyEmptyMirrorStillCountsZero(t *testing.T) {
	host, stop := catalogServer(t, map[string][]string{
		"/v2/_catalog":            {},
		"/v2/bnk-mirror/_catalog": {},
	})
	defer stop()

	n, err := probeEngine(host).ProbeNamespace(context.Background(), "bnk-mirror")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if n != 0 {
		t.Errorf("counted %d on an empty mirror, want 0 — the fallback must not invent repositories", n)
	}
}

// A registry that does not serve the scoped catalogue at all must fall back to
// the root's answer rather than surfacing the fallback's 404 as the result.
func TestAMissingScopedCatalogueReportsTheRootsAnswer(t *testing.T) {
	host, stop := catalogServer(t, map[string][]string{
		"/v2/_catalog": {}, // scoped path returns 404
	})
	defer stop()

	n, err := probeEngine(host).ProbeNamespace(context.Background(), "bnk-mirror")
	if err != nil {
		t.Errorf("probe returned an error for a registry with no scoped catalogue: %v\n"+
			"The fallback is an extra chance, not the contract — the root already answered.", err)
	}
	if n != 0 {
		t.Errorf("counted %d, want 0", n)
	}
}

func TestCountUnderPrefixMatchesOnPathSegments(t *testing.T) {
	repos := []string{"bnk-mirror", "bnk-mirror/images/x", "bnk-mirror-other/y", "unrelated"}
	if got := countUnderPrefix(repos, "bnk-mirror"); got != 2 {
		t.Errorf("countUnderPrefix = %d, want 2 — %q must not match the %q prefix",
			got, "bnk-mirror-other/y", "bnk-mirror")
	}
}

// THE FALLBACK MUST NOT DEFEAT THE PROBE.
//
// Many registries ignore the path and answer /v2/<anything>/_catalog with the
// ROOT catalogue. With a typo'd generic_repo_prefix the root count is
// legitimately 0, the fallback fires, and the registry hands back everything it
// holds — so a prefix nothing was ever pushed under would report repositories
// and `adopt` would record a mirror whose prefix is wrong. That is precisely the
// failure the probe exists to catch.
//
// Caught by the pre-existing TestEngine_ProbeNamespace when the fallback was
// first written; this pins it directly so the reason is not lost.
func TestAScopedListingIdenticalToTheRootIsNotTreatedAsScoped(t *testing.T) {
	all := []string{"bnk-mirror/one", "bnk-mirror/two", "somewhere-else/three"}
	host, stop := catalogServer(t, map[string][]string{
		"/v2/_catalog": all,
		// The registry ignores the path and returns the whole catalogue.
		"/v2/typo-mirror/_catalog": all,
	})
	defer stop()

	n, err := probeEngine(host).ProbeNamespace(context.Background(), "typo-mirror")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if n != 0 {
		t.Errorf("counted %d under a prefix nothing was pushed to, want 0.\n"+
			"The registry echoed the root catalogue; believing it makes `adopt` accept a "+
			"typo'd registry.generic_repo_prefix, which is the one thing the probe is for.", n)
	}
}

func TestSameRepoListIgnoresOrderButNotContent(t *testing.T) {
	if !sameRepoList([]string{"a", "b"}, []string{"b", "a"}) {
		t.Error("order-only difference reported as different")
	}
	if sameRepoList([]string{"a", "b"}, []string{"a", "c"}) {
		t.Error("different contents reported as the same")
	}
	if sameRepoList([]string{"a"}, []string{"a", "a"}) {
		t.Error("different lengths reported as the same")
	}
	if !sameRepoList(nil, nil) {
		t.Error("two empty listings reported as different")
	}
}

// The scoped fetch must reach the SAME registries the rest of the engine does.
// It builds its own http.Client instead of going through crane, so crane.Insecure
// does not apply and the behaviour has to be spelled out — leaving it out meant
// the fallback could not reach a self-signed mirror at all, which is the kind of
// registry most likely to need it. The failure is soft (the caller falls back to
// the root count), so the symptom is silence.
func TestTheScopedFetchHonoursInsecure(t *testing.T) {
	all := []string{"bnk-mirror/images/x"}
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/_catalog") {
			w.WriteHeader(http.StatusOK)
			return
		}
		repos := []string{}
		if r.URL.Path == "/v2/bnk-mirror/_catalog" {
			repos = all
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"repositories": repos})
	})
	// TLS with a self-signed cert: exactly the mirror --insecure exists for.
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "https://")

	eng := &Engine{
		Target:   &ocireg.Target{Host: host, Namespace: "bnk-mirror", Auth: authn.Anonymous},
		Insecure: true,
	}
	got, err := eng.scopedCatalog(context.Background(), host, "bnk-mirror")
	if err != nil {
		t.Fatalf("scoped fetch against a self-signed mirror with Insecure set: %v\n"+
			"The rest of the engine reaches this registry; this call must too.", err)
	}
	if len(got) != 1 {
		t.Errorf("got %v, want the one repository", got)
	}
}

// The scheme is attempted, not guessed. Keying it on the hostname — loopback
// means http — sends a cleartext request at the https port of an ordinary TLS
// registry on 127.0.0.1, which is what a self-signed lab mirror often is.
func TestThePlainHTTPFallbackReachesBothSchemes(t *testing.T) {
	for _, tls := range []bool{true, false} {
		mux := http.NewServeMux()
		mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"repositories": []string{"images/x"}})
		})
		var srv *httptest.Server
		if tls {
			srv = httptest.NewTLSServer(mux)
		} else {
			srv = httptest.NewServer(mux)
		}
		host := strings.TrimPrefix(strings.TrimPrefix(srv.URL, "https://"), "http://")
		eng := &Engine{
			Target:   &ocireg.Target{Host: host, Namespace: "bnk-mirror", Auth: authn.Anonymous},
			Insecure: true,
		}
		got, err := eng.scopedCatalog(context.Background(), host, "bnk-mirror")
		if err != nil {
			t.Errorf("tls=%v: %v", tls, err)
		} else if len(got) != 1 {
			t.Errorf("tls=%v: got %v, want one repository", tls, got)
		}
		srv.Close()
	}
}
