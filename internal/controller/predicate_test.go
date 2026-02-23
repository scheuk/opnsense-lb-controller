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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestServiceLoadBalancerClass(t *testing.T) {
	const class = "opnsense.org/opnsense-lb"
	pred := ServiceLoadBalancerClass(class)

	t.Run("Service with type LoadBalancer and matching LoadBalancerClass returns true", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "lb"},
			Spec: corev1.ServiceSpec{
				Type:              corev1.ServiceTypeLoadBalancer,
				LoadBalancerClass: ptrString(class),
			},
		}
		e := event.CreateEvent{Object: svc}
		if !pred.Create(e) {
			t.Error("Create: expected true for LoadBalancer with matching LoadBalancerClass")
		}
		eDel := event.DeleteEvent{Object: svc}
		if !pred.Delete(eDel) {
			t.Error("Delete: expected true for LoadBalancer with matching LoadBalancerClass")
		}
		eUpd := event.UpdateEvent{ObjectOld: svc, ObjectNew: svc}
		if !pred.Update(eUpd) {
			t.Error("Update: expected true for LoadBalancer with matching LoadBalancerClass")
		}
	})

	t.Run("Service with type ClusterIP returns false", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "clusterip"},
			Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
			},
		}
		e := event.CreateEvent{Object: svc}
		if pred.Create(e) {
			t.Error("Create: expected false for ClusterIP")
		}
	})

	t.Run("LoadBalancer with different class returns false", func(t *testing.T) {
		other := "other.org/lb"
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "other"},
			Spec: corev1.ServiceSpec{
				Type:              corev1.ServiceTypeLoadBalancer,
				LoadBalancerClass: ptrString(other),
			},
		}
		e := event.CreateEvent{Object: svc}
		if pred.Create(e) {
			t.Error("Create: expected false for LoadBalancer with different class")
		}
	})

	t.Run("LoadBalancer with nil LoadBalancerClass returns false", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "no-class"},
			Spec: corev1.ServiceSpec{
				Type:              corev1.ServiceTypeLoadBalancer,
				LoadBalancerClass: nil,
			},
		}
		e := event.CreateEvent{Object: svc}
		if pred.Create(e) {
			t.Error("Create: expected false when LoadBalancerClass is nil")
		}
	})

	t.Run("nil object returns false", func(t *testing.T) {
		e := event.CreateEvent{Object: nil}
		if pred.Create(e) {
			t.Error("Create: expected false for nil object")
		}
	})
}

func ptrString(s string) *string { return &s }
