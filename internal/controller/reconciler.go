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
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/scheuk/opnsense-lb-controller/internal/config"
	"github.com/scheuk/opnsense-lb-controller/internal/opnsense"
)

// Reconciler reconciles LoadBalancer Services with the configured LoadBalancerClass
// by syncing desired NAT state to OPNsense and updating Service status.
type Reconciler struct {
	Client            client.Client
	EventRecorder     record.EventRecorder
	OPNsense          opnsense.Client
	VIPAlloc          config.VIPAllocator
	LoadBalancerClass string
	ManagedBy         string
	FinalizerName     string
}

// NewReconciler returns a Reconciler with the given dependencies.
func NewReconciler(
	c client.Client,
	recorder record.EventRecorder,
	opnsenseClient opnsense.Client,
	vipAlloc config.VIPAllocator,
	loadBalancerClass string,
	managedBy string,
	finalizerName string,
) *Reconciler {
	return &Reconciler{
		Client:            c,
		EventRecorder:     recorder,
		OPNsense:          opnsenseClient,
		VIPAlloc:          vipAlloc,
		LoadBalancerClass: loadBalancerClass,
		ManagedBy:         managedBy,
		FinalizerName:     finalizerName,
	}
}

// Reconcile handles a Service key (namespace/name). It ensures NAT rules and VIP
// on OPNsense match the desired state and updates Service status. When a Service
// is deleted, cleanup runs and the finalizer is removed so the object can be deleted.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	key := req.Namespace + "/" + req.Name
	logger.Info("Reconciling Service", "key", key)

	var svc corev1.Service
	if err := r.Client.Get(ctx, req.NamespacedName, &svc); err != nil {
		if apierrors.IsNotFound(err) {
			r.cleanup(ctx, key)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if svc.DeletionTimestamp != nil {
		r.cleanup(ctx, key)
		if err := r.removeFinalizer(ctx, &svc); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if !r.isOurService(&svc) {
		r.cleanup(ctx, key)
		return ctrl.Result{}, nil
	}

	if added, err := r.addFinalizerIfMissing(ctx, &svc); err != nil {
		return ctrl.Result{}, err
	} else if added {
		return ctrl.Result{Requeue: true}, nil
	}

	vip := r.VIPAlloc.Allocate(key)
	if vip == "" {
		r.EventRecorder.Eventf(&svc, corev1.EventTypeWarning, "NoVIP", "no VIP available for %s", key)
		r.clearServiceStatus(ctx, req.NamespacedName)
		return ctrl.Result{}, nil
	}

	var endpoints corev1.Endpoints //nolint:staticcheck // SA1019: migrate to discoveryv1.EndpointSlice
	_ = r.Client.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: req.Name}, &endpoints)

	getNodeIP := func(nodeName string) (string, bool) {
		var node corev1.Node
		if err := r.Client.Get(ctx, types.NamespacedName{Name: nodeName}, &node); err != nil {
			return "", false
		}
		for _, a := range node.Status.Addresses {
			if a.Type == corev1.NodeInternalIP {
				return a.Address, true
			}
		}
		return "", false
	}

	state, err := ComputeDesiredState(vip, &svc, &endpoints, 0, getNodeIP)
	if err != nil {
		r.EventRecorder.Eventf(&svc, corev1.EventTypeWarning, "ComputeDesiredStateFailed", "ComputeDesiredState: %v", err)
		r.clearServiceStatus(ctx, req.NamespacedName)
		return ctrl.Result{Requeue: true}, nil
	}
	if state == nil {
		return ctrl.Result{}, nil
	}

	if err := r.OPNsense.EnsureVIP(ctx, state.VIP); err != nil {
		r.EventRecorder.Eventf(&svc, corev1.EventTypeWarning, "EnsureVIPFailed", "OPNsense EnsureVIP: %v", err)
		r.clearServiceStatus(ctx, req.NamespacedName)
		return ctrl.Result{Requeue: true}, nil
	}

	desiredRules := desiredStateToOPNsenseRules(state, r.ManagedBy, key)
	if err := r.OPNsense.ApplyNATRules(ctx, desiredRules, r.ManagedBy, key); err != nil {
		r.EventRecorder.Eventf(&svc, corev1.EventTypeWarning, "ApplyNATRulesFailed", "OPNsense ApplyNATRules: %v", err)
		r.clearServiceStatus(ctx, req.NamespacedName)
		return ctrl.Result{Requeue: true}, nil
	}

	var svcLatest corev1.Service
	if err := r.Client.Get(ctx, req.NamespacedName, &svcLatest); err != nil {
		return ctrl.Result{}, err
	}
	if err := UpdateServiceLoadBalancerIngress(ctx, r.Client, &svcLatest, vip); err != nil {
		r.EventRecorder.Eventf(&svcLatest, corev1.EventTypeWarning, "StatusPatchFailed", "patch Service status: %v", err)
		return ctrl.Result{Requeue: true}, nil
	}
	r.EventRecorder.Eventf(&svcLatest, corev1.EventTypeNormal, "Synced", "assigned VIP %s and synced NAT rules to OPNsense", state.VIP)
	logger.Info("Synced NAT and status for Service", "key", key)
	return ctrl.Result{}, nil
}

