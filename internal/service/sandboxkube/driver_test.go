package sandboxkube

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/sandboxruntime"
	"github.com/rakunlabs/at/internal/service/container"
)

func testOptions() config.KubernetesSandbox {
	return config.KubernetesSandbox{Namespace: "at-sandboxes", DeploymentID: "test", HelperImage: "example/helper:v1", SingleReplica: true, NetworkPolicyEnforced: true, PodPidsLimit: 128}
}

func testDriver(t *testing.T) (*Driver, *fake.Clientset) {
	t.Helper()
	opts, err := normalizeOptions(testOptions())
	if err != nil {
		t.Fatal(err)
	}
	client := fake.NewClientset()
	d := &Driver{client: client, opts: opts}
	client.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		pod := action.(ktesting.CreateAction).GetObject().(*corev1.Pod).DeepCopy()
		pod.UID = types.UID("uid-" + pod.Name)
		pod.Status = corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Name: sandboxContainer, Ready: true}}}
		if err := client.Tracker().Create(corev1.SchemeGroupVersion.WithResource("pods"), pod, opts.Namespace); err != nil {
			return true, nil, err
		}
		return true, pod, nil
	})
	d.stream = func(context.Context, string, []string, remotecommand.StreamOptions) error { return nil }
	return d, client
}

func testConfig() container.Config {
	return container.Config{Enabled: true, Image: "debian:13.7-slim", CPU: "2", Memory: "4g", PersistentVolume: true, KeepAlive: true, RetainWhenIdle: true, PidsLimit: 256, DiskLimitBytes: 20 << 30, Network: true, CapAdd: container.PackageManagerCapabilities}
}

func TestKubernetesOptionsFailClosed(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*config.KubernetesSandbox)
	}{
		{"multi replica", func(o *config.KubernetesSandbox) { o.SingleReplica = false }},
		{"unverified network", func(o *config.KubernetesSandbox) { o.NetworkPolicyEnforced = false }},
		{"missing PID ceiling", func(o *config.KubernetesSandbox) { o.PodPidsLimit = 0 }},
		{"system namespace", func(o *config.KubernetesSandbox) { o.Namespace = "kube-system" }},
		{"missing deployment", func(o *config.KubernetesSandbox) { o.DeploymentID = "" }},
		{"missing helper", func(o *config.KubernetesSandbox) { o.HelperImage = "" }},
		{"invalid home mode", func(o *config.KubernetesSandbox) { o.HomeAccessMode = "ReadOnlyMany" }},
		{"invalid size", func(o *config.KubernetesSandbox) { o.HomeSize = "-1Gi" }},
		{"invalid network", func(o *config.KubernetesSandbox) { o.BlockedCIDRs = []string{"garbage"} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			o := testOptions()
			tt.change(&o)
			if _, err := normalizeOptions(o); err == nil {
				t.Fatal("unsafe configuration admitted")
			}
		})
	}
}

