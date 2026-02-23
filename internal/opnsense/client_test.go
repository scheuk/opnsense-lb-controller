package opnsense

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	err := cli.ApplyNATRules(ctx, desired, "opnsense-lb-controller")
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

func TestClient_listManagedRules(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"rows": []map[string]string{
				{"uuid": "u1", "description": "managed-by-controller x"},
				{"uuid": "u2", "description": "other rule"},
			},
		})
	}))
	defer server.Close()

	cfg := Config{BaseURL: server.URL, Client: server.Client()}
	cli := NewClient(cfg).(*client)
	ctx := context.Background()
	rules, err := cli.listManagedRules(ctx, "managed-by-controller")
	if err != nil {
		t.Fatalf("listManagedRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("listManagedRules: got %d rules, want 1", len(rules))
	}
	if rules[0].UUID != "u1" {
		t.Errorf("uuid: got %q", rules[0].UUID)
	}
}
