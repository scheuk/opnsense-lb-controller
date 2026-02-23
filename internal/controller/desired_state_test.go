package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestComputeDesiredState(t *testing.T) {
	const vip = "192.0.2.1"
	const nodePort = 30080

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "test-svc"},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeLoadBalancer,
			Ports: []corev1.ServicePort{
				{Port: 80, NodePort: nodePort, Protocol: corev1.ProtocolTCP},
			},
		},
	}
	ep := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "test-svc"},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}},
			},
		},
	}

	got, err := ComputeDesiredState(svc, ep, nodePort)
	if err != nil {
		t.Fatalf("ComputeDesiredState: %v", err)
	}
	if got == nil {
		t.Fatal("ComputeDesiredState returned nil state")
	}
	if got.VIP != vip {
		t.Errorf("VIP: got %q, want %q", got.VIP, vip)
	}
	if len(got.Rules) != 1 {
		t.Fatalf("Rules: got %d, want 1", len(got.Rules))
	}
	r := got.Rules[0]
	if r.ExternalPort != 80 {
		t.Errorf("ExternalPort: got %d, want 80", r.ExternalPort)
	}
	if r.Protocol != string(corev1.ProtocolTCP) {
		t.Errorf("Protocol: got %q, want tcp", r.Protocol)
	}
	if len(r.Backends) != 1 {
		t.Fatalf("Backends: got %d, want 1", len(r.Backends))
	}
	if r.Backends[0].IP != "10.0.0.1" || r.Backends[0].Port != nodePort {
		t.Errorf("Backend: got %s:%d, want 10.0.0.1:%d", r.Backends[0].IP, r.Backends[0].Port, nodePort)
	}
}
