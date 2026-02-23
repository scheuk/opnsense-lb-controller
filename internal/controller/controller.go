package controller

import (
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	listerscorev1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"

	"github.com/scheuk/opnsense-lb-controller/internal/config"
	"github.com/scheuk/opnsense-lb-controller/internal/opnsense"
)

// Controller reconciles LoadBalancer Services with our loadBalancerClass by
// syncing desired NAT state to OPNsense and updating Service status.
type Controller struct {
	clientset         kubernetes.Interface
	opnsense          opnsense.Client
	svcLister         listerscorev1.ServiceLister
	epLister          listerscorev1.EndpointsLister
	nodeLister        listerscorev1.NodeLister
	svcInformer       cache.SharedIndexInformer
	epInformer        cache.SharedIndexInformer
	nodeInformer      cache.SharedIndexInformer
	queue             workqueue.RateLimitingInterface
	loadBalancerClass string
	vipAlloc          config.VIPAllocator
	managedBy         string
	recorder          record.EventRecorder
}

// NewController creates a controller that uses the given clientset and OPNsense client.
func NewController(cfg *rest.Config, opnsenseClient opnsense.Client, loadBalancerClass string, vipAlloc config.VIPAllocator, managedBy string) (*Controller, error) {
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	eventBroadcaster := record.NewBroadcaster()
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "opnsense-lb-controller"})
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: clientset.CoreV1().Events(metav1.NamespaceAll)})

	informerFactory := informers.NewSharedInformerFactory(clientset, 0)
	svcInformer := informerFactory.Core().V1().Services().Informer()
	epInformer := informerFactory.Core().V1().Endpoints().Informer()
	nodeInformer := informerFactory.Core().V1().Nodes().Informer()
	queue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "opnsense-lb")

	c := &Controller{
		clientset:         clientset,
		opnsense:          opnsenseClient,
		svcLister:         informerFactory.Core().V1().Services().Lister(),
		epLister:          informerFactory.Core().V1().Endpoints().Lister(),
		nodeLister:        informerFactory.Core().V1().Nodes().Lister(),
		svcInformer:       svcInformer,
		epInformer:        epInformer,
		nodeInformer:      nodeInformer,
		queue:             queue,
		loadBalancerClass: loadBalancerClass,
		vipAlloc:          vipAlloc,
		managedBy:         managedBy,
		recorder:          recorder,
	}

	enqueue := func(obj interface{}) {
		key, err := cache.MetaNamespaceKeyFunc(obj)
		if err != nil {
			runtime.HandleError(err)
			return
		}
		queue.Add(key)
	}

	_, _ = svcInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if c.isOurService(obj.(*corev1.Service)) {
				enqueue(obj)
			}
		},
		UpdateFunc: func(_, newObj interface{}) {
			if c.isOurService(newObj.(*corev1.Service)) {
				enqueue(newObj)
			}
		},
		DeleteFunc: enqueue,
	})
	_, _ = epInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.enqueueEndpoints(obj) },
		UpdateFunc: func(_, newObj interface{}) { c.enqueueEndpoints(newObj) },
		DeleteFunc: func(obj interface{}) { c.enqueueEndpoints(obj) },
	})
	_, _ = nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(interface{}) { c.enqueueAllManagedServices() },
		UpdateFunc: func(_, _ interface{}) { c.enqueueAllManagedServices() },
		DeleteFunc: func(interface{}) { c.enqueueAllManagedServices() },
	})

	return c, nil
}

func (c *Controller) enqueueAllManagedServices() {
	svcs, err := c.svcLister.List(labels.Everything())
	if err != nil {
		runtime.HandleError(err)
		return
	}
	for _, svc := range svcs {
		if c.isOurService(svc) {
			key := svc.Namespace + "/" + svc.Name
			c.queue.Add(key)
		}
	}
}

func (c *Controller) isOurService(svc *corev1.Service) bool {
	if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return false
	}
	if svc.Spec.LoadBalancerClass == nil {
		return false
	}
	return *svc.Spec.LoadBalancerClass == c.loadBalancerClass
}

func (c *Controller) enqueueEndpoints(obj interface{}) {
	ep, ok := obj.(*corev1.Endpoints)
	if !ok {
		return
	}
	key := ep.Namespace + "/" + ep.Name
	c.queue.Add(key)
}

