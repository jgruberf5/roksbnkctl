package test

// The Gateway API surface BNK actually installs.
//
// This exists because #99 shipped a fixture for `gateway.networking.k8s.io/v1alpha2
// TCPRoute` — a CRD BNK never installs. The object could not be created, the
// iperf3 L4 leg of the matrix never ran, and because the fixture apply is
// best-effort the failure was silent: no L4 result rather than an error. A test
// existed and was green, because it asserted the same wrong shape the code had.
//
// Naming the contract once, here, is what makes it checkable. Anything that
// emits, deletes, or waits on a route kind derives it from these — so a kind
// that is not in the channel has no name to spell, and a guard test can find
// one that is spelled anyway.
//
// BNK 2.3 pins Gateway API **1.4.1 standard**: six CRDs, all annotated
// channel=standard. The standard channel has no TCPRoute, TLSRoute or UDPRoute
// — those are experimental-channel kinds. BNK supplies L4Route
// (gateway.k8s.f5net.com/v1) for TCP instead.
const (
	// GatewayAPIGroup is the upstream Gateway API group.
	GatewayAPIGroup = "gateway.networking.k8s.io"
	// BNKGatewayGroup is BNK's own gateway group, which supplies L4Route.
	BNKGatewayGroup = "gateway.k8s.f5net.com"
)

// RouteKind is one route kind, with the group and plural resource name needed
// to address it as a CRD (`<plural>.<group>`, the form kubectl delete takes).
type RouteKind struct {
	Kind     string
	Group    string
	Version  string
	Resource string // plural, lowercase
}

// CRD is "<resource>.<group>" — how a kind is named to kubectl.
func (r RouteKind) CRD() string { return r.Resource + "." + r.Group }

// APIVersion is "<group>/<version>" — how a kind is named in a manifest.
func (r RouteKind) APIVersion() string { return r.Group + "/" + r.Version }

// Route kinds BNK installs and this tool may create.
var (
	HTTPRouteKind = RouteKind{Kind: "HTTPRoute", Group: GatewayAPIGroup, Version: "v1", Resource: "httproutes"}
	GRPCRouteKind = RouteKind{Kind: "GRPCRoute", Group: GatewayAPIGroup, Version: "v1", Resource: "grpcroutes"}
	L4RouteKind   = RouteKind{Kind: "L4Route", Group: BNKGatewayGroup, Version: "v1", Resource: "l4routes"}
)

// InstalledRouteKinds are the route kinds a BNK cluster can actually serve.
// Fixture teardown deletes exactly these, so a kind added here is cleaned up
// without a second edit somewhere else remembering to.
var InstalledRouteKinds = []RouteKind{HTTPRouteKind, GRPCRouteKind, L4RouteKind}

// AbsentRouteKinds are Gateway API route kinds that exist upstream but are NOT
// in the standard channel, so they can never be created against a BNK cluster.
//
// Listed rather than merely omitted: an object of one of these kinds applies
// cleanly to a cluster that has the experimental channel and vanishes silently
// on one that does not, which is precisely how #99 stayed invisible. The guard
// test uses this list to fail on any reference to them.
var AbsentRouteKinds = []string{"TCPRoute", "TLSRoute", "UDPRoute"}

// FixtureRouteCRDs is the CRD list fixture teardown deletes, derived from
// InstalledRouteKinds so it cannot name a kind the fixtures never create — or
// miss one they do.
func FixtureRouteCRDs() []string {
	out := make([]string, 0, len(InstalledRouteKinds))
	for _, r := range InstalledRouteKinds {
		out = append(out, r.CRD())
	}
	return out
}
