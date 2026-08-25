package k8s

import (
	"context"
	"fmt"
	"io"
	"strings"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// DeleteOrphanedAdmissionWebhooks removes ValidatingWebhookConfigurations whose
// service lives in a namespace that is about to be destroyed (#208).
//
// Order is the whole point. BNK installs `f5validate-f5-bnk`, pointing at
// `f5-validation-svc` INSIDE the BNK namespace. Destroying the namespace deletes
// the service first, and every subsequent attempt to remove the namespace's
// remaining content calls a webhook that can no longer answer:
//
//	Failed to delete all resource types, 2 remaining: Internal error occurred:
//	failed calling webhook "f5validate.f5net.com": ... service
//	"f5-validation-svc" not found
//
// The namespace then sits in Terminating forever — nothing retries its way out,
// because the webhook is never coming back. Deleting the configuration first
// removes the gate before the service disappears.
//
// Scoped to webhooks whose clientConfig actually points into the doomed
// namespace: a webhook served from somewhere else is someone else's, and
// deleting it would disable admission control the operator still relies on.
// Missing configurations are not an error — a teardown that already ran, or an
// install that never got this far, are both fine.
func DeleteOrphanedAdmissionWebhooks(ctx context.Context, cs kubernetes.Interface, namespace string, logw io.Writer) ([]string, error) {
	if namespace == "" {
		return nil, nil
	}
	api := cs.AdmissionregistrationV1()

	list, err := api.ValidatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing validating webhook configurations: %w", err)
	}

	var deleted []string
	for _, wc := range list.Items {
		if !webhookServedFrom(namespaceOf(wc), namespace) {
			continue
		}
		if err := api.ValidatingWebhookConfigurations().Delete(ctx, wc.Name, metav1.DeleteOptions{}); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return deleted, fmt.Errorf("deleting validating webhook configuration %s: %w", wc.Name, err)
		}
		deleted = append(deleted, wc.Name)
		if logw != nil {
			fmt.Fprintf(logw, "  removed validating webhook %s (served from %s, which is being destroyed)\n", wc.Name, namespace)
		}
	}
	return deleted, nil
}

// namespaceOf collects the namespaces a configuration's webhooks are served
// from. A webhook with a URL rather than a service reference has none, and is
// correctly left alone.
func namespaceOf(wc admissionregistrationv1.ValidatingWebhookConfiguration) []string {
	var out []string
	for _, w := range wc.Webhooks {
		if w.ClientConfig.Service != nil {
			out = append(out, w.ClientConfig.Service.Namespace)
		}
	}
	return out
}

func webhookServedFrom(namespaces []string, target string) bool {
	for _, n := range namespaces {
		if strings.EqualFold(n, target) {
			return true
		}
	}
	return false
}