func TestKubernetesCreateStopAndPurge(t *testing.T) {
	ctx := context.Background()
	d, client := testDriver(t)
	cfg := testConfig()
	handle, err := d.Create(ctx, "scope", cfg)
	if err != nil {
		t.Fatal(err)
	}
	again, err := d.Create(ctx, "scope", cfg)
	if err != nil || again != handle {
		t.Fatalf("reuse: %s %v", again, err)
	}
	pod, err := d.pod(ctx, handle)
	if err != nil {
		t.Fatal(err)
	}
	if *pod.Spec.AutomountServiceAccountToken || *pod.Spec.EnableServiceLinks || pod.Spec.HostNetwork || pod.Spec.HostPID {
		t.Fatal("sandbox inherited cluster access")
	}
	user := pod.Spec.Containers[0]
	if *user.SecurityContext.AllowPrivilegeEscalation || *user.SecurityContext.Privileged || user.SecurityContext.Capabilities.Drop[0] != "ALL" {
		t.Fatal("sandbox security contract drifted")
	}
	if user.Resources.Limits.Memory().Value() != 4<<30 {
		t.Fatal("Docker memory suffix was not normalized")
	}
	if pod.Spec.InitContainers[0].Command[0] != "/at-sandbox" || user.Command[0] != sandboxruntime.Launcher {
		t.Fatal("Python-less helper setup was lost")
	}
	policies, _ := client.NetworkingV1().NetworkPolicies(d.opts.Namespace).List(ctx, metav1.ListOptions{})
	if len(policies.Items) != 1 || len(policies.Items[0].Spec.Ingress) != 0 || len(policies.Items[0].Spec.Egress) != 2 {
		t.Fatal("network policy missing")
	}
	if err := d.Stop(ctx, handle); err != nil {
		t.Fatal(err)
	}
	if d.Running(ctx, handle) {
		t.Fatal("Stop left the pod running")
	}
	if _, err := client.CoreV1().PersistentVolumeClaims(d.opts.Namespace).Get(ctx, d.name("data", "scope"), metav1.GetOptions{}); err != nil {
		t.Fatal("Stop deleted persistent data")
	}
	if err := d.Purge(ctx, "scope"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().PersistentVolumeClaims(d.opts.Namespace).Get(ctx, d.name("data", "scope"), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("Purge did not delete data: %v", err)
	}
	if err := d.Purge(ctx, "scope"); err != nil {
		t.Fatalf("purge must be idempotent: %v", err)
	}
}

func TestKubernetesStopScopeWithoutLocalTracking(t *testing.T) {
	ctx := t.Context()
	d, client := testDriver(t)
	handle, err := d.Create(ctx, "scope", testConfig())
	if err != nil {
		t.Fatal(err)
	}
	// A new Manager has no handle cache; the driver resolves and verifies the
	// persistent workload rather than treating it as already stopped.
	m := container.NewWithDriver(d)
	if err := m.StopContainer(ctx, "scope"); err != nil {
		t.Fatal(err)
	}
	if d.Running(ctx, handle) {
		t.Fatal("untracked pod left running")
	}
	if _, err := client.CoreV1().PersistentVolumeClaims(d.opts.Namespace).Get(ctx, d.name("data", "scope"), metav1.GetOptions{}); err != nil {
		t.Fatal("scope stop deleted workspace storage")
	}
	if err := m.StopContainer(ctx, "scope"); err != nil {
		t.Fatalf("missing scope stop not idempotent: %v", err)
	}
}

func TestKubernetesRefusesForeignAndStaleResources(t *testing.T) {
	ctx := context.Background()
	d, client := testDriver(t)
	cfg := testConfig()
	pod, _ := d.podSpec("scope", cfg)
	pod.Labels[managedLabel] = "another-installation"
	pod.UID = "foreign"
	if err := client.Tracker().Create(corev1.SchemeGroupVersion.WithResource("pods"), pod, d.opts.Namespace); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Create(ctx, "scope", cfg); err == nil {
		t.Fatal("foreign pod adopted")
	}
	if err := d.Purge(ctx, "scope"); err == nil {
		t.Fatal("foreign scope purged")
	}
	if err := d.Remove(ctx, podHandle(pod)); err == nil {
		t.Fatal("foreign pod deleted")
	}
	owned, err := d.Create(ctx, "owned", cfg)
	if err != nil {
		t.Fatal(err)
	}
	name, _, _ := splitHandle(owned)
	if err := d.Remove(ctx, name+"/old-uid"); err == nil {
		t.Fatal("stale handle deleted a replacement")
	}
	if !d.Running(ctx, owned) {
		t.Fatal("refusal still deleted the pod")
	}
	pvc := d.pvc("foreign-pvc", false, cfg)
	pvc.Labels[managedLabel] = "foreign"
	_, _ = client.CoreV1().PersistentVolumeClaims(d.opts.Namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if _, err := d.Create(ctx, "foreign-pvc", cfg); err == nil {
		t.Fatal("foreign PVC adopted")
	}
}

func TestKubernetesHomeIsolationAndAccessModes(t *testing.T) {
	ctx := context.Background()
	d, client := testDriver(t)
	cfg := testConfig()
	cfg.HomeScope = "account-a"
	cfg.HomePath = "/root"
	a, err := d.Create(ctx, "ws-a", cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.Create(ctx, "ws-b", cfg)
	if err != nil {
		t.Fatal(err)
	}
	other := cfg
	other.HomeScope = "account-b"
	c, err := d.Create(ctx, "ws-c", other)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveHome(ctx, "account-a"); err != nil {
		t.Fatal(err)
	}
	if d.Running(ctx, a) || d.Running(ctx, b) || !d.Running(ctx, c) {
		t.Fatal("home reset must affect only its account")
	}
	if _, err := client.CoreV1().PersistentVolumeClaims(d.opts.Namespace).Get(ctx, d.name("data", "ws-a"), metav1.GetOptions{}); err != nil {
		t.Fatal("home reset removed workspace data")
	}
	d.opts.HomeAccessMode = string(corev1.ReadWriteOnce)
	if _, err := d.Create(ctx, "ws-d", other); err == nil || !strings.Contains(err.Error(), "already mounted") {
		t.Fatalf("RWO concurrent home must fail clearly: %v", err)
	}
}

func TestKubernetesExecExitAndCancellation(t *testing.T) {
	ctx := context.Background()
	d, _ := testDriver(t)
	handle, err := d.Create(ctx, "scope", testConfig())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	d.stream = func(_ context.Context, _ string, argv []string, o remotecommand.StreamOptions) error {
		if argv[1] != "run" || argv[3] != "/workspace/project" || argv[len(argv)-1] != "; rm -rf /" {
			t.Fatalf("argv interpolated or launch omitted: %v", argv)
		}
		_, _ = io.WriteString(o.Stdout, "answer")
		return utilexec.CodeExitError{Err: errors.New("exit"), Code: 7}
	}
	code, err := d.Exec(ctx, handle, container.ExecRequest{WorkDir: "/workspace/project", Argv: []string{"echo", "; rm -rf /"}, Stdout: &out})
	if err != nil || code != 7 || out.String() != "answer" {
		t.Fatalf("exit contract: %d %v %s", code, err, out.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls := []string{}
	d.stream = func(streamCtx context.Context, _ string, argv []string, _ remotecommand.StreamOptions) error {
		calls = append(calls, argv[1])
		if argv[1] == "run" {
			cancel()
			return context.Canceled
		}
		if streamCtx.Err() != nil {
			t.Fatal("remote cancellation inherited expired context")
		}
		return nil
	}
	if _, err := d.Exec(ctx, handle, container.ExecRequest{Argv: []string{"sleep", "60"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation missing: %v", err)
	}
	if strings.Join(calls, ",") != "run,cancel" {
		t.Fatalf("remote process was not cancelled: %v", calls)
	}
}

func TestKubernetesNetworkDisabledAndPIDBounds(t *testing.T) {
	d, _ := testDriver(t)
	p := d.policy("scope", false)
	if len(p.Spec.Ingress) != 0 || len(p.Spec.Egress) != 0 || len(p.Spec.PolicyTypes) != 2 {
		t.Fatal("Network false must deny ingress and egress")
	}
	cfg := testConfig()
	cfg.PidsLimit = 64
	if _, err := d.Create(context.Background(), "scope", cfg); err == nil {
		t.Fatal("requested PID limit silently weakened")
	}
	caps := d.Capabilities()
	if caps.PreservesRootOnStop || caps.MultiReplica || caps.FileHelperPath == "" {
		t.Fatal("unsupported guarantees advertised")
	}
}

func TestKubernetesDeletionAndPolicyFailures(t *testing.T) {
	d, client := testDriver(t)
	ctx := context.Background()
	handle, err := d.Create(ctx, "scope", testConfig())
	if err != nil {
		t.Fatal(err)
	}
	client.PrependReactor("delete", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("API unavailable")
	})
	if err := d.Stop(ctx, handle); err == nil {
		t.Fatal("failed stop reported success")
	}
	if err := d.Purge(ctx, "scope"); err == nil {
		t.Fatal("purge ignored failed pod deletion")
	}
	if _, err := client.CoreV1().PersistentVolumeClaims(d.opts.Namespace).Get(ctx, d.name("data", "scope"), metav1.GetOptions{}); err != nil {
		t.Fatal("failed purge removed data")
	}

	d, client = testDriver(t)
	client.PrependReactor("create", "networkpolicies", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("policy denied")
	})
	if _, err := d.Create(ctx, "scope", testConfig()); err == nil {
		t.Fatal("pod created without its network policy")
	}
	pods, _ := client.CoreV1().Pods(d.opts.Namespace).List(ctx, metav1.ListOptions{})
	if len(pods.Items) != 0 {
		t.Fatal("policy failure still ran user code")
	}
}

func TestKubernetesPVCDeletionWaitsForCompletion(t *testing.T) {
	d, client := testDriver(t)
	if err := d.ensurePVC(context.Background(), "scope", false, testConfig()); err != nil {
		t.Fatal(err)
	}
	client.PrependReactor("delete", "persistentvolumeclaims", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, nil // API accepted but a finalizer keeps the PVC.
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := d.deletePVC(ctx, "scope", false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pending PVC deletion reported success: %v", err)
	}
}

func TestKubernetesHomeCannotMaskHelpers(t *testing.T) {
	d, _ := testDriver(t)
	for _, p := range []string{"/opt", sandboxruntime.Directory, sandboxruntime.Directory + "/nested"} {
		cfg := testConfig()
		cfg.HomeScope, cfg.HomePath = "account", p
		if _, err := d.Create(context.Background(), "scope", cfg); err == nil {
			t.Fatalf("home masked helpers: %s", p)
		}
	}
}

func TestKubernetesTerminalResizeAndClose(t *testing.T) {
	d, _ := testDriver(t)
	handle, err := d.Create(context.Background(), "scope", testConfig())
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	cancelled := make(chan struct{})
	d.stream = func(ctx context.Context, _ string, argv []string, opts remotecommand.StreamOptions) error {
		if argv[1] == "cancel" {
			close(cancelled)
			return nil
		}
		if !opts.Tty || opts.Stderr != nil || argv[1] != "run-shell" {
			t.Errorf("incorrect terminal stream: %v", argv)
		}
		size := opts.TerminalSizeQueue.Next()
		if size == nil || size.Width != 80 || size.Height != 24 {
			t.Errorf("initial resize missing: %v", size)
		}
		close(started)
		<-ctx.Done()
		if opts.TerminalSizeQueue.Next() != nil {
			t.Error("resize queue did not end on close")
		}
		return ctx.Err()
	}
	term, err := d.Attach(context.Background(), handle, "/workspace", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("terminal did not start")
	}
	if err := term.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("terminal close did not cancel remote command")
	}
}

func TestKubernetesReuseRepairsDependencies(t *testing.T) {
	d, client := testDriver(t)
	ctx := context.Background()
	cfg := testConfig()
	handle, err := d.Create(ctx, "scope", cfg)
	if err != nil {
		t.Fatal(err)
	}
	policyName := d.name("network", "scope")
	if err := client.NetworkingV1().NetworkPolicies(d.opts.Namespace).Delete(ctx, policyName, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	again, err := d.Create(ctx, "scope", cfg)
	if err != nil || again != handle {
		t.Fatalf("reuse: %s %v", again, err)
	}
	if _, err := client.NetworkingV1().NetworkPolicies(d.opts.Namespace).Get(ctx, policyName, metav1.GetOptions{}); err != nil {
		t.Fatal("reusing a running pod did not restore its policy")
	}
	policy, _ := client.NetworkingV1().NetworkPolicies(d.opts.Namespace).Get(ctx, policyName, metav1.GetOptions{})
	policy.Labels[managedLabel] = "foreign"
	if _, err := client.NetworkingV1().NetworkPolicies(d.opts.Namespace).Update(ctx, policy, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Create(ctx, "scope", cfg); err == nil {
		t.Fatal("reuse adopted a foreign policy")
	}
}

func TestKubernetesRemovalDoesNotForceDelete(t *testing.T) {
	d, client := testDriver(t)
	ctx := context.Background()
	handle, err := d.Create(ctx, "scope", testConfig())
	if err != nil {
		t.Fatal(err)
	}
	client.PrependReactor("delete", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		opts := action.(ktesting.DeleteAction).GetDeleteOptions()
		if opts.GracePeriodSeconds != nil && *opts.GracePeriodSeconds == 0 {
			t.Error("forced deletion can leave a sandbox running after its API object disappears")
		}
		if opts.Preconditions == nil || opts.Preconditions.UID == nil {
			t.Error("deletion lost UID precondition")
		}
		return false, nil, nil
	})
	if err := d.Remove(ctx, handle); err != nil {
		t.Fatal(err)
	}
}

func TestKubernetesTerminalCloseWaitsForRemoteCancellation(t *testing.T) {
	d, _ := testDriver(t)
	handle, err := d.Create(context.Background(), "scope", testConfig())
	if err != nil {
		t.Fatal(err)
	}
	cancelling := make(chan struct{})
	release := make(chan struct{})
	d.stream = func(ctx context.Context, _ string, argv []string, _ remotecommand.StreamOptions) error {
		if argv[1] == "cancel" {
			close(cancelling)
			<-release
			return nil
		}
		<-ctx.Done()
		return ctx.Err()
	}
	term, err := d.Attach(context.Background(), handle, "/workspace", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- term.Close() }()
	select {
	case <-cancelling:
	case <-time.After(time.Second):
		t.Fatal("remote cancellation did not start")
	}
	select {
	case <-closed:
		t.Error("Close returned before cancellation finished")
	default:
	}
	close(release)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after cancellation")
	}
}

func TestKubernetesTerminalNormalExitDoesNotCancelAgain(t *testing.T) {
	d, _ := testDriver(t)
	handle, err := d.Create(context.Background(), "scope", testConfig())
	if err != nil {
		t.Fatal(err)
	}
	d.stream = func(_ context.Context, _ string, argv []string, _ remotecommand.StreamOptions) error {
		if argv[1] == "cancel" {
			t.Error("successful shell exit should not dispatch another cancel request")
		}
		return nil
	}
	term, err := d.Attach(context.Background(), handle, "/workspace", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(term); err != nil {
		t.Fatal(err)
	}
	if err := term.Close(); err != nil {
		t.Fatal(err)
	}
}
