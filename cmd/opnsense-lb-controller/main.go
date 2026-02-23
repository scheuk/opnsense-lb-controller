package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/scheuk/opnsense-lb-controller/internal/config"
	"github.com/scheuk/opnsense-lb-controller/internal/controller"
	"github.com/scheuk/opnsense-lb-controller/internal/opnsense"
)

func main() {
	kubeconfig := flag.String("kubeconfig", "", "Path to kubeconfig; empty for in-cluster")
	flag.Parse()

	cfg := config.LoadFromEnv()
	restCfg, err := loadKubeconfig(*kubeconfig)
	if err != nil {
		panic(err)
	}

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		panic(err)
	}

	apiKey := os.Getenv("OPNSENSE_API_KEY")
	apiSecret := os.Getenv("OPNSENSE_API_SECRET")
	if cfg.OPNsenseSecretName != "" {
		sec, err := clientset.CoreV1().Secrets(cfg.OPNsenseSecretNamespace).
			Get(context.Background(), cfg.OPNsenseSecretName, metav1.GetOptions{})
		if err != nil {
			panic(err)
		}
		if k := sec.Data["apiKey"]; len(k) > 0 {
			apiKey = string(k)
		}
		if s := sec.Data["apiSecret"]; len(s) > 0 {
			apiSecret = string(s)
		}
		if apiKey == "" {
			apiKey = string(sec.Data["key"])
			apiSecret = string(sec.Data["secret"])
		}
	}

	oc := opnsense.NewClient(opnsense.Config{
		BaseURL:   cfg.OPNsenseURL,
		APIKey:    apiKey,
		APISecret: apiSecret,
	})

	if cfg.SingleVIP == "" && len(cfg.VIPPool) == 0 {
		// Default for local/dev only; production should set VIP or VIP_POOL explicitly.
		cfg.SingleVIP = "192.0.2.1"
		_, _ = os.Stderr.WriteString(
			"opnsense-lb-controller: neither VIP nor VIP_POOL set; using default 192.0.2.1 (dev only). " +
				"Set VIP or VIP_POOL in production.\n")
	}
	vipAlloc := config.NewVIPAllocator(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		cancel()
	}()

	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme:                  scheme.Scheme,
		LeaderElection:          true,
		LeaderElectionNamespace: cfg.LeaseNamespace,
		LeaderElectionID:        cfg.LeaseName,
	})
	if err != nil {
		panic(err)
	}

	rec := controller.NewReconciler(
		mgr.GetClient(),
		mgr.GetEventRecorderFor("opnsense-lb-controller"), //nolint:staticcheck // SA1019: use GetEventRecorder later
		oc,
		vipAlloc,
		cfg.LoadBalancerClass,
		"opnsense-lb-controller",
		"opnsense.org/opnsense-lb",
	)

	endpointsEnqueue := handler.EnqueueRequestsFromMapFunc(
		func(ctx context.Context, obj client.Object) []reconcile.Request {
			return []reconcile.Request{
				{NamespacedName: types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}}}
		})

	servicesEnqueueForNode := func(cl client.Reader, loadBalancerClass string) handler.MapFunc {
		return func(ctx context.Context, obj client.Object) []reconcile.Request {
			var list corev1.ServiceList
			if err := cl.List(ctx, &list); err != nil {
				return nil
			}
			var reqs []reconcile.Request
			for i := range list.Items {
				svc := &list.Items[i]
				if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
					continue
				}
				if svc.Spec.LoadBalancerClass == nil || *svc.Spec.LoadBalancerClass != loadBalancerClass {
					continue
				}
				reqs = append(reqs, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: svc.Namespace, Name: svc.Name}})
			}
			return reqs
		}
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Service{}).
		WithEventFilter(controller.ServiceLoadBalancerClass(cfg.LoadBalancerClass)).
		Watches(&corev1.Endpoints{}, endpointsEnqueue). //nolint:staticcheck // SA1019: migrate to discoveryv1.EndpointSlice
		Watches(&corev1.Node{},
			handler.EnqueueRequestsFromMapFunc(servicesEnqueueForNode(mgr.GetClient(), cfg.LoadBalancerClass))).
		Complete(rec); err != nil {
		panic(err)
	}

	if err := mgr.Start(ctx); err != nil {
		panic(err)
	}
}

func loadKubeconfig(path string) (*rest.Config, error) {
	if path != "" {
		return clientcmd.BuildConfigFromFlags("", path)
	}
	return rest.InClusterConfig()
}
