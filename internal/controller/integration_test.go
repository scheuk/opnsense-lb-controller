package controller

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	if err != nil {
		panic(err)
	}
	rec := NewReconciler(
		mgr.GetClient(),
		mgr.GetEventRecorderFor("opnsense-lb-controller"),
		mock,
		vipAlloc,
		"opnsense.org/opnsense-lb",
		"opnsense-lb-controller",
		"opnsense.org/opnsense-lb",
	)
	endpointsEnqueue := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}}}
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
				reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: svc.Namespace, Name: svc.Name}})
			}
			return reqs
		}
	}
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Service{}).
		WithEventFilter(ServiceLoadBalancerClass("opnsense.org/opnsense-lb")).
		Watches(&corev1.Endpoints{}, endpointsEnqueue).
		Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(servicesEnqueueForNode(mgr.GetClient(), "opnsense.org/opnsense-lb"))).
		Complete(rec); err != nil {
		panic(err)
	}
	go func() {
		_ = mgr.Start(ctx)
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

func TestIntegration_CreateService(t *testing.T) {
	requireEnvtest(t)
	_, client, mock, _ := testStartEnvtest()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ns := "test-create-svc"
	svcName := "lb1"
	nodeName := "node-create-svc"
	createNamespace(ctx, t, client, ns)
	createNode(ctx, t, client, nodeName, "192.0.2.10")
	createLoadBalancerService(ctx, t, client, ns, svcName, 30080)
	createEndpoints(ctx, t, client, ns, svcName, nodeName)

	ip := waitForIngressIP(ctx, t, client, ns, svcName, 10*time.Second)
	if ip != "192.0.2.1" && ip != "192.0.2.2" {
		t.Errorf("expected ingress IP 192.0.2.1 or 192.0.2.2, got %s", ip)
	}
	vips := mock.VIPs()
	hasVIP := slices.Contains(vips, ip)
	if !hasVIP {
		t.Errorf("mock VIPs expected to contain %s, got %v", ip, vips)
	}
	serviceKey := ns + "/" + svcName
	rules := mock.NATRulesFor(serviceKey)
	if len(rules) != 1 {
		t.Fatalf("expected 1 NAT rule for %s, got %d", serviceKey, len(rules))
	}
	if rules[0].ExternalPort != 80 || rules[0].TargetIP != "192.0.2.10" || rules[0].TargetPort != 30080 {
		t.Errorf("NAT rule: expected ExternalPort=80 TargetIP=192.0.2.10 TargetPort=30080, got %+v", rules[0])
	}
}

func TestIntegration_DeleteService_Cleanup(t *testing.T) {
	requireEnvtest(t)
	_, client, mock, _ := testStartEnvtest()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ns := "test-delete-svc"
	svcName := "lb2"
	nodeName := "node-delete-svc"
	serviceKey := ns + "/" + svcName
	createNamespace(ctx, t, client, ns)
	createNode(ctx, t, client, nodeName, "192.0.2.10")
	createLoadBalancerService(ctx, t, client, ns, svcName, 30081)
	createEndpoints(ctx, t, client, ns, svcName, nodeName)

	ip := waitForIngressIP(ctx, t, client, ns, svcName, 10*time.Second)
	if err := client.CoreV1().Services(ns).Delete(ctx, svcName, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete service: %v", err)
	}
	waitForNoNATRules(t, mock, serviceKey, 10*time.Second)
	vips := mock.VIPs()
	if slices.Contains(vips, ip) {
		t.Errorf("expected VIP %s to be released after delete, still in mock: %v", ip, vips)
	}
}

func TestIntegration_UpdatePorts(t *testing.T) {
	requireEnvtest(t)
	_, client, mock, _ := testStartEnvtest()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ns := "test-update-ports"
	svcName := "lb4"
	nodeName := "node-update-ports"
	serviceKey := ns + "/" + svcName
	createNamespace(ctx, t, client, ns)
	createNode(ctx, t, client, nodeName, "192.0.2.10")
	createLoadBalancerService(ctx, t, client, ns, svcName, 30082)
	createEndpoints(ctx, t, client, ns, svcName, nodeName)

	ip := waitForIngressIP(ctx, t, client, ns, svcName, 10*time.Second)
	svc, err := client.CoreV1().Services(ns).Get(ctx, svcName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get service: %v", err)
	}
	svc.Spec.Ports = append(svc.Spec.Ports, corev1.ServicePort{
		Name:       "https",
		Port:       443,
		TargetPort: intstr.FromInt32(8443),
		NodePort:   30444,
		Protocol:   corev1.ProtocolTCP,
	})
	if _, err := client.CoreV1().Services(ns).Update(ctx, svc, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update service: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		rules := mock.NATRulesFor(serviceKey)
		if len(rules) >= 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	rules := mock.NATRulesFor(serviceKey)
	if len(rules) != 2 {
		t.Fatalf("expected 2 NAT rules after adding port, got %d", len(rules))
	}
	svc, _ = client.CoreV1().Services(ns).Get(ctx, svcName, metav1.GetOptions{})
	if len(svc.Status.LoadBalancer.Ingress) == 0 || svc.Status.LoadBalancer.Ingress[0].IP != ip {
		t.Errorf("expected same VIP %s after update, got %v", ip, svc.Status.LoadBalancer.Ingress)
	}
}

func TestIntegration_ChangeLoadBalancerClass_Cleanup(t *testing.T) {
	t.Skip("Kubernetes API does not allow changing spec.loadBalancerClass after it is set; cleanup on class change is not testable via API")
	requireEnvtest(t)
	_, client, mock, _ := testStartEnvtest()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ns := "test-changeclass-svc"
	svcName := "lb3"
	serviceKey := ns + "/" + svcName
	createNamespace(ctx, t, client, ns)
	createNode(ctx, t, client, "node-1", "192.0.2.10")
	createLoadBalancerService(ctx, t, client, ns, svcName, 30083)
	createEndpoints(ctx, t, client, ns, svcName, "node-1")

	waitForIngressIP(ctx, t, client, ns, svcName, 10*time.Second)
	patch := `{"spec":{"loadBalancerClass":"other.org/lb"}}`
	if _, err := client.CoreV1().Services(ns).Patch(ctx, svcName, types.MergePatchType, []byte(patch), metav1.PatchOptions{}); err != nil {
		t.Fatalf("patch service: %v", err)
	}
	waitForNoNATRules(t, mock, serviceKey, 10*time.Second)
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

func waitForNoNATRules(t *testing.T, mock *FakeOPNsense, serviceKey string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(mock.NATRulesFor(serviceKey)) == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for no NAT rules for %s (within %v)", serviceKey, timeout)
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

func createLoadBalancerService(ctx context.Context, t *testing.T, client kubernetes.Interface, ns, name string, nodePort int32) *corev1.Service {
	t.Helper()
	if nodePort == 0 {
		nodePort = 30080
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.ServiceSpec{
			Type:              corev1.ServiceTypeLoadBalancer,
			LoadBalancerClass: ptr("opnsense.org/opnsense-lb"),
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080), NodePort: nodePort, Protocol: corev1.ProtocolTCP},
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
