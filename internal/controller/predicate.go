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
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// ServiceLoadBalancerClass returns a predicate that filters core/v1 Service events:
// only Services with Spec.Type == LoadBalancer and Spec.LoadBalancerClass matching
// the given loadBalancerClass pass the filter. Used with WithEventFilter() so only
// Services with this LoadBalancerClass are reconciled.
func ServiceLoadBalancerClass(loadBalancerClass string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		svc, ok := obj.(*corev1.Service)
		if !ok || svc == nil {
			return false
		}
		if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			return false
		}
		if svc.Spec.LoadBalancerClass == nil {
			return false
		}
		return *svc.Spec.LoadBalancerClass == loadBalancerClass
	})
}