// Run starts informers and the workqueue loop. It blocks until ctx is cancelled.
func (c *Controller) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.svcInformer.Run(ctx.Done())
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.epInformer.Run(ctx.Done())
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.nodeInformer.Run(ctx.Done())
	}()

	if !cache.WaitForCacheSync(ctx.Done(), c.svcInformer.HasSynced, c.epInformer.HasSynced, c.nodeInformer.HasSynced) {
		return fmt.Errorf("cache sync failed")
	}

	go func() {
		<-ctx.Done()
		c.queue.ShutDown()
	}()

	for c.processNext(ctx) {
	}
	return nil
}

func (c *Controller) processNext(ctx context.Context) bool {
	key, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(key)
	err := c.reconcile(ctx, key.(string))
	if err != nil {
		runtime.HandleError(err)
		c.queue.AddRateLimited(key)
		return true
	}
	c.queue.Forget(key)
	return true
}

func (c *Controller) reconcile(ctx context.Context, key string) error {
	ns, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}
	svc, err := c.svcLister.Services(ns).Get(name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			c.cleanupService(ctx, key)
			return nil
		}
		return err
	}
	if !c.isOurService(svc) {
		c.cleanupService(ctx, key)
		return nil
	}

	vip := c.vipAlloc.Allocate(key)
	if vip == "" {
		c.recorder.Eventf(svc, corev1.EventTypeWarning, "NoVIP", "no VIP available for %s", key)
		return fmt.Errorf("no VIP available for %s", key)
	}

	ep, err := c.epLister.Endpoints(ns).Get(name)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	getNodeIP := func(nodeName string) (string, bool) {
		node, err := c.nodeLister.Get(nodeName)
		if err != nil {
			return "", false
		}
		for _, a := range node.Status.Addresses {
			if a.Type == corev1.NodeInternalIP {
				return a.Address, true
			}
		}
		return "", false
	}
	state, err := ComputeDesiredState(vip, svc, ep, 0, getNodeIP)
	if err != nil {
		return err
	}
	if state == nil {
		return nil
	}

	if err := c.opnsense.EnsureVIP(ctx, state.VIP); err != nil {
		c.recorder.Eventf(svc, corev1.EventTypeWarning, "EnsureVIPFailed", "OPNsense EnsureVIP: %v", err)
		return err
	}

	desiredRules := desiredStateToOPNsenseRules(state, c.managedBy, key)
	if err := c.opnsense.ApplyNATRules(ctx, desiredRules, c.managedBy, key); err != nil {
		c.recorder.Eventf(svc, corev1.EventTypeWarning, "ApplyNATRulesFailed", "OPNsense ApplyNATRules: %v", err)
		return err
	}

	// Patch Service status: .status.loadBalancer.ingress = [{ ip: vip }]
	statusPatch := []byte(fmt.Sprintf(`{"status":{"loadBalancer":{"ingress":[{"ip":%q}]}}}`, state.VIP))
	_, err = c.clientset.CoreV1().Services(ns).Patch(ctx, name, types.MergePatchType, statusPatch, metav1.PatchOptions{}, "status")
	if err != nil {
		c.recorder.Eventf(svc, corev1.EventTypeWarning, "StatusPatchFailed", "patch Service status: %v", err)
		return err
	}
	c.recorder.Eventf(svc, corev1.EventTypeNormal, "Synced", "assigned VIP %s and synced NAT rules to OPNsense", state.VIP)
	return nil
}

// cleanupService removes this service's NAT rules from OPNsense, releases the VIP (for pool), and releases the allocator key.
func (c *Controller) cleanupService(ctx context.Context, key string) {
	vip := c.vipAlloc.GetVIP(key)
	// Delete this service's NAT rules (empty desired = delete only).
	if err := c.opnsense.ApplyNATRules(ctx, nil, c.managedBy, key); err != nil {
		runtime.HandleError(fmt.Errorf("cleanup ApplyNATRules for %s: %w", key, err))
	}
	if vip != "" {
		if err := c.opnsense.RemoveVIP(ctx, vip); err != nil {
			runtime.HandleError(fmt.Errorf("cleanup RemoveVIP %s for %s: %w", vip, key, err))
		}
	}
	c.vipAlloc.Release(key)
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
