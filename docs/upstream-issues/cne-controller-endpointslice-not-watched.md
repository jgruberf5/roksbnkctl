# Upstream issue draft — f5-cne-controller: pool members not refreshed on EndpointSlice change

> Draft upstream issue for filing against F5's BIG-IP Next for Kubernetes (BNK) project. Live-reproduced and worked around (BNK 2.3.0 / manifest `2.3.0-3.2598.3-0.0.170`). User-facing severity: **major** — VIP returns HTTP 500 silently until an operator notices and manually patches the HTTPRoute.

## Summary

`f5-cne-controller` resolves HTTPRoute `backendRefs` → Service → EndpointSlice → TMM pool members **only at HTTPRoute spec reconcile time**. It does NOT subscribe to EndpointSlice change events for backend services. When a backend pod is rescheduled (new pod IP), the EndpointSlice updates correctly but TMM's `pool_member` table retains the stale (deleted) pod IP. Traffic landing on the VIP returns HTTP 500 ("no available pool member") indefinitely.

The only documented workaround is to bounce the HTTPRoute (delete + re-apply, or patch the spec) so the controller re-reads the current EndpointSlice. This is a known issue verified live on multiple occasions.

## Versions

- BNK chart: `2.3.0`
- CNE manifest: `2.3.0-3.2598.3-0.0.170`
- EKS: `v1.30.14-eks-3385e9b`
- Containerd: `2.2.3`
- Cluster shape: 3 nodes, 1 TMM pod (host-device data plane), 1 nginx test backend

## Reproduction (any BNK 2.3.0 cluster)

