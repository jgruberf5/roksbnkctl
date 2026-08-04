package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/jgruberf5/roksbnkctl/internal/k8s"
)

// The `tfx` command family is the Windows-native replacement for the terraform
// modules' `local-exec` shell glue (bash/curl/grep). Terraform invokes it as
// `${roksbnkctl_binary} tfx <verb> --flags ...` with NO `interpreter` set, so on
// Windows `cmd.exe /C` just execs roksbnkctl.exe. See the PRD at
// docs/prd/native-windows-tfx.md.
//
// Invocation contract (cross-platform): flags-only commands (no pipes / shell
// builtins); the kube token is read from an ENV var, never the command line; any
// manifest/large payload arrives on stdin. So `cmd.exe` vs `sh` quoting cannot
// diverge. Exit code is the contract: 0 = success.
//
// Hidden: it is machine-facing, not for humans.

var (
	flagTFXKubeHost   string
	flagTFXTokenEnv   string
	flagTFXInsecure   bool
	flagTFXKubeconfig string
	flagTFXCAFile     string
)

var tfxCmd = &cobra.Command{
	Use:    "tfx",
	Short:  "Terraform helper verbs (internal; invoked by the bundled HCL)",
	Hidden: true,
	Long: `tfx is the cross-platform replacement for the terraform modules' local-exec
shell glue. Each verb speaks to the cluster through this binary's own Go client
libraries, so a deployment needs no bash/curl/grep on the host — the modules run
natively on Windows with only terraform + helm on PATH.

Terraform invokes these; you do not. See docs/prd/native-windows-tfx.md.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	// Shared connection flags — persistent so every verb inherits them.
	pf := tfxCmd.PersistentFlags()
	pf.StringVar(&flagTFXKubeHost, "kube-host", "", "kube API server URL (default: $KUBE_HOST)")
	pf.StringVar(&flagTFXTokenEnv, "token-env", "KUBE_TOKEN", "env var holding the bearer token (never passed on the command line)")
	pf.BoolVar(&flagTFXInsecure, "insecure", false, "skip TLS verification (matches the modules' historical `curl -sk`)")
	pf.StringVar(&flagTFXCAFile, "kube-ca-file", "", "PEM CA bundle to verify the API server (ignored when --insecure)")
	pf.StringVar(&flagTFXKubeconfig, "kubeconfig", "", "kubeconfig path (local/testing fallback instead of --kube-host + token env)")
	rootCmd.AddCommand(tfxCmd)
}

// tfxRESTConfig resolves the cluster connection from the shared flags. Precedence:
// an explicit --kubeconfig (local/testing) wins; otherwise host+token (the
// terraform contract — token from an env var, never the command line).
func tfxRESTConfig() (*rest.Config, error) {
	if flagTFXKubeconfig != "" {
		return k8s.BuildRESTConfig(flagTFXKubeconfig)
	}
	host := flagTFXKubeHost
	if host == "" {
		host = os.Getenv("KUBE_HOST")
	}
	tokenEnv := flagTFXTokenEnv
	if tokenEnv == "" {
		tokenEnv = "KUBE_TOKEN"
	}
	token := os.Getenv(tokenEnv)

	var caData []byte
	if !flagTFXInsecure && flagTFXCAFile != "" {
		b, err := os.ReadFile(flagTFXCAFile)
		if err != nil {
			return nil, fmt.Errorf("reading --kube-ca-file %q: %w", flagTFXCAFile, err)
		}
		caData = b
	}
	return k8s.RESTConfigFromHostToken(host, token, flagTFXInsecure, caData)
}

// tfxDynamic builds the dynamic client the wait/delete/patch verbs use (explicit
// --gvr, no mapper needed).
func tfxDynamic() (dynamic.Interface, error) {
	cfg, err := tfxRESTConfig()
	if err != nil {
		return nil, err
	}
	return k8s.DynamicClientForConfig(cfg)
}

// tfxDynamicAndMapper adds a discovery-backed REST mapper, for `tfx apply` where
// the manifest carries a Kind that must be resolved to its resource.
func tfxDynamicAndMapper() (dynamic.Interface, meta.RESTMapper, error) {
	cfg, err := tfxRESTConfig()
	if err != nil {
		return nil, nil, err
	}
	dc, err := k8s.DynamicClientForConfig(cfg)
	if err != nil {
		return nil, nil, err
	}
	mapper, err := k8s.RESTMapperForConfig(cfg)
	if err != nil {
		return nil, nil, err
	}
	return dc, mapper, nil
}

// parseGVR parses a --gvr string into a GroupVersionResource. Accepts
// "group/version/resource" (e.g. apps/v1/deployments, k8s.f5.com/v1/cneinstances)
// or a core-group "version/resource" (e.g. v1/pods, with an optional leading "/").
// Taking the resource plural directly means no discovery/REST-mapper round-trip.
func parseGVR(s string) (schema.GroupVersionResource, error) {
	parts := strings.Split(strings.TrimPrefix(s, "/"), "/")
	switch len(parts) {
	case 3:
		if parts[1] == "" || parts[2] == "" {
			break
		}
		return schema.GroupVersionResource{Group: parts[0], Version: parts[1], Resource: parts[2]}, nil
	case 2:
		if parts[0] == "" || parts[1] == "" {
			break
		}
		// Core group: version/resource.
		return schema.GroupVersionResource{Group: "", Version: parts[0], Resource: parts[1]}, nil
	}
	return schema.GroupVersionResource{}, fmt.Errorf(
		"invalid --gvr %q: want group/version/resource (e.g. apps/v1/deployments) or version/resource for the core group (e.g. v1/pods)", s)
}

// tfxResource resolves the dynamic ResourceInterface for a gvr, namespaced when
// ns is non-empty (cluster-scoped otherwise).
func tfxResource(dc dynamic.Interface, gvr schema.GroupVersionResource, ns string) dynamic.ResourceInterface {
	if ns == "" {
		return dc.Resource(gvr)
	}
	return dc.Resource(gvr).Namespace(ns)
}
