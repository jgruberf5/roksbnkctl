package ibm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/IBM/platform-services-go-sdk/iamidentityv1"
)

// transitGatewayHost is the global Transit Gateway API endpoint.
const transitGatewayHost = "https://transit.cloud.ibm.com"

// OrphanResource is one leftover IBM Cloud resource discovered by the sweep,
// matched against the workspace's `<prefix>-*` naming convention.
type OrphanResource struct {
	Kind   string // instance | floating_ip | public_gateway | subnet | security_group | ssh_key | vpc | transit_gateway | cluster | cos_instance | trusted_profile
	ID     string
	Name   string
	Region string // VPC-regional resources carry their region; global ones leave it ""
	// CRN is the resource's Cloud Resource Name, when the listing carried one.
	// It exists for one reason: a Transit Gateway connection identifies its
	// attached network by CRN, so deciding whether a connection points at a VPC
	// THIS sweep is deleting is a CRN comparison. Empty for surfaces that do not
	// publish one; never used for deletion (that is always ID).
	CRN string
}

// SweepScope bounds the orphan search: the workspace prefix every resource is
// named from, the cluster ID (so the BNK trusted profile — named after the
// cluster, not the prefix — can still be found after the cluster is gone), and
// the regions to scan for regional VPC resources.
type SweepScope struct {
	Prefix    string
	ClusterID string
	Regions   []string
}

// OrphanKindOrder returns the teardown priority for a kind (lower = delete
// first). Honours the dependency chain: instances release their floating IPs
// and free subnets/SGs; the cluster is kicked off early (async) so its delete
// progresses while the rest runs; the cluster VPC goes last. Resources with the
// same order can be deleted in any sequence.
func OrphanKindOrder(kind string) int {
	switch kind {
	case "cluster":
		return 0
	case "instance":
		return 1
	case "floating_ip":
		return 2
	case "transit_gateway":
		return 3
	case "public_gateway":
		return 4
	case "subnet":
		return 5
	case "security_group":
		return 6
	case "vpc":
		return 7
	case "cos_instance":
		return 8
	case "trusted_profile":
		return 9
	default:
		return 100
	}
}

// matchesPrefix reports whether a resource name belongs to the workspace: an
// exact match (the cluster is named the bare prefix) or a `<prefix>-` child.
func matchesPrefix(name, prefix string) bool {
	if prefix == "" {
		return false
	}
	return name == prefix || strings.HasPrefix(name, prefix+"-")
}

// vpcHost is the regional VPC API endpoint for region.
func vpcHost(region string) string {
	return fmt.Sprintf("https://%s.iaas.cloud.ibm.com", region)
}

type vpcNamed struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	CRN  string `json:"crn"`
}

// vpcCollections maps each OrphanResource kind to its VPC collection path + the
// JSON key the items live under.
var vpcCollections = []struct{ kind, collection, key string }{
	{"instance", "instances", "instances"},
	{"floating_ip", "floating_ips", "floating_ips"},
	{"public_gateway", "public_gateways", "public_gateways"},
	{"subnet", "subnets", "subnets"},
	{"security_group", "security_groups", "security_groups"},
	{"ssh_key", "keys", "keys"},
	{"vpc", "vpcs", "vpcs"},
}

// listVPCCollection pages through a VPC collection in a region and returns the
// id+name of every item. Pagination follows the API's next.href cursor (which
// already carries the version/generation/start params).
func (c *Client) listVPCCollection(ctx context.Context, region, collection, key string) ([]vpcNamed, error) {
	url := fmt.Sprintf("%s/v1/%s?version=%s&generation=2&limit=100", vpcHost(region), collection, vpcAPIVersion)
	var out []vpcNamed
	for url != "" {
		body, err := c.authedGET(ctx, url)
		if err != nil {
			return nil, err
		}
		var page map[string]json.RawMessage
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", collection, err)
		}
		if raw, ok := page[key]; ok {
			var items []vpcNamed
			if err := json.Unmarshal(raw, &items); err != nil {
				return nil, fmt.Errorf("parsing %s items: %w", collection, err)
			}
			out = append(out, items...)
		}
		url = ""
		if raw, ok := page["next"]; ok {
			var next struct {
				Href string `json:"href"`
			}
			if json.Unmarshal(raw, &next) == nil {
				url = next.Href
			}
		}
	}
	return out, nil
}

