package k8s

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Registry-CA trust for air-gapped ROKS.
//
// On ROKS the native OpenShift additionalTrustedCA / MachineConfig path for
// injecting a private registry's CA into the node container runtime is blocked
// (the cluster is HostedCluster-managed and the image.config / MachineConfig
// surface is reconciled away). The working workaround is a privileged
// DaemonSet — one pod per node — that writes the CA into each node's
// /etc/containers/certs.d/<host>/ca.crt (and the docker path), which CRI-O
// reads per-pull with no reboot. A DaemonSet (not an init container) is
// required because the CA must be on EVERY node before ANY chart's pods pull;
// an init container only prepares its own pod's node.
//
// EnsureRegistryCATrust emits that stack natively — the install command is a
// Go-built string, so the shell loop variable ($d) reaches the container's
// /bin/sh intact rather than being eaten by an external heredoc. A hash of the
// CA is annotated on the pod template so a changed CA (e.g. a cluster rebuild
// that regenerates the registry's self-signed cert) auto-rolls the DaemonSet.

const (
	registryTrustNamespace = "roksbnkctl-registry-trust"
	registryTrustName      = "registry-ca-installer"
	registryTrustCAKey     = "ca.crt"
	registryTrustHashAnnot = "roksbnkctl.f5.com/registry-ca-hash"
)

// EnsureRegistryCATrust installs (or refreshes) the registry CA on every node
// via a privileged DaemonSet and blocks until it is Ready on all nodes.
//
//   - host       the bare registry host[:port] as it appears in pull refs
//     (e.g. "10.241.0.4"). The CA lands at certs.d/<host>/ca.crt.
//   - caPEM      the PEM-encoded CA (or self-signed leaf) the registry serves.
//   - image      the installer image. It must be node-cached (air-gap nodes
//     cannot pull public images); when empty, the node-resolver image already
//     present on every ROKS node is auto-discovered. imagePullPolicy is
//     IfNotPresent so the installer itself never needs a pull.
//   - wait        when true, block until the DaemonSet is Ready on all nodes.
func (c *Client) EnsureRegistryCATrust(ctx context.Context, host, caPEM, image string, wait bool) error {
	host = strings.TrimSpace(host)
	caPEM = strings.TrimSpace(caPEM)
	if host == "" {
		return fmt.Errorf("registry CA trust: empty host")
	}
	if caPEM == "" {
		return fmt.Errorf("registry CA trust: empty CA PEM for %s", host)
	}

	sum := sha256.Sum256([]byte(caPEM))
	caHash := hex.EncodeToString(sum[:])[:16]

	if image == "" {
		img, err := c.nodeCachedImage(ctx)
		if err != nil {
			return fmt.Errorf("registry CA trust: no installer image given and node-cached image discovery failed: %w", err)
		}
		image = img
	}

	if err := c.ensureNamespace(ctx, registryTrustNamespace); err != nil {
		return err
	}
	if err := c.ensureServiceAccount(ctx, registryTrustNamespace, registryTrustName); err != nil {
		return err
	}
	if err := c.ensurePrivilegedSCCBinding(ctx, registryTrustNamespace, registryTrustName); err != nil {
		return err
	}
	if err := c.ensureCAConfigMap(ctx, registryTrustNamespace, caPEM); err != nil {
		return err
	}
	if err := c.ensureInstallerDaemonSet(ctx, host, image, caHash); err != nil {
		return err
	}

	if !wait {
		return nil
	}
	return c.waitRegistryTrustReady(ctx, 3*time.Minute)
}

// nodeCachedImage returns an image known to be present on every ROKS node, so
// the installer DaemonSet can run under air-gap (imagePullPolicy IfNotPresent)
// without pulling. The openshift-dns node-resolver runs on every node.
func (c *Client) nodeCachedImage(ctx context.Context) (string, error) {
	ds, err := c.clientset.AppsV1().DaemonSets("openshift-dns").Get(ctx, "node-resolver", metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("reading openshift-dns/node-resolver: %w", err)
	}
	if len(ds.Spec.Template.Spec.Containers) == 0 {
		return "", fmt.Errorf("openshift-dns/node-resolver has no containers")
	}
	img := ds.Spec.Template.Spec.Containers[0].Image
	if img == "" {
		return "", fmt.Errorf("openshift-dns/node-resolver container has no image")
	}
	return img, nil
}

