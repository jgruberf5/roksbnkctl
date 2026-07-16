package ibm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Transit Gateway read helpers, over the same global endpoint + authedGET the
// orphan sweep uses. Read-only: these back `tgw status` and pre-connect
// validation, so an operator sees the LIVE attachment state rather than only
// what roksbnkctl recorded at apply time.

// TransitGateway is a resolved Transit Gateway (more than the sweep's id+name).
type TransitGateway struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	CRN      string `json:"crn"`
	Location string `json:"location"`
	Status   string `json:"status"`
	Global   bool   `json:"global"`
}

// TGWConnection is one network attached to a gateway.
type TGWConnection struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	NetworkType string `json:"network_type"`
	// NetworkID is the attached network's CRN (the VPC CRN for network_type=vpc).
	NetworkID string `json:"network_id"`
	// Status is the live attachment state: attached | pending | deleting | failed.
	Status string `json:"status"`
}

// ListTransitGateways returns every Transit Gateway in the account (richer than
// the sweep's id+name-only listing).
func (c *Client) ListTransitGateways(ctx context.Context) ([]TransitGateway, error) {
	url := fmt.Sprintf("%s/v1/transit_gateways?version=%s", transitGatewayHost, vpcAPIVersion)
	body, err := c.authedGET(ctx, url)
	if err != nil {
		return nil, err
	}
	var page struct {
		TransitGateways []TransitGateway `json:"transit_gateways"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("parsing transit_gateways: %w", err)
	}
	return page.TransitGateways, nil
}

// ResolveTransitGateway finds the gateway matching nameOrID against BOTH id and
// name — the same match terraform's tgw_connection module makes, so validation
// here and the apply agree. Errors on zero or multiple matches.
func (c *Client) ResolveTransitGateway(ctx context.Context, nameOrID string) (*TransitGateway, error) {
	gws, err := c.ListTransitGateways(ctx)
	if err != nil {
		return nil, err
	}
	return matchTransitGateway(gws, nameOrID)
}

// matchTransitGateway is the pure name-or-id resolution — the terraform module
// filters identically. Errors on zero or multiple matches so an ambiguous name
// fails loudly instead of attaching to an arbitrary gateway.
func matchTransitGateway(gws []TransitGateway, nameOrID string) (*TransitGateway, error) {
	var matches []TransitGateway
	for _, g := range gws {
		if g.ID == nameOrID || g.Name == nameOrID {
			matches = append(matches, g)
		}
	}
	switch len(matches) {
	case 1:
		return &matches[0], nil
	case 0:
		return nil, fmt.Errorf("no Transit Gateway named or with id %q in this account", nameOrID)
	default:
		return nil, fmt.Errorf("Transit Gateway %q is ambiguous: matched %d gateways — pass the gateway id", nameOrID, len(matches))
	}
}

// ListTGWConnections returns the networks attached to a gateway.
func (c *Client) ListTGWConnections(ctx context.Context, gatewayID string) ([]TGWConnection, error) {
	url := fmt.Sprintf("%s/v1/transit_gateways/%s/connections?version=%s", transitGatewayHost, gatewayID, vpcAPIVersion)
	body, err := c.authedGET(ctx, url)
	if err != nil {
		return nil, err
	}
	var page struct {
		Connections []TGWConnection `json:"connections"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("parsing transit gateway connections: %w", err)
	}
	return page.Connections, nil
}

// TGWConnectionState reports the live attachment state of the connection whose
// network CRN matches vpcCRN, on the given gateway. Returns ("", nil) when the
// gateway carries no connection for that VPC (detached), so callers can
// distinguish "detached" from an API error.
func (c *Client) TGWConnectionState(ctx context.Context, gatewayID, vpcCRN string) (string, error) {
	conns, err := c.ListTGWConnections(ctx, gatewayID)
	if err != nil {
		return "", err
	}
	return connectionStateFor(conns, vpcCRN), nil
}

// connectionStateFor returns the status of the connection whose network CRN
// matches vpcCRN, or "" when none does (the VPC is detached from this gateway).
func connectionStateFor(conns []TGWConnection, vpcCRN string) string {
	for _, conn := range conns {
		if strings.EqualFold(conn.NetworkID, vpcCRN) {
			return conn.Status
		}
	}
	return ""
}
