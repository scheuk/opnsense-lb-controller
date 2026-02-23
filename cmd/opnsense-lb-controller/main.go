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

	"github.com/scheuk/opnsense-lb-controller/internal/controller"
	"github.com/scheuk/opnsense-lb-controller/internal/opnsense"
)

func main() {
	kubeconfig := flag.String("kubeconfig", "", "Path to kubeconfig; empty for in-cluster")
	leaseNS := flag.String("lease-namespace", "default", "Namespace for leader election lease")
	flag.Parse()

	cfg, err := loadKubeconfig(*kubeconfig)
	if err != nil {
		panic(err)
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		panic(err)
	}

	opnsenseURL := os.Getenv("OPNSENSE_URL")
	opnsenseKey := os.Getenv("OPNSENSE_API_KEY")
	opnsenseSecret := os.Getenv("OPNSENSE_API_SECRET")
	vip := os.Getenv("VIP")
	if vip == "" {
		vip = "192.0.2.1"
	}
	managedBy := "opnsense-lb-controller"

	oc := opnsense.NewClient(opnsense.Config{
		BaseURL:   opnsenseURL,
		APIKey:    opnsenseKey,
		APISecret: opnsenseSecret,
	})

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
			Name:      "opnsense-lb-controller",
			Namespace: *leaseNS,
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
				ctrl, err := controller.NewController(cfg, oc, vip, managedBy)
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
