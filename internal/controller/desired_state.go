package controller

import (
	"errors"

	corev1 "k8s.io/api/core/v1"
)

// DesiredState holds the desired NAT state for a Service: VIP and rules.
type DesiredState struct {
	VIP   string
	Rules []NATRule
}

// NATRule represents one port-forward rule (external port → backends).
type NATRule struct {
	ExternalPort int32
	Protocol     string
	Backends     []Backend
}

// Backend is a single backend target (IP and port).
type Backend struct {
	IP   string
	Port int32
}

// ComputeDesiredState builds the desired NAT state from a Service and its Endpoints.
// vip is the virtual IP to use; nodePort is used when resolving backends (e.g. NodePort).
// Returns nil, nil when not implemented (stub).
func ComputeDesiredState(svc *corev1.Service, endpoints *corev1.Endpoints, nodePort int32) (*DesiredState, error) {
	return nil, errors.New("not implemented")
}
