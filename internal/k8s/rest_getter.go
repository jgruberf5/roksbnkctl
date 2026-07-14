package k8s

import (
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

// newRESTClientGetter builds a cli-runtime RESTClientGetter wired to the
// roksbnkctl kubeconfig discovery rules. We delegate to cli-runtime's
// own ConfigFlags and just plumb our resolved path + namespace through.
//
// kubeconfigPath: empty → workspace default via DefaultKubeconfigPath().
// "in-cluster" sentinel is *not* supported by ConfigFlags directly —
// callers needing in-cluster mode use BuildClientset/BuildDynamicClient
// and bypass cli-runtime. Get/Apply/Describe always run against an
// explicit kubeconfig in v1.
//
// kubeContext pins which context in the file is used. "" means "whatever
// the file's current-context says". Callers that resolved the file by
// WORKSPACE pass the context naming that workspace's cluster, so a shared
// multi-cluster ~/.kube/config whose current-context points somewhere else
// still gets addressed correctly instead of silently talking to the wrong
// cluster.
//
// namespace is the value of -n; "" means "fall back to whatever the
// context says (or 'default')".
func newRESTClientGetter(kubeconfigPath, kubeContext, namespace string) genericclioptions.RESTClientGetter {
	cf := genericclioptions.NewConfigFlags(true)
	if kubeconfigPath == "" {
		kubeconfigPath = DefaultKubeconfigPath()
	}
	if kubeconfigPath != "" && kubeconfigPath != InClusterKubeconfigSentinel {
		cf.KubeConfig = &kubeconfigPath
	}
	if kubeContext != "" {
		cf.Context = &kubeContext
	}
	if namespace != "" {
		cf.Namespace = &namespace
	}
	return cf
}