1. Deploy a `Gateway` with `gatewayClassName: <bnk>` and an `HTTPRoute` whose `backendRef` points at a Service backed by a regular pod (e.g. nginx in the same namespace).
2. Curl the Gateway address → HTTP 200, traffic flows.
3. Delete the backend pod (`kubectl delete pod <nginx-pod>`). A new pod is scheduled with a **different IP**.
4. Verify the EndpointSlice updates immediately: `kubectl get endpointslices -n <ns>` shows the new pod IP.
5. Curl the Gateway address again → **HTTP 500** indefinitely. `tmctl pool_member_stat` (in the f5-tmm pod's `debug` sidecar) shows the OLD (now-deleted) pod IP, not the new one.

## Diagnostic evidence

### TMM perspective (stale pool member)

```
$ kubectl exec -n f5-cne-system f5-tmm-<id> -c debug -- \
    tmctl -d blade -c pool_member_stat | grep nginx
f5-cne-system-nginx-gw-http-80-nginx-route-rule-0-pool,\
00:00:00:00:00:00:00:00:00:00:FF:FF:0A:00:01:46:00:00:00:00,80,...
                                          ~~~~~~~~~~~
# 0A:00:01:46 = 10.0.1.70 — old, deleted pod IP
```

### Kubernetes perspective (current pod IP)

```
$ kubectl get endpointslices -n f5-cne-system | grep nginx
nginx-cvvms   IPv4   80   10.0.1.76   3h30m   # new pod IP
```

### cne-controller behaviour

After the backend pod is rescheduled, `kubectl logs f5-cne-controller-<id> -c f5-cne-controller` shows:

- No `"GatewayReconciler: handling http route update"` line for the affected HTTPRoute.
- Periodic `"Endpointslice nginx-<...> changed, syncing"` lines DO appear (the watch is firing) but only for **internal F5 services** (grpc-pccd-svc, grpc-pod-mgr-svc, f5-analyzer-grpc-svc, etc.) — not for user-defined backends referenced by HTTPRoute.

Annotation-only or finalizer-only patches to the HTTPRoute are explicitly ignored:

```
"Only CR status/finalizer is updated, ignoring the update" CrName="nginx-route"
```

A **spec** change DOES trigger a reconcile:

```
"Updating HTTPRoute" CrName="nginx-route" Operation="Update"
"GatewayReconciler: handling http route update" RouteName="nginx-route"
"Monitors derived from backendRefs" RouteName="nginx-route" rule index=0
```

After the spec-triggered reconcile, `tmctl pool_member_stat` shows the new pod IP (`0A:00:01:4C` = 10.0.1.76) and curl returns HTTP 200.

### Confirmed-ineffective recoveries

We tried, with full logs captured each time:

- **Restarting f5-cne-controller** — no effect. On restart, the controller observes the HTTPRoute (status shows `ResolvedRefs=True` at the post-restart timestamp) but never pushes the new pool member to TMM. Hypothesis: the restart-time reconcile dedups against the controller's in-memory cache, which was populated from the pre-restart state in TMM; the new EndpointSlice doesn't trigger a reconcile because it didn't *change* during the restart window.
- **Restarting f5-tmm** — would lose connection state and isn't a viable recovery for a production workload.
- **Patching HTTPRoute annotations** — explicitly filtered out by the controller's update predicate (log line above).
- **Patching HTTPRoute finalizers** — same.

## Suggested fix

Add an EndpointSlice informer to `GatewayReconciler` (or the equivalent component responsible for translating `HTTPRoute.backendRefs` → pool members). On EndpointSlice add/update/delete events, enqueue reconciles for every HTTPRoute whose `backendRefs` reference the parent Service. The Kubernetes controller-runtime `Watches()` builder supports this pattern out-of-the-box via `handler.EnqueueRequestsFromMapFunc`.

Reference implementation pattern (controller-runtime):

```go
func (r *HTTPRouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&gatewayv1.HTTPRoute{}).
        Watches(
            &discoveryv1.EndpointSlice{},
            handler.EnqueueRequestsFromMapFunc(r.findRoutesForSlice),
        ).
        Complete(r)
}

func (r *HTTPRouteReconciler) findRoutesForSlice(ctx context.Context, obj client.Object) []reconcile.Request {
    slice := obj.(*discoveryv1.EndpointSlice)
    serviceName := slice.Labels["kubernetes.io/service-name"]
    if serviceName == "" {
        return nil
    }
    var routes gatewayv1.HTTPRouteList
    _ = r.Client.List(ctx, &routes, client.InNamespace(slice.Namespace))
    var requests []reconcile.Request
    for _, route := range routes.Items {
        for _, rule := range route.Spec.Rules {
            for _, ref := range rule.BackendRefs {
                if ref.BackendRef.BackendObjectReference.Name == gatewayv1.ObjectName(serviceName) {
                    requests = append(requests, reconcile.Request{
                        NamespacedName: types.NamespacedName{
                            Name:      route.Name,
                            Namespace: route.Namespace,
                        },
                    })
                }
            }
        }
    }
    return requests
}
```

## Operator-side workaround (until the fix lands)

`awsbnkctl bnk resync <httproute-name> -n <namespace>` (shipped in `awsbnkctl` slice-11) does the spec-toggle for you:

```
weight 1 → 2  (forces spec generation bump → controller reconciles)
weight 2 → 1  (restores original weight)
```

The controller picks up the spec change, re-resolves the EndpointSlice, and pushes fresh pool members. Idempotent. Behaviour-preserving. No pod restarts. Verified live: curl response transitions from HTTP 500 to HTTP 200 within ~1 second of running `awsbnkctl bnk resync`.

## Impact

This affects any production deployment where backend pods can be evicted, drained, or replaced (so: every production deployment). The current behaviour silently breaks the VIP and provides no signal to the operator that the pool is stale beyond observing HTTP 500 traffic. A naive operator who restarts the controller will see no improvement, escalating to "is BNK broken?" investigations that are expensive in operator time.

## Suggested labels

`area/gateway-api`, `area/data-plane-sync`, `kind/bug`, `priority/high`, `affects-version/2.3.0`
