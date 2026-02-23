package opnsense

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_ApplyNATRules_HTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/firewall/d_nat/search_rule" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"rows": []interface{}{}})
		case r.URL.Path == "/api/firewall/d_nat/add_rule" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/firewall/filter_base/savepoint" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"revision": "123"})
		case r.URL.Path == "/api/firewall/filter_base/apply" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := Config{
		BaseURL:   server.URL,
		APIKey:    "key",
		APISecret: "secret",
		Client:    server.Client(),
	}
	cli := NewClient(cfg)
	ctx := context.Background()
	desired := []NATRule{
		{ExternalPort: 80, Protocol: "tcp", TargetIP: "10.0.0.1", TargetPort: 30080, Description: "managed"},
	}
	err := cli.ApplyNATRules(ctx, desired, "opnsense-lb-controller", "default/my-svc")
	if err != nil {
		t.Fatalf("ApplyNATRules: %v", err)
	}
}

func TestClient_ListNATRules_HTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/firewall/d_nat/search_rule" && r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"rows": []map[string]string{
					{"uuid": "a1", "description": "opnsense-lb-controller rule1"},
					{"uuid": "b2", "description": "other"},
				},
			})
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := Config{
		BaseURL:   server.URL,
		APIKey:    "key",
		APISecret: "secret",
		Client:    server.Client(),
	}
	cli := NewClient(cfg)
	ctx := context.Background()
	rules, err := cli.ListNATRules(ctx)
	if err != nil {
		t.Fatalf("ListNATRules: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("ListNATRules: got %d rules, want 2", len(rules))
	}
	if rules[0].UUID != "a1" || rules[0].Description != "opnsense-lb-controller rule1" {
		t.Errorf("first rule: got uuid=%q desc=%q", rules[0].UUID, rules[0].Description)
	}
}

func TestClient_EnsureVIP_RemoveVIP_HTTP(t *testing.T) {
	var addVIPCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/interfaces/vip_settings/search_item" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"rows": []interface{}{}})
		case r.URL.Path == "/api/interfaces/vip_settings/add_item" && r.Method == http.MethodPost:
			addVIPCalled = true
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/interfaces/vip_settings/reconfigure" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := Config{BaseURL: server.URL, Client: server.Client()}
	cli := NewClient(cfg)
	ctx := context.Background()
	err := cli.EnsureVIP(ctx, "192.0.2.1")
	if err != nil {
		t.Fatalf("EnsureVIP: %v", err)
	}
	if !addVIPCalled {
		t.Error("EnsureVIP did not call add_item")
	}
}

// TestClient_ApplyNATRules_perServiceScoping verifies that ApplyNATRules only deletes rules
// for the given serviceKey (description contains both managedBy and serviceKey).
func TestClient_ApplyNATRules_perServiceScoping(t *testing.T) {
	var delUUIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/firewall/d_nat/search_rule" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"rows": []map[string]string{
					{"uuid": "u1", "description": "managed-by-controller ns/svc1 192.0.2.1"},
					{"uuid": "u2", "description": "managed-by-controller ns/svc2 192.0.2.2"},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/api/firewall/d_nat/del_rule/") && r.Method == http.MethodPost:
			uuid := strings.TrimPrefix(r.URL.Path, "/api/firewall/d_nat/del_rule/")
			delUUIDs = append(delUUIDs, uuid)
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/firewall/d_nat/add_rule" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/firewall/filter_base/savepoint" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"revision": "123"})
		case r.URL.Path == "/api/firewall/filter_base/apply" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := Config{BaseURL: server.URL, Client: server.Client()}
	cli := NewClient(cfg)
	ctx := context.Background()
	desired := []NATRule{
		{ExternalPort: 80, Protocol: "tcp", TargetIP: "10.0.0.1", TargetPort: 30080, Description: "managed-by-controller ns/svc1 192.0.2.1"},
	}
	err := cli.ApplyNATRules(ctx, desired, "managed-by-controller", "ns/svc1")
	if err != nil {
		t.Fatalf("ApplyNATRules: %v", err)
	}
	// Only u1 (ns/svc1) should be deleted, not u2 (ns/svc2).
	if len(delUUIDs) != 1 || delUUIDs[0] != "u1" {
		t.Errorf("ApplyNATRules deleted wrong rules: got delUUIDs=%v, want [u1]", delUUIDs)
	}
}
