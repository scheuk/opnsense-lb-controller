/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestUpdateServiceLoadBalancerIngress(t *testing.T) {
	ctx := context.Background()

	t.Run("sets ingress to single VIP when vip is non-empty", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "test-svc"},
			Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeLoadBalancer,
			},
		}
		c := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(svc).
			WithStatusSubresource(svc).
			Build()

		err := UpdateServiceLoadBalancerIngress(ctx, c, svc, "192.0.2.1")
		if err != nil {
			t.Fatalf("UpdateServiceLoadBalancerIngress: %v", err)
		}

		var updated corev1.Service
		if err := c.Get(ctx, client.ObjectKeyFromObject(svc), &updated); err != nil {
			t.Fatalf("Get Service: %v", err)
		}
		if len(updated.Status.LoadBalancer.Ingress) != 1 {
			t.Fatalf("LoadBalancer.Ingress: got len %d, want 1", len(updated.Status.LoadBalancer.Ingress))
		}
		if updated.Status.LoadBalancer.Ingress[0].IP != "192.0.2.1" {
			t.Errorf("LoadBalancer.Ingress[0].IP: got %q, want %q", updated.Status.LoadBalancer.Ingress[0].IP, "192.0.2.1")
		}
	})

	t.Run("sets ingress to empty when vip is empty", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "test-svc"},
			Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeLoadBalancer,
			},
			Status: corev1.ServiceStatus{
				LoadBalancer: corev1.LoadBalancerStatus{
					Ingress: []corev1.LoadBalancerIngress{{IP: "192.0.2.1"}},
				},
			},
		}
		c := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(svc).
			WithStatusSubresource(svc).
			Build()

		err := UpdateServiceLoadBalancerIngress(ctx, c, svc, "")
		if err != nil {
			t.Fatalf("UpdateServiceLoadBalancerIngress: %v", err)
		}

		var updated corev1.Service
		if err := c.Get(ctx, client.ObjectKeyFromObject(svc), &updated); err != nil {
			t.Fatalf("Get Service: %v", err)
		}
		if len(updated.Status.LoadBalancer.Ingress) != 0 {
			t.Errorf("LoadBalancer.Ingress: got len %d, want 0", len(updated.Status.LoadBalancer.Ingress))
		}
	})
}
