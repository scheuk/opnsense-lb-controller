package config

import (
	"os"
	"strings"
)

// Config holds controller configuration from env or flags.
type Config struct {
	LoadBalancerClass       string
	OPNsenseURL             string
	OPNsenseSecretName      string
	OPNsenseSecretNamespace string
	// SingleVIP is used when set; otherwise VIPPool is used for allocation.
	SingleVIP string
	VIPPool   []string
	// LeaderElection
	LeaseNamespace string
	LeaseName      string
}

// LoadFromEnv populates Config from environment variables.
func LoadFromEnv() *Config {
	c := &Config{
		LoadBalancerClass:       getEnv("LOAD_BALANCER_CLASS", "opnsense.org/opnsense-lb"),
		OPNsenseURL:             os.Getenv("OPNSENSE_URL"),
		OPNsenseSecretName:      os.Getenv("OPNSENSE_SECRET_NAME"),
		OPNsenseSecretNamespace: getEnv("OPNSENSE_SECRET_NAMESPACE", "default"),
		SingleVIP:               os.Getenv("VIP"),
		LeaseNamespace:          getEnv("LEASE_NAMESPACE", "default"),
		LeaseName:               getEnv("LEASE_NAME", "opnsense-lb-controller"),
	}
	if pool := os.Getenv("VIP_POOL"); pool != "" {
		for _, s := range strings.Split(pool, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				c.VIPPool = append(c.VIPPool, s)
			}
		}
	}
	return c
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// VIPAllocator assigns a VIP for a Service. When SingleVIP is set, returns it for all;
// otherwise allocates from VIPPool per service key and releases on Release.
// GetVIP returns the currently allocated VIP for a service key, or "" if none.
type VIPAllocator interface {
	Allocate(serviceKey string) string
	Release(serviceKey string)
	GetVIP(serviceKey string) string
}

// NewVIPAllocator returns a VIPAllocator from config.
func NewVIPAllocator(cfg *Config) VIPAllocator {
	if cfg.SingleVIP != "" {
		return &singleVIP{vip: cfg.SingleVIP}
	}
	return newPoolAllocator(cfg.VIPPool)
}

type singleVIP struct{ vip string }

func (s *singleVIP) Allocate(string) string { return s.vip }
func (s *singleVIP) Release(string)         {}
// GetVIP returns "" for single-VIP so the controller does not call RemoveVIP (VIP is shared).
func (s *singleVIP) GetVIP(serviceKey string) string { return "" }

type poolAllocator struct {
	pool   []string
	used   map[string]string
	assign map[string]string
}

func newPoolAllocator(pool []string) *poolAllocator {
	return &poolAllocator{
		pool:   pool,
		used:   make(map[string]string),
		assign: make(map[string]string),
	}
}

func (p *poolAllocator) Allocate(serviceKey string) string {
	if vip, ok := p.assign[serviceKey]; ok {
		return vip
	}
	for _, ip := range p.pool {
		if p.used[ip] == "" {
			p.used[ip] = serviceKey
			p.assign[serviceKey] = ip
			return ip
		}
	}
	return ""
}

func (p *poolAllocator) Release(serviceKey string) {
	vip := p.assign[serviceKey]
	delete(p.assign, serviceKey)
	if vip != "" {
		delete(p.used, vip)
	}
}

func (p *poolAllocator) GetVIP(serviceKey string) string {
	return p.assign[serviceKey]
}
