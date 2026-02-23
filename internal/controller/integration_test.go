package controller

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
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

func ptr(s string) *string { return &s }

func waitForIngressIP(ctx context.Context, t *testing.T, client kubernetes.Interface, ns, svcName string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		svc, err := client.CoreV1().Services(ns).Get(ctx, svcName, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get service: %v", err)
		}
		if len(svc.Status.LoadBalancer.Ingress) > 0 && svc.Status.LoadBalancer.Ingress[0].IP != "" {
			return svc.Status.LoadBalancer.Ingress[0].IP
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for status.loadBalancer.ingress (within %v)", timeout)
	return ""
}

func createNamespace(ctx context.Context, t *testing.T, client kubernetes.Interface, name string) *corev1.Namespace {
	t.Helper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	ns, err := client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	return ns
}

func createNode(ctx context.Context, t *testing.T, client kubernetes.Interface, name, internalIP string) *corev1.Node {
	t.Helper()
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: internalIP},
			},
		},
	}
	node, err := client.CoreV1().Nodes().Create(ctx, node, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	return node
}

func createLoadBalancerService(ctx context.Context, t *testing.T, client kubernetes.Interface, ns, name string) *corev1.Service {
	t.Helper()
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeLoadBalancer,
			LoadBalancerClass: ptr("opnsense.org/opnsense-lb"),
			Ports: []corev1.ServicePort{
				{Port: 80, TargetPort: intstr.FromInt32(8080), NodePort: 30080, Protocol: corev1.ProtocolTCP},
			},
		},
	}
	svc, err := client.CoreV1().Services(ns).Create(ctx, svc, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create service: %v", err)
	}
	return svc
}

func createEndpoints(ctx context.Context, t *testing.T, client kubernetes.Interface, ns, name, nodeName string) *corev1.Endpoints {
	t.Helper()
	ep := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: "10.0.0.1", NodeName: ptr(nodeName)},
				},
				Ports: []corev1.EndpointPort{{Port: 8080, Protocol: corev1.ProtocolTCP}},
			},
		},
	}
	ep, err := client.CoreV1().Endpoints(ns).Create(ctx, ep, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create endpoints: %v", err)
	}
	return ep
}
