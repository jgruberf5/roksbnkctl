package k8s

import (
	"context"
	"testing"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func webhookCfg(name, svcNS string) *admissionregistrationv1.ValidatingWebhookConfiguration {
	wc := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{Name: name + ".example.com"},
		},
	}
	if svcNS != "" {
		wc.Webhooks[0].ClientConfig.Service = &admissionregistrationv1.ServiceReference{
			Namespace: svcNS, Name: "f5-validation-svc",
		}
	}
	return wc
}

// #208. The webhook must go before the namespace, or the namespace never
// finishes deleting: its service disappears first and every further deletion
// calls a webhook that cannot answer.
func TestDeleteOrphanedAdmissionWebhooksRemovesTheOneServedFromTheDoomedNamespace(t *testing.T) {
	cs := fake.NewSimpleClientset(webhookCfg("f5validate-f5-bnk", "f5-bnk"))
	got, err := DeleteOrphanedAdmissionWebhooks(context.Background(), cs, "f5-bnk", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "f5validate-f5-bnk" {
		t.Fatalf("got %v; want [f5validate-f5-bnk]", got)
	}
	if _, err := cs.AdmissionregistrationV1().ValidatingWebhookConfigurations().
		Get(context.Background(), "f5validate-f5-bnk", metav1.GetOptions{}); err == nil {
		t.Error("the configuration still exists, so the namespace would still deadlock")
	}
}

// The dangerous failure is over-reach. A webhook served from somewhere else
// belongs to something else, and deleting it disables admission control the
// operator still relies on — on a shared cluster, for other tenants.
func TestDeleteOrphanedAdmissionWebhooksLeavesOtherNamespacesAlone(t *testing.T) {
	cs := fake.NewSimpleClientset(
		webhookCfg("f5validate-f5-bnk", "f5-bnk"),
		webhookCfg("someone-elses", "kube-system"),
		webhookCfg("url-based", ""), // no service ref at all
	)
	got, err := DeleteOrphanedAdmissionWebhooks(context.Background(), cs, "f5-bnk", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("deleted %v; want only the f5-bnk one", got)
	}
	for _, keep := range []string{"someone-elses", "url-based"} {
		if _, err := cs.AdmissionregistrationV1().ValidatingWebhookConfigurations().
			Get(context.Background(), keep, metav1.GetOptions{}); err != nil {
			t.Errorf("%s was deleted; it is served from outside the destroyed namespace", keep)
		}
	}
}

// A teardown that already ran, or an install that never got this far, are both
// fine — this runs on every bnk down and must not fail either.
func TestDeleteOrphanedAdmissionWebhooksIsQuietWhenThereIsNothingToDo(t *testing.T) {
	cs := fake.NewSimpleClientset()
	got, err := DeleteOrphanedAdmissionWebhooks(context.Background(), cs, "f5-bnk", nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got (%v, %v); want (nil, nil) on an empty cluster", got, err)
	}
	// An empty namespace means "no BNK namespace known" — it must not sweep
	// every webhook on the cluster.
	cs2 := fake.NewSimpleClientset(webhookCfg("f5validate-f5-bnk", "f5-bnk"))
	got2, err := DeleteOrphanedAdmissionWebhooks(context.Background(), cs2, "", nil)
	if err != nil || len(got2) != 0 {
		t.Fatalf("got (%v, %v) for an empty namespace; want nothing touched", got2, err)
	}
}