func (r *Reconciler) isOurService(svc *corev1.Service) bool {
	if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return false
	}
	if svc.Spec.LoadBalancerClass == nil {
		return false
	}
	return *svc.Spec.LoadBalancerClass == r.LoadBalancerClass
}

// addFinalizerIfMissing adds r.FinalizerName to svc.Finalizers if not present,
// patches the Service, and returns (true, nil) so the caller can requeue; returns (false, nil) if already present.
func (r *Reconciler) addFinalizerIfMissing(ctx context.Context, svc *corev1.Service) (bool, error) {
	if slices.Contains(svc.Finalizers, r.FinalizerName) {
		return false, nil
	}
	modified := svc.DeepCopy()
	modified.Finalizers = append(modified.Finalizers, r.FinalizerName)
	if err := r.Client.Patch(ctx, modified, client.MergeFrom(svc)); err != nil {
		return false, err
	}
	return true, nil
}

// removeFinalizer removes r.FinalizerName from svc.Finalizers if present and patches the Service.
func (r *Reconciler) removeFinalizer(ctx context.Context, svc *corev1.Service) error {
	var newFinalizers []string
	for _, f := range svc.Finalizers {
		if f != r.FinalizerName {
			newFinalizers = append(newFinalizers, f)
		}
	}
	if len(newFinalizers) == len(svc.Finalizers) {
		return nil
	}
	modified := svc.DeepCopy()
	modified.Finalizers = newFinalizers
	return r.Client.Patch(ctx, modified, client.MergeFrom(svc))
}

// clearServiceStatus re-fetches the Service and sets status.loadBalancer.ingress to [].
func (r *Reconciler) clearServiceStatus(ctx context.Context, nn types.NamespacedName) {
	var latest corev1.Service
	if err := r.Client.Get(ctx, nn, &latest); err != nil {
		return
	}
	_ = UpdateServiceLoadBalancerIngress(ctx, r.Client, &latest, "")
}

// cleanup removes this service's NAT rules from OPNsense, releases the VIP (for pool), and releases the allocator key.
// It does not remove the finalizer; the caller removes it when handling delete. Cleanup is idempotent.
func (r *Reconciler) cleanup(ctx context.Context, key string) {
	logger := log.FromContext(ctx)
	logger.Info("Cleaning up NAT/VIP for key", "key", key)
	vip := r.VIPAlloc.GetVIP(key)
	if err := r.OPNsense.ApplyNATRules(ctx, nil, r.ManagedBy, key); err != nil {
		logger.Error(err, "Cleanup ApplyNATRules failed", "key", key)
	}
	if vip != "" {
		if err := r.OPNsense.RemoveVIP(ctx, vip); err != nil {
			logger.Error(err, "Cleanup RemoveVIP failed", "key", key, "vip", vip)
		}
	}
	r.VIPAlloc.Release(key)
}
