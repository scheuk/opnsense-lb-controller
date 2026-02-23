package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/scheuk/opnsense-lb-controller/internal/controller"
	"github.com/scheuk/opnsense-lb-controller/internal/opnsense"
)

func main() {
	kubeconfig := flag.String("kubeconfig", "", "Path to kubeconfig; empty for in-cluster")
	flag.Parse()

	cfg, err := loadKubeconfig(*kubeconfig)
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

	ctrl, err := controller.NewController(cfg, oc, vip, managedBy)
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		cancel()
	}()

	if err := ctrl.Run(ctx); err != nil {
		panic(err)
	}
}

func loadKubeconfig(path string) (*rest.Config, error) {
	if path != "" {
		return clientcmd.BuildConfigFromFlags("", path)
	}
	return rest.InClusterConfig()
}
