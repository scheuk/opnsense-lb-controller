package controller

import (
	"context"
	"fmt"
	"sync"

	"github.com/scheuk/opnsense-lb-controller/internal/opnsense"
)

// FakeOPNsense is an in-memory implementation of opnsense.Client for integration tests.
// It records VIPs and NAT rules so tests can assert controller behavior.
type FakeOPNsense struct {
	mu    sync.RWMutex
	vips  map[string]struct{}
	rules []fakeNATRule
	uuid  int
}

type fakeNATRule struct {
	opnsense.NATRule
	serviceKey string
}

// NewFakeOPNsense returns a new FakeOPNsense ready for use.
func NewFakeOPNsense() *FakeOPNsense {
	return &FakeOPNsense{
		vips:  make(map[string]struct{}),
		rules: nil,
	}
}

// EnsureVIP records the VIP. Implements opnsense.Client.
func (f *FakeOPNsense) EnsureVIP(ctx context.Context, vip string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vips[vip] = struct{}{}
	return nil
}

// RemoveVIP removes the VIP. Implements opnsense.Client.
func (f *FakeOPNsense) RemoveVIP(ctx context.Context, vip string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.vips, vip)
	return nil
}

// ListNATRules returns all stored rules so the controller can diff. Implements opnsense.Client.
func (f *FakeOPNsense) ListNATRules(ctx context.Context) ([]opnsense.NATRule, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]opnsense.NATRule, len(f.rules))
	for i, r := range f.rules {
		out[i] = r.NATRule
	}
	return out, nil
}

// ApplyNATRules replaces rules for this serviceKey with desired. Implements opnsense.Client.
func (f *FakeOPNsense) ApplyNATRules(ctx context.Context, desired []opnsense.NATRule, managedBy, serviceKey string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Remove existing rules for this serviceKey
	n := 0
	for _, r := range f.rules {
		if r.serviceKey != serviceKey {
			f.rules[n] = r
			n++
		}
	}
	f.rules = f.rules[:n]
	// Append desired rules with UUID and serviceKey
	for _, r := range desired {
		f.uuid++
		f.rules = append(f.rules, fakeNATRule{
			NATRule: opnsense.NATRule{
				UUID:         fmt.Sprintf("fake-uuid-%d", f.uuid),
				ExternalPort: r.ExternalPort,
				Protocol:     r.Protocol,
				TargetIP:     r.TargetIP,
				TargetPort:   r.TargetPort,
				Description:  r.Description,
			},
			serviceKey: serviceKey,
		})
	}
	return nil
}

// VIPs returns a copy of the current set of VIPs (for assertions).
func (f *FakeOPNsense) VIPs() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]string, 0, len(f.vips))
	for v := range f.vips {
		out = append(out, v)
	}
	return out
}

// NATRulesFor returns rules that were applied for the given serviceKey (for assertions).
func (f *FakeOPNsense) NATRulesFor(serviceKey string) []opnsense.NATRule {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []opnsense.NATRule
	for _, r := range f.rules {
		if r.serviceKey == serviceKey {
			out = append(out, r.NATRule)
		}
	}
	return out
}
