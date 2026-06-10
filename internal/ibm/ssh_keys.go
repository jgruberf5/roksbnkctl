package ibm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// SSHKey is a minimal view of an IBM Cloud VPC SSH key.
type SSHKey struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
	Type      string `json:"type"`
}

// GetSSHKeyByName returns the VPC SSH key named name in region, or (nil, nil)
// when no key by that name exists there. VPC SSH keys are REGIONAL — the same
// name in two regions is two independent keys — so callers check each region a
// jumphost lives in.
func (c *Client) GetSSHKeyByName(ctx context.Context, region, name string) (*SSHKey, error) {
	url := fmt.Sprintf("%s/v1/keys?version=%s&generation=2&limit=100", vpcHost(region), vpcAPIVersion)
	for url != "" {
		body, err := c.authedGET(ctx, url)
		if err != nil {
			return nil, err
		}
		var page struct {
			Keys []SSHKey `json:"keys"`
			Next struct {
				Href string `json:"href"`
			} `json:"next"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("parsing SSH keys in %s: %w", region, err)
		}
		for i := range page.Keys {
			if page.Keys[i].Name == name {
				k := page.Keys[i]
				return &k, nil
			}
		}
		url = page.Next.Href
	}
	return nil, nil
}

// CreateSSHKey uploads publicKeyOpenSSH as a VPC SSH key named name in region.
// The key type is inferred from the public key (ed25519 / rsa) so it works for
// both a roksbnkctl-generated ed25519 key and replicating an existing key.
func (c *Client) CreateSSHKey(ctx context.Context, region, name, publicKeyOpenSSH, resourceGroupID string) (*SSHKey, error) {
	reqBody := map[string]any{
		"name":       name,
		"public_key": publicKeyOpenSSH,
		"type":       keyTypeFromPublic(publicKeyOpenSSH),
	}
	if resourceGroupID != "" {
		reqBody["resource_group"] = map[string]string{"id": resourceGroupID}
	}
	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/v1/keys?version=%s&generation=2", vpcHost(region), vpcAPIVersion)
	resp, err := c.authedPOST(ctx, url, bodyJSON)
	if err != nil {
		return nil, fmt.Errorf("creating SSH key %q in %s: %w", name, region, err)
	}
	var k SSHKey
	if err := json.Unmarshal(resp, &k); err != nil {
		return nil, fmt.Errorf("parsing created key: %w", err)
	}
	return &k, nil
}

// keyTypeFromPublic maps an OpenSSH public-key line to the IBM VPC key `type`.
func keyTypeFromPublic(pub string) string {
	if strings.HasPrefix(strings.TrimSpace(pub), "ssh-rsa") {
		return "rsa"
	}
	return "ed25519"
}
