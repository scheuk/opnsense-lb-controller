package controller

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/scheuk/opnsense-lb-controller/internal/config"
)

var (
	testEnv     *envtest.Environment
	envtestCfg  *rest.Config
	k8sClient   kubernetes.Interface
	envtestMock *FakeOPNsense
	startOnce   sync.Once
	startErr    error
	// envtestSkipped is true when envtest failed to start (e.g. missing KUBEBUILDER_ASSETS).
	// Integration tests should call requireEnvtest(t) and skip when true.
	envtestSkipped bool
)

func testStartEnvtest() (*rest.Config, kubernetes.Interface, *FakeOPNsense, error) {
	startOnce.Do(func() {
		testEnv = &envtest.Environment{}
		envtestCfg, startErr = testEnv.Start()
		if startErr != nil {
			if strings.Contains(startErr.Error(), "no such file or directory") || strings.Contains(startErr.Error(), "kubebuilder") {
				envtestSkipped = true
			}
			return
		}
		k8sClient, startErr = kubernetes.NewForConfig(envtestCfg)
		if startErr != nil {
			return
		}
		envtestMock = NewFakeOPNsense()
	})
	return envtestCfg, k8sClient, envtestMock, startErr
}

// requireEnvtest skips the test if envtest failed to start (e.g. KUBEBUILDER_ASSETS not set).
func requireEnvtest(t *testing.T) {
	t.Helper()
	if envtestSkipped {
		t.Skip("envtest binaries not available (set KUBEBUILDER_ASSETS or run: setup-envtest use -p path)")
	}
	_, _, _, err := testStartEnvtest()
	if err != nil {
		t.Fatalf("envtest: %v", err)
	}
}

func startController(ctx context.Context, cfg *rest.Config, mock *FakeOPNsense, vipAlloc config.VIPAllocator) {
	ctrl, err := NewController(cfg, mock, "opnsense.org/opnsense-lb", vipAlloc, "opnsense-lb-controller")
	if err != nil {
		panic(err)
	}
	go func() {
		_ = ctrl.Run(ctx)
	}()
}

func TestMain(m *testing.M) {
	cfg, _, mock, err := testStartEnvtest()
	if err != nil && !envtestSkipped {
		_, _ = os.Stderr.WriteString("envtest start failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	if !envtestSkipped && err == nil {
		vipAlloc := config.NewVIPAllocator(&config.Config{VIPPool: []string{"192.0.2.1", "192.0.2.2"}})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		startController(ctx, cfg, mock, vipAlloc)
		code := m.Run()
		cancel()
		if testEnv != nil {
			_ = testEnv.Stop()
		}
		os.Exit(code)
		return
	}
	os.Exit(m.Run())
}

func TestEnvtestStart(t *testing.T) {
	requireEnvtest(t)
	// If we get here, envtest started and controller is running.
}
