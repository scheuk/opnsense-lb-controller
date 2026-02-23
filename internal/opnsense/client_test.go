package opnsense

import (
	"context"
	"testing"
)

func TestClient_ApplyNATRules_Stub(t *testing.T) {
	cfg := Config{
		BaseURL:   "https://opnsense.example.com",
		APIKey:    "key",
		APISecret: "secret",
	}
	cli := NewClient(cfg)
	ctx := context.Background()
	desired := []NATRule{
		{ExternalPort: 80, Protocol: "tcp", TargetIP: "10.0.0.1", TargetPort: 30080, Description: "managed"},
	}
	err := cli.ApplyNATRules(ctx, desired, "opnsense-lb-controller")
	if err != nil {
		t.Fatalf("ApplyNATRules: %v", err)
	}
}

func TestClient_ListNATRules_Stub(t *testing.T) {
	cli := NewClient(Config{})
	ctx := context.Background()
	rules, err := cli.ListNATRules(ctx)
	if err != nil {
		t.Fatalf("ListNATRules: %v", err)
	}
	if rules != nil {
		t.Errorf("ListNATRules: got %d rules, stub should return nil", len(rules))
	}
}