// listTransitGateways returns the account's transit gateways (id+name).
func (c *Client) listTransitGateways(ctx context.Context) ([]vpcNamed, error) {
	url := fmt.Sprintf("%s/v1/transit_gateways?version=%s", transitGatewayHost, vpcAPIVersion)
	body, err := c.authedGET(ctx, url)
	if err != nil {
		return nil, err
	}
	var page struct {
		TransitGateways []vpcNamed `json:"transit_gateways"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("parsing transit_gateways: %w", err)
	}
	return page.TransitGateways, nil
}

// FindOrphans enumerates every `<prefix>-*` resource still present in the
// account across the scope's regions plus the global Transit Gateway, COS,
// cluster, and trusted-profile surfaces. Per-surface errors are swallowed
// (best-effort discovery — an inaccessible region or service shouldn't abort
// the whole sweep); only a name match contributes a result.
func (c *Client) FindOrphans(ctx context.Context, scope SweepScope) ([]OrphanResource, error) {
	var found []OrphanResource

	// Regional VPC resources.
	for _, region := range dedupeNonEmpty(scope.Regions) {
		for _, vc := range vpcCollections {
			items, err := c.listVPCCollection(ctx, region, vc.collection, vc.key)
			if err != nil {
				continue
			}
			for _, it := range items {
				if matchesPrefix(it.Name, scope.Prefix) {
					found = append(found, OrphanResource{Kind: vc.kind, ID: it.ID, Name: it.Name, Region: region, CRN: it.CRN})
				}
			}
		}
	}

	// Transit gateways (global).
	if tgws, err := c.listTransitGateways(ctx); err == nil {
		for _, t := range tgws {
			if matchesPrefix(t.Name, scope.Prefix) {
				found = append(found, OrphanResource{Kind: "transit_gateway", ID: t.ID, Name: t.Name})
			}
		}
	}

	// Registry COS instances (resource controller).
	if cos, err := c.ListCOSInstances(ctx); err == nil {
		for _, ci := range cos {
			if matchesPrefix(ci.Name, scope.Prefix) {
				found = append(found, OrphanResource{Kind: "cos_instance", ID: ci.CRN, Name: ci.Name})
			}
		}
	}

	// The ROKS cluster (named the bare prefix).
	if info, err := c.GetCluster(ctx, scope.Prefix); err == nil && info.ID != "" {
		found = append(found, OrphanResource{Kind: "cluster", ID: info.ID, Name: info.Name})
	}

	// BNK trusted profile — named "<clusterid>-f5-cne-controller-f5-bnk", so it
	// is only identifiable when the cluster ID is known (passed from
	// cluster-outputs.json, which survives the cluster's deletion).
	if scope.ClusterID != "" {
		if prof, err := c.findBNKTrustedProfile(ctx, scope.ClusterID); err == nil && prof != nil {
			found = append(found, *prof)
		}
	}

	return found, nil
}

// findBNKTrustedProfile locates the FLO CNE-controller trusted profile for a
// cluster ID, listing the account's profiles and matching on the id-prefixed,
// f5-bnk-suffixed name.
func (c *Client) findBNKTrustedProfile(ctx context.Context, clusterID string) (*OrphanResource, error) {
	if c.identity == nil {
		if _, err := c.Verify(ctx); err != nil {
			return nil, err
		}
	}
	accountID := c.identity.AccountID
	res, _, err := c.iam.ListProfilesWithContext(ctx, &iamidentityv1.ListProfilesOptions{
		AccountID: &accountID,
	})
	if err != nil {
		return nil, err
	}
	for _, p := range res.Profiles {
		name := ""
		if p.Name != nil {
			name = *p.Name
		}
		if strings.HasPrefix(name, clusterID) && strings.Contains(name, "f5-cne-controller") {
			return &OrphanResource{Kind: "trusted_profile", ID: *p.ID, Name: name}, nil
		}
	}
	return nil, nil
}

// DeleteOrphan deletes a single discovered resource. Idempotent (a 404 is
// success).
//
// sweep is every resource this run is deleting. Only the Transit Gateway path
// reads it, and it must: a gateway cannot be deleted while anything is attached,
// and whether detaching a given network is in scope depends on whether the
// sweep is deleting that network too. Pass the full discovered set.
func (c *Client) DeleteOrphan(ctx context.Context, o OrphanResource, sweep []OrphanResource) error {
	switch o.Kind {
	case "instance", "floating_ip", "public_gateway", "subnet", "security_group", "ssh_key", "vpc":
		collection := vpcCollectionFor(o.Kind)
		url := fmt.Sprintf("%s/v1/%s/%s?version=%s&generation=2", vpcHost(o.Region), collection, o.ID, vpcAPIVersion)
		return c.authedDELETE(ctx, url)
	case "transit_gateway":
		return c.deleteTransitGateway(ctx, o.ID, o.Name, sweptVPCCRNs(sweep))
	case "cluster":
		url := fmt.Sprintf("%s/global/v1/clusters/%s?deleteResources=true", containerServiceBase, o.ID)
		return c.authedDELETE(ctx, url)
	case "cos_instance":
		return c.DeleteCOSInstance(ctx, o.ID, true)
	case "trusted_profile":
		_, err := c.iam.DeleteProfileWithContext(ctx, &iamidentityv1.DeleteProfileOptions{ProfileID: &o.ID})
		return err
	default:
		return fmt.Errorf("unknown orphan kind %q", o.Kind)
	}
}

// sweptVPCCRNs is the set of VPC CRNs this run is deleting — the authority for
// "is detaching this network part of what the operator agreed to?". Only VPCs
// count: a subnet or an instance is not something a gateway attaches to.
func sweptVPCCRNs(sweep []OrphanResource) map[string]bool {
	crns := map[string]bool{}
	for _, o := range sweep {
		if o.Kind == "vpc" && o.CRN != "" {
			crns[strings.ToLower(o.CRN)] = true
		}
	}
	return crns
}

// ErrForeignTGWConnection is returned when a gateway still has a connection to a
// network the sweep is NOT deleting. Callers use it to explain that the failure
// is a deliberate refusal, not something a re-run can clear.
var ErrForeignTGWConnection = errors.New("transit gateway has connections to networks outside this sweep")

// tgwConnectionSettleTimeout / tgwConnectionPollInterval bound the wait for
// deleted connections to disappear. IBM removes a connection ASYNCHRONOUSLY —
// the DELETE returns while the connection sits in `deleting` — and the gateway
// DELETE fails 412 for as long as ANY connection is still listed. Detaching a
// VPC is quick in practice; the timeout only has to outlast that.
//
// var rather than const so the sequencing tests can shorten them; a real
// five-minute timeout would make the bounded-wait case unrunnable.
var (
	tgwConnectionSettleTimeout = 5 * time.Minute
	tgwConnectionPollInterval  = 5 * time.Second
)

// deleteTransitGateway detaches the gateway's connections and then deletes it.
//
// The order was already right; the WAIT was missing. The previous version fired
// every connection DELETE, discarded each result, and immediately deleted the
// gateway — which IBM refuses with 412 while the connections are still
// `deleting`. A re-run then appeared to fix it, but only because the
// connections had finished clearing in the meantime. That made an ordinary race
// look like a transient the "re-run cleanup" advice covered (#85).
//
// It also decides, deliberately, what may be detached. A gateway matching the
// workspace prefix is in scope; the networks attached to it are not necessarily
// so. Connections to VPCs this sweep is ALSO deleting are ours to remove.
// Anything else — a VPC under another prefix, another account's network via a
// cross-account connection, a non-VPC attachment such as Direct Link or a GRE
// tunnel — belongs to someone else, and a prefix-scoped cleanup has no mandate
// to disconnect it. Those refuse, naming what is attached, rather than silently
// detaching a shared gateway's other tenants.
func (c *Client) deleteTransitGateway(ctx context.Context, id, name string, sweptVPCs map[string]bool) error {
	gatewayURL := fmt.Sprintf("%s/v1/transit_gateways/%s?version=%s", transitGatewayHost, id, vpcAPIVersion)

	conns, err := c.ListTGWConnections(ctx, id)
	if err != nil {
		// DeleteOrphan is documented idempotent, and the commonest reason this
		// listing fails is that the gateway is already gone — a re-run, or a
		// concurrent sweep. Returning here would break that contract (the code
		// this replaced swallowed the error and let the DELETE's own 404 report
		// success). So still attempt the delete: 404 succeeds, and a gateway
		// that genuinely still has connections comes back as IBM's own 412
		// rather than a guess made here. Only if BOTH fail is the listing error
		// worth reporting, and then it explains why we could not be smarter.
		if derr := c.authedDELETE(ctx, gatewayURL); derr != nil {
			return fmt.Errorf("could not list connections for transit gateway %s (%v), and deleting it failed: %w", displayTGW(name, id), err, derr)
		}
		return nil
	}

	// #87: partition SETTLED state. A connection still `pending` is refused with
	// 409 invalid_state, and the old code sent swept-VPC connections straight to
	// DELETE whatever their status — so a sweep run right after an interrupted
	// attach failed, and said "re-run", which failed identically until IBM
	// finished attaching. That wait is one cleanup can do itself.
	// Refuse BEFORE waiting. A connection's ownership is knowable from the first
	// listing — network_type and network_id do not change as it settles — so a
	// foreign connection that happens to be `pending` would otherwise cost the
	// operator the full settle timeout before being told, correctly, that this
	// gateway is not ours to delete. Refusals should be immediate; only work we
	// intend to DO is worth waiting for.
	if _, _, foreign := partitionTGWConnections(conns, sweptVPCs); len(foreign) > 0 {
		return foreignTGWConnectionError(name, id, foreign)
	}

	conns, err = c.waitTGWConnectionsSettled(ctx, id, conns)
	if err != nil {
		return err
	}

	detach, settling, foreign := partitionTGWConnections(conns, sweptVPCs)
	if len(foreign) > 0 {
		// The region hint is not filler. cleanup scans only the workspace's
		// cluster and client regions by default, so the likeliest reason a VPC
		// looks foreign is that it IS yours and simply was not scanned — and
		// without this the operator is told to go detach their own network by
		// hand.
		return foreignTGWConnectionError(name, id, foreign)
	}

	// Anything still ARRIVING here outlasted the settle wait. Say so and stop,
	// rather than falling through: nothing was detachable, so the cleared-wait
	// below would burn a second full timeout on connections we never deleted and
	// then report "waiting for connections to detach" — which is not what
	// happened. Halves the worst case and names the actual obstacle.
	var stillArriving []TGWConnection
	for _, conn := range settling {
		if tgwConnectionArriving(conn.Status) {
			stillArriving = append(stillArriving, conn)
		}
	}
	if len(stillArriving) > 0 && len(detach) == 0 {
		return fmt.Errorf("transit gateway %s still has a connection attaching after %s: %s — IBM refuses a DELETE from `pending`, so this has to finish before the gateway can go. Re-run once it reports `attached`",
			displayTGW(name, id), tgwConnectionSettleTimeout, describeTGWConnections(stillArriving))
	}

	for _, conn := range detach {
		delURL := fmt.Sprintf("%s/v1/transit_gateways/%s/connections/%s?version=%s", transitGatewayHost, id, conn.ID, vpcAPIVersion)
		if err := c.authedDELETE(ctx, delURL); err != nil {
			// Surfaced, not swallowed: the old code discarded this and let the
			// gateway delete fail with an opaque 412 instead.
			return fmt.Errorf("detaching connection %s from transit gateway %s: %w", displayTGW(conn.Name, conn.ID), displayTGW(name, id), err)
		}
	}

	// Wait only when something actually has to clear — what we just detached,
	// or what was already on its way out. A gateway that listed nothing is
	// deletable now, and polling it once more to be told so is a wasted round
	// trip on the common re-run path.
	if len(detach) > 0 || len(settling) > 0 {
		if err := c.waitTGWConnectionsCleared(ctx, id); err != nil {
			return err
		}
	}

	return c.authedDELETE(ctx, gatewayURL)
}

// foreignTGWConnectionError is the single wording for the refusal, shared by
// the pre-wait check and the post-wait one so the two cannot drift apart.
//
// The region hint is not filler. cleanup scans only the workspace's cluster and
// client regions by default, so the likeliest reason a VPC looks foreign is that
// it IS yours and simply was not scanned — and without this the operator is told
// to go detach their own network by hand.
func foreignTGWConnectionError(name, id string, foreign []TGWConnection) error {
	return fmt.Errorf("%w: %s is still attached to %s — cleanup detaches only VPCs it is also deleting, so removing these is your call. If a listed VPC is yours, it may just be in a region this sweep did not scan: re-run with --all-regions (or --region <name>). Otherwise detach it with `ibmcloud tg connection-delete %s <connection-id>`, or delete those networks, then sweep again",
		ErrForeignTGWConnection, displayTGW(name, id), describeTGWConnections(foreign), id)
}

// tgwConnectionInFlight reports whether a connection is mid-transition. IBM
// accepts a connection DELETE only from a settled state, so these are the
// statuses to WAIT OUT rather than act on:
//
//	pending    — still attaching (#87)
//	deleting   — already on its way out (#85)
//	detaching  — the same, under IBM's other spelling
//
// Deliberately a denylist of TRANSIENT states, not an allowlist of `attached`.
// A connection stuck in `failed` or `suspended` is not going to become
// `attached`, but it CAN be deleted — requiring `attached` would make cleanup
// wait out its whole timeout and then refuse to remove exactly the wreckage it
// exists to remove.
func tgwConnectionInFlight(status string) bool {
	return tgwConnectionArriving(status) || tgwConnectionDeparting(status)
}

// tgwConnectionArriving — still attaching. Ours to wait for; someone else's to
// refuse over, which is why this is distinct from departing.
func tgwConnectionArriving(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), "pending")
}

// tgwConnectionDeparting — on its way out, under either of IBM's spellings.
// Whoever started it, the connection will be gone, so it is never a reason to
// refuse the gateway — only a reason to wait.
func tgwConnectionDeparting(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "deleting", "detaching":
		return true
	}
	return false
}

// waitTGWConnectionsSettled blocks until no connection is mid-transition, and
// returns the final listing so the caller partitions fresh state rather than
// the stale read it started from.
//
// This is the #85 wait applied to the ATTACH side. #85 waited after issuing the
// detaches; #87 showed the same race exists before them — a connection still
// `pending` is refused with 409 invalid_state, and `cleanup` is most often
// reached for precisely when an interrupted `up` has left one attaching.
//
// On timeout it returns what it has rather than failing: the DELETE that
// follows produces IBM's own error, which beats a guess made here, and the
// partition treats anything still in flight as settling so it is never deleted.
func (c *Client) waitTGWConnectionsSettled(ctx context.Context, id string, conns []TGWConnection) ([]TGWConnection, error) {
	deadline := time.Now().Add(tgwConnectionSettleTimeout)
	for {
		var inFlight []TGWConnection
		for _, conn := range conns {
			if tgwConnectionInFlight(conn.Status) {
				inFlight = append(inFlight, conn)
			}
		}
		if len(inFlight) == 0 || time.Now().After(deadline) {
			return conns, nil
		}
		select {
		case <-ctx.Done():
			return conns, ctx.Err()
		case <-time.After(tgwConnectionPollInterval):
		}
		next, err := c.ListTGWConnections(ctx, id)
		if err != nil {
			// Same reasoning as the cleared-wait: stop waiting and let the
			// delete report, rather than inventing a failure here.
			return conns, nil
		}
		conns = next
	}
}

// partitionTGWConnections sorts a gateway's connections into three buckets.
//
//   - detach   — a VPC attachment whose VPC this sweep is also deleting. Ours to
//     remove, and the only bucket a DELETE is issued for.
//   - settling — already `deleting`. On its way out with or without us, so it
//     blocks the gateway but must NOT be deleted again. Issuing a second DELETE
//     risks a non-404 rejection, and authedDELETE forgives only 404 — that error
//     would abort the whole gateway delete for a connection that was about to
//     disappear on its own. Waiting is both correct and free.
//   - foreign  — everything else. Refused, never touched.
//
// `settling` is checked first and deliberately does not consider ownership: a
// connection on its way out cannot be saved by refusing, and treating it as
// foreign would refuse a gateway seconds from being deletable — re-introducing
// #85 for the one case a re-run genuinely does fix.
func partitionTGWConnections(conns []TGWConnection, sweptVPCs map[string]bool) (detach, settling, foreign []TGWConnection) {
	for _, conn := range conns {
		ours := strings.EqualFold(conn.NetworkType, "vpc") && sweptVPCs[strings.ToLower(conn.NetworkID)]
		switch {
		// DEPARTING — ownership is irrelevant. It will be gone either way, so
		// refusing the gateway over someone else's outgoing connection would
		// block a delete that is seconds from being possible (#85).
		case tgwConnectionDeparting(conn.Status):
			settling = append(settling, conn)

		// NOT OURS — refuse, whatever state it is in. Checked BEFORE the
		// arriving case on purpose: ownership does not change as a connection
		// settles, so it is knowable from the first listing, and making the
		// operator wait out the settle timeout for a refusal we can give now is
		// wasted time. Collapsing "arriving" and "departing" into one
		// in-flight check hid this — a foreign connection that happened to be
		// `pending` was classified as ours to wait for.
		case !ours:
			foreign = append(foreign, conn)

		// OURS, still ARRIVING — wait it out. IBM refuses a DELETE from
		// `pending` with 409 invalid_state (#87).
		case tgwConnectionArriving(conn.Status):
			settling = append(settling, conn)

		default:
			detach = append(detach, conn)
		}
	}
	return detach, settling, foreign
}

// waitTGWConnectionsCleared blocks until the gateway lists no connections, or
// the timeout expires. This is the step whose absence produced #85.
func (c *Client) waitTGWConnectionsCleared(ctx context.Context, id string) error {
	deadline := time.Now().Add(tgwConnectionSettleTimeout)
	for {
		conns, err := c.ListTGWConnections(ctx, id)
		if err != nil {
			// Two cases land here and both want the same thing: the gateway is
			// already gone (a re-run), or the API blipped. Fall through to the
			// DELETE — it is idempotent on 404, and if connections really do
			// remain it returns the 412 with IBM's own wording, which is a
			// better report than a guess made here. That 412 IS the transient
			// the "re-run cleanup" advice covers.
			return nil
		}
		if len(conns) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for transit gateway %s connections to detach; still present: %s",
				tgwConnectionSettleTimeout, id, describeTGWConnections(conns))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(tgwConnectionPollInterval):
		}
	}
}

// describeTGWConnections renders connections for an error message: what is
// attached and what kind of thing it is, so the operator can decide without
// going to look it up.
func describeTGWConnections(conns []TGWConnection) string {
	parts := make([]string, 0, len(conns))
	for _, conn := range conns {
		kind := conn.NetworkType
		if kind == "" {
			kind = "unknown"
		}
		desc := fmt.Sprintf("%s [%s", displayTGW(conn.Name, conn.ID), kind)
		if conn.NetworkID != "" {
			desc += " " + conn.NetworkID
		}
		if conn.Status != "" {
			desc += ", " + conn.Status
		}
		parts = append(parts, desc+"]")
	}
	return strings.Join(parts, ", ")
}

// displayTGW prefers a name, falling back to the id when the API returned none.
func displayTGW(name, id string) string {
	if name == "" {
		return id
	}
	return name
}

func vpcCollectionFor(kind string) string {
	for _, vc := range vpcCollections {
		if vc.kind == kind {
			return vc.collection
		}
	}
	return ""
}

// SortOrphans orders resources for deletion by OrphanKindOrder (stable within a
// kind), so callers delete in dependency order.
func SortOrphans(orphans []OrphanResource) {
	sort.SliceStable(orphans, func(i, j int) bool {
		return OrphanKindOrder(orphans[i].Kind) < OrphanKindOrder(orphans[j].Kind)
	})
}

func dedupeNonEmpty(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
