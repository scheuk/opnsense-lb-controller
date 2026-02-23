package opnsense

import "context"

// NATRule represents one OPNsense DNAT rule (external port → target IP:port).
type NATRule struct {
	ExternalPort int
	Protocol     string
	TargetIP     string
	TargetPort   int
	Description  string
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
}

// NewClient returns a Client implementation. Stub: List returns nil, Apply is no-op.
func NewClient(cfg Config) Client {
	return &client{cfg: cfg}
}

type client struct {
	cfg Config
}

func (c *client) ListNATRules(ctx context.Context) ([]NATRule, error) {
	return nil, nil
}

func (c *client) ApplyNATRules(ctx context.Context, desired []NATRule, managedBy string) error {
	return nil
}
