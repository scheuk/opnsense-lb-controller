package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

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
		sec, err := clientset.CoreV1().Secrets(cfg.OPNsenseSecretNamespace).Get(context.Background(), cfg.OPNsenseSecretName, metav1.GetOptions{})
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
		_, _ = os.Stderr.WriteString("opnsense-lb-controller: neither VIP nor VIP_POOL set; using default 192.0.2.1 (dev only). Set VIP or VIP_POOL in production.\n")
	}
	vipAlloc := config.NewVIPAllocator(cfg)
	managedBy := "opnsense-lb-controller"

	identity, _ := os.Hostname()
	if identity == "" {
		identity = "opnsense-lb-controller"
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		cancel()
	}()

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      cfg.LeaseName,
			Namespace: cfg.LeaseNamespace,
		},
		Client: clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: identity,
		},
	}

	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   15 * time.Second,
		RenewDeadline:   10 * time.Second,
		RetryPeriod:     2 * time.Second,
		ReleaseOnCancel: true,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				ctrl, err := controller.NewController(restCfg, oc, cfg.LoadBalancerClass, vipAlloc, managedBy)
				if err != nil {
					panic(err)
				}
				_ = ctrl.Run(ctx)
			},
			OnStoppedLeading: func() {
				cancel()
			},
		},
	})
}

func loadKubeconfig(path string) (*rest.Config, error) {
	if path != "" {
		return clientcmd.BuildConfigFromFlags("", path)
	}
	return rest.InClusterConfig()
}
