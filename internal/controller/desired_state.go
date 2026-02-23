package controller

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/scheuk/opnsense-lb-controller/internal/opnsense"
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

// NodeIPResolver returns the internal IP for a node by name, or false if not found.
type NodeIPResolver func(nodeName string) (internalIP string, ok bool)

// ComputeDesiredState builds the desired NAT state from a Service and its Endpoints.
// vip is the virtual IP to use. For each LoadBalancer port, one NATRule is built with
// backends from Endpoints. When getNodeIP is set, EndpointAddress.NodeName is resolved
// to the node's internal IP for NodePort backends; otherwise addr.IP is used.
// Nil or empty Endpoints yield rules with empty Backends.
func ComputeDesiredState(vip string, svc *corev1.Service, endpoints *corev1.Endpoints, nodePort int32, getNodeIP NodeIPResolver) (*DesiredState, error) {
	if svc == nil {
		return nil, nil
	}
	state := &DesiredState{VIP: vip}
	var backendIPs []string
	if endpoints != nil {
		for _, sub := range endpoints.Subsets {
			for _, addr := range sub.Addresses {
				ip := addr.IP
				if addr.NodeName != nil && getNodeIP != nil {
					if nodeIP, ok := getNodeIP(*addr.NodeName); ok {
						ip = nodeIP
					}
				}
				if ip != "" {
					backendIPs = append(backendIPs, ip)
				}
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

// desiredStateToOPNsenseRules converts controller desired state to one opnsense.NATRule per backend.
// Description includes managedBy and serviceKey so rules are scoped per service.
func desiredStateToOPNsenseRules(state *DesiredState, managedBy, serviceKey string) []opnsense.NATRule {
	var out []opnsense.NATRule
	descPrefix := managedBy + " " + serviceKey + " " + state.VIP
	for _, r := range state.Rules {
		for _, b := range r.Backends {
			out = append(out, opnsense.NATRule{
				ExternalPort: int(r.ExternalPort),
				Protocol:     r.Protocol,
				TargetIP:     b.IP,
				TargetPort:   int(b.Port),
				Description:  descPrefix,
			})
		}
	}
	return out
}