func (c *Client) ensureServiceAccount(ctx context.Context, ns, name string) error {
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
	_, err := c.clientset.CoreV1().ServiceAccounts(ns).Create(ctx, sa, metav1.CreateOptions{})
	if k8serr.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// ensurePrivilegedSCCBinding binds the installer SA to the OpenShift privileged
// SCC (via its auto-generated ClusterRole) so the DaemonSet's privileged pods
// admit.
func (c *Client) ensurePrivilegedSCCBinding(ctx context.Context, ns, sa string) error {
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: registryTrustName + "-scc"},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "system:openshift:scc:privileged",
		},
		Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: sa, Namespace: ns}},
	}
	_, err := c.clientset.RbacV1().ClusterRoleBindings().Create(ctx, crb, metav1.CreateOptions{})
	if k8serr.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (c *Client) ensureCAConfigMap(ctx context.Context, ns, caPEM string) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "registry-ca", Namespace: ns},
		Data:       map[string]string{registryTrustCAKey: caPEM},
	}
	cms := c.clientset.CoreV1().ConfigMaps(ns)
	_, err := cms.Create(ctx, cm, metav1.CreateOptions{})
	if k8serr.IsAlreadyExists(err) {
		_, err = cms.Update(ctx, cm, metav1.UpdateOptions{})
	}
	return err
}

// registryCAInstallCmd builds the DaemonSet container's install command. $d is
// a literal in this Go string, so it reaches the container's /bin/sh as the
// shell loop variable — no external heredoc interpolation can eat it (the
// failure mode that wrote the CA to a bogus in-container path). host is
// substituted here by Go, not by any shell.
func registryCAInstallCmd(host string) string {
	return fmt.Sprintf(
		`set -e; for d in /host/etc/containers/certs.d /host/etc/docker/certs.d; do `+
			`mkdir -p "$d/%s"; rm -f "$d/%s/ca.crt"; install -m644 /ca/ca.crt "$d/%s/ca.crt"; done; `+
			`echo "registry CA installed for %s"; sleep infinity`,
		host, host, host, host)
}

func (c *Client) ensureInstallerDaemonSet(ctx context.Context, host, image, caHash string) error {
	priv := true
	installCmd := registryCAInstallCmd(host)

	labels := map[string]string{"app": registryTrustName}
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: registryTrustName, Namespace: registryTrustNamespace, Labels: labels},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: map[string]string{registryTrustHashAnnot: caHash},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: registryTrustName,
					Tolerations:        []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					Containers: []corev1.Container{{
						Name:            "installer",
						Image:           image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Command:         []string{"/bin/sh", "-c"},
						Args:            []string{installCmd},
						SecurityContext: &corev1.SecurityContext{Privileged: &priv},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "hostetc", MountPath: "/host/etc"},
							{Name: "ca", MountPath: "/ca"},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "hostetc", VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{Path: "/etc"},
						}},
						{Name: "ca", VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: "registry-ca"},
							},
						}},
					},
				},
			},
		},
	}

	dsClient := c.clientset.AppsV1().DaemonSets(registryTrustNamespace)
	existing, err := dsClient.Get(ctx, registryTrustName, metav1.GetOptions{})
	if k8serr.IsNotFound(err) {
		_, err = dsClient.Create(ctx, ds, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	// Preserve resourceVersion for the update; a changed hash/host/image rolls
	// the pods (the pod-template annotation carries the CA hash).
	ds.ResourceVersion = existing.ResourceVersion
	_, err = dsClient.Update(ctx, ds, metav1.UpdateOptions{})
	return err
}

// waitRegistryTrustReady blocks until the installer DaemonSet reports Ready on
// every node it targets (and its latest generation is observed), or the timeout
// elapses.
func (c *Client) waitRegistryTrustReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	dsClient := c.clientset.AppsV1().DaemonSets(registryTrustNamespace)
	for {
		ds, err := dsClient.Get(ctx, registryTrustName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		s := ds.Status
		rolledOut := s.ObservedGeneration >= ds.Generation
		desired := s.DesiredNumberScheduled
		if rolledOut && desired > 0 &&
			s.NumberReady == desired && s.UpdatedNumberScheduled == desired {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("registry CA installer not ready on all nodes after %s (ready %d/%d)",
				timeout, s.NumberReady, desired)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}
