package opnsense

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// NATRule represents one OPNsense DNAT rule (external port → target IP:port).
// UUID is set when the rule is returned from the API (for updates/deletes).
type NATRule struct {
	UUID         string `json:"uuid,omitempty"`
	ExternalPort int    `json:"-"`
	Protocol     string `json:"-"`
	TargetIP     string `json:"-"`
	TargetPort   int    `json:"-"`
	Description  string `json:"-"`
}

// Client talks to the OPNsense API for NAT and VIP management.
type Client interface {
	ListNATRules(ctx context.Context) ([]NATRule, error)
	ApplyNATRules(ctx context.Context, desired []NATRule, managedBy string) error
}

// Config holds OPNsense API connection settings.
type Config struct {
	BaseURL   string
	APIKey    string
	APISecret string
	// Client is optional; used for tests. If nil, http.DefaultClient is used.
	Client *http.Client
}

// NewClient returns a Client implementation using the OPNsense API.
func NewClient(cfg Config) Client {
	c := &client{cfg: cfg}
	if c.cfg.Client == nil {
		c.cfg.Client = http.DefaultClient
	}
	return c
}

type client struct {
	cfg Config
}

// searchRuleResponse matches OPNsense search_rule JSON (rows array).
type searchRuleResponse struct {
	Rows []struct {
		UUID        string `json:"uuid"`
		Description string `json:"description"`
	} `json:"rows"`
}


// rulePayload is sent to add_rule. Field names follow OPNsense DNat model.
type rulePayload struct {
	Rule struct {
		Description string `json:"description"`
		Protocol    string `json:"protocol"`
		// Destination and target: OPNsense uses interface/dest port and target host/port.
		Destination string `json:"destination"`
		Target      string `json:"target"`
	} `json:"rule"`
}

func (c *client) ListNATRules(ctx context.Context) ([]NATRule, error) {
	base := strings.TrimSuffix(c.cfg.BaseURL, "/")
	u := base + "/api/firewall/d_nat/search_rule"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.cfg.APIKey, c.cfg.APISecret)
	q := req.URL.Query()
	q.Set("current", "1")
	q.Set("rowCount", "10000")
	req.URL.RawQuery = q.Encode()

	resp, err := c.cfg.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opnsense d_nat search_rule: %s", resp.Status)
	}
	var out searchRuleResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	var rules []NATRule
	for _, row := range out.Rows {
		rules = append(rules, NATRule{UUID: row.UUID, Description: row.Description})
	}
	return rules, nil
}

func (c *client) ApplyNATRules(ctx context.Context, desired []NATRule, managedBy string) error {
	current, err := c.listManagedRules(ctx, managedBy)
	if err != nil {
		return err
	}
	// Delete all current managed rules by UUID, then add all desired.
	for _, r := range current {
		if err := c.delRule(ctx, r.UUID); err != nil {
			return err
		}
	}
	for _, r := range desired {
		if err := c.addRule(ctx, r, managedBy); err != nil {
			return err
		}
	}
	if len(current) > 0 || len(desired) > 0 {
		if err := c.applyFirewall(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (c *client) listManagedRules(ctx context.Context, managedBy string) ([]NATRule, error) {
	all, err := c.ListNATRules(ctx)
	if err != nil {
		return nil, err
	}
	var out []NATRule
	for _, r := range all {
		if strings.Contains(r.Description, managedBy) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (c *client) addRule(ctx context.Context, r NATRule, managedBy string) error {
	base := strings.TrimSuffix(c.cfg.BaseURL, "/")
	u := base + "/api/firewall/d_nat/add_rule"
	desc := r.Description
	if desc == "" {
		desc = fmt.Sprintf("%s %s:%d->%s:%d", managedBy, r.Protocol, r.ExternalPort, r.TargetIP, r.TargetPort)
	}
	payload := rulePayload{}
	payload.Rule.Description = desc
	payload.Rule.Protocol = strings.ToUpper(r.Protocol)
	payload.Rule.Destination = fmt.Sprintf("0.0.0.0/%d", r.ExternalPort)
	payload.Rule.Target = fmt.Sprintf("%s:%d", r.TargetIP, r.TargetPort)
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.cfg.APIKey, c.cfg.APISecret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.cfg.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("opnsense d_nat add_rule: %s", resp.Status)
	}
	return nil
}

func (c *client) delRule(ctx context.Context, uuid string) error {
	base := strings.TrimSuffix(c.cfg.BaseURL, "/")
	u := base + "/api/firewall/d_nat/del_rule/" + url.PathEscape(uuid)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.cfg.APIKey, c.cfg.APISecret)
	resp, err := c.cfg.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("opnsense d_nat del_rule: %s", resp.Status)
	}
	return nil
}

func (c *client) applyFirewall(ctx context.Context) error {
	base := strings.TrimSuffix(c.cfg.BaseURL, "/")
	// Savepoint then apply to commit firewall changes.
	spURL := base + "/api/firewall/filter_base/savepoint"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spURL, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.cfg.APIKey, c.cfg.APISecret)
	resp, err := c.cfg.Client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("opnsense filter_base savepoint: %s", resp.Status)
	}
	// Apply (commit). Use revision from savepoint if needed; some versions accept apply without param.
	applyURL := base + "/api/firewall/filter_base/apply"
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, applyURL, nil)
	if err != nil {
		return err
	}
	req2.SetBasicAuth(c.cfg.APIKey, c.cfg.APISecret)
	resp2, err := c.cfg.Client.Do(req2)
	if err != nil {
		return err
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return fmt.Errorf("opnsense filter_base apply: %s", resp2.Status)
	}
	return nil
}
