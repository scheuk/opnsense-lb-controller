package controller

import (
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
// vip is the virtual IP to use. For each LoadBalancer port, one NATRule is built with
// backends from Endpoints (Addresses[].IP) and nodePort. Nil or empty Endpoints yield
// rules with empty Backends.
func ComputeDesiredState(vip string, svc *corev1.Service, endpoints *corev1.Endpoints, nodePort int32) (*DesiredState, error) {
	if svc == nil {
		return nil, nil
	}
	state := &DesiredState{VIP: vip}
	var backendIPs []string
	if endpoints != nil {
		for _, sub := range endpoints.Subsets {
			for _, addr := range sub.Addresses {
				backendIPs = append(backendIPs, addr.IP)
			}
		}
	}
	for _, p := range svc.Spec.Ports {
		np := p.NodePort
		if nodePort != 0 {
			np = nodePort
		}
		backends := make([]Backend, 0, len(backendIPs))
		for _, ip := range backendIPs {
			backends = append(backends, Backend{IP: ip, Port: np})
		}
		state.Rules = append(state.Rules, NATRule{
			ExternalPort: p.Port,
			Protocol:     string(p.Protocol),
			Backends:     backends,
		})
	}
	return state, nil
}
