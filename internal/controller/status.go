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

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// UpdateServiceLoadBalancerIngress patches the Service's status.loadBalancer.ingress
// to a single entry with the given VIP when vip is non-empty, or to an empty slice
// when vip is empty. Only .status.loadBalancer is changed.
func UpdateServiceLoadBalancerIngress(ctx context.Context, c client.Client, svc *corev1.Service, vip string) error {
	modified := svc.DeepCopy()
	if vip != "" {
		modified.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: vip}}
	} else {
		modified.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{}
	}
	return c.Status().Patch(ctx, modified, client.MergeFrom(svc))
}
