package sandboxkube

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/sandboxruntime"
	"github.com/rakunlabs/at/internal/service/container"
)

// These tests are opt-in and use only a newly created, random namespace. They
// deliberately bypass production's operator assertions: kindnet is adequate
// for transport tests but is NOT proof of CNI enforcement or CSI guarantees.
func clusterDriver(t *testing.T) (*Driver, container.Config) {
	t.Helper()
	kubeconfig := os.Getenv("AT_TEST_KUBERNETES_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("set AT_TEST_KUBERNETES_KUBECONFIG or run ci/test-sandbox-kubernetes.sh")
	}
	helper, image := os.Getenv("AT_TEST_KUBERNETES_HELPER_IMAGE"), os.Getenv("AT_TEST_KUBERNETES_IMAGE")
	if helper == "" || image == "" {
		t.Fatal("cluster tests require explicit helper and sandbox image names")
	}
	rc, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	rc.Timeout = 15 * time.Second
	client, err := kubernetes.NewForConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	namespace := "at-test-" + strings.ToLower(rand.Text())[:16]
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace,
		Labels: map[string]string{"pod-security.kubernetes.io/enforce": "baseline"}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{}); err != nil {
			t.Errorf("delete test namespace %s: %v", namespace, err)
		}
	})
	d := &Driver{client: client, restConfig: rc, opts: config.KubernetesSandbox{Namespace: namespace, DeploymentID: "cluster-test", HelperImage: helper,
		HomeAccessMode: string(corev1.ReadWriteOnce), HomeSize: "64Mi", WorkspaceSize: "128Mi", SingleReplica: true, PodPidsLimit: 256}}
	d.stream = d.streamExec
	// Use the shipped controller Role, not the cluster administrator, for
	// sandbox operations. Keep the admin client only for fixture setup/cleanup.
	if _, err := client.CoreV1().ServiceAccounts(namespace).Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "controller"}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile("../../../deploy/kubernetes/sandbox-rbac.yaml")
	if err != nil {
		t.Fatal(err)
	}
	decoder := kubeyaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifest), 4096)
	var role rbacv1.Role
	for {
		var document rbacv1.Role
		if err := decoder.Decode(&document); err != nil {
			t.Fatalf("read controller Role: %v", err)
		}
		if document.Kind == "Role" {
			role = document
			break
		}
	}
	role.Namespace = namespace
	if _, err := client.RbacV1().Roles(namespace).Create(ctx, &role, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RbacV1().RoleBindings(namespace).Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "controller"},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: "controller", Namespace: namespace}},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: role.Name},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	restricted := rest.CopyConfig(rc)
	restricted.Impersonate = rest.ImpersonationConfig{UserName: "system:serviceaccount:" + namespace + ":controller", Groups: []string{"system:serviceaccounts", "system:serviceaccounts:" + namespace, "system:authenticated"}}
	controller, err := kubernetes.NewForConfig(restricted)
	if err != nil {
		t.Fatal(err)
	}
	d.client, d.restConfig = controller, restricted
	d.ownership = newControllerOwnership(controller.CoordinationV1().Leases(namespace), d.opts.DeploymentID)
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Errorf("release controller ownership: %v", err)
		}
	})
	t.Log("real API/exec/PVC tests; NetworkPolicy enforcement, multi-node CSI and distributed ownership are NOT being certified")
	cfg := container.Config{Enabled: true, Image: image, CPU: "500m", Memory: "256Mi", KeepAlive: true, PersistentVolume: true, PidsLimit: 256,
		HomeScope: "account", HomePath: "/root", CapAdd: container.PackageManagerCapabilities}
	return d, cfg
}

func TestKubernetesClusterControllerRBAC(t *testing.T) {
	d, _ := clusterDriver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := d.client.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{}); !apierrors.IsForbidden(err) {
		t.Fatalf("controller may read another namespace: %v", err)
	}
	if _, err := d.client.CoreV1().Secrets(d.opts.Namespace).List(ctx, metav1.ListOptions{}); !apierrors.IsForbidden(err) {
		t.Fatalf("controller may read secrets: %v", err)
	}
	if _, err := d.client.CoordinationV1().Leases(d.opts.Namespace).Get(ctx, "foreign-controller", metav1.GetOptions{}); !apierrors.IsForbidden(err) {
		t.Fatalf("controller may read unrelated Lease: %v", err)
	}
}

func clusterExec(t *testing.T, d *Driver, handle string, req container.ExecRequest) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	req.Stdout, req.Stderr = &stdout, &stderr
	code, err := d.Exec(ctx, handle, req)
	if err != nil {
		t.Fatalf("exec %v: %v (stderr %s)", req.Argv, err, stderr.String())
	}
	return stdout.String(), stderr.String(), code
}

// Select each transport without fallback so a passing test actually proves
// both protocols, not merely that one of them happened to work.
func clusterTransport(d *Driver, websocket bool) streamFunc {
	return func(ctx context.Context, name string, argv []string, opts remotecommand.StreamOptions) error {
		request := d.client.CoreV1().RESTClient().Post().Resource("pods").Name(name).Namespace(d.opts.Namespace).SubResource("exec").VersionedParams(&corev1.PodExecOptions{
			Container: sandboxContainer, Command: argv, Stdin: opts.Stdin != nil, Stdout: opts.Stdout != nil, Stderr: opts.Stderr != nil, TTY: opts.Tty}, scheme.ParameterCodec)
		cfg := rest.CopyConfig(d.restConfig)
		cfg.Timeout = 0
		var executor remotecommand.Executor
		var err error
		if websocket {
			executor, err = remotecommand.NewWebSocketExecutor(cfg, "GET", request.URL().String())
		} else {
			executor, err = remotecommand.NewSPDYExecutor(cfg, "POST", request.URL())
		}
		if err != nil {
			return err
		}
		return executor.StreamWithContext(ctx, opts)
	}
}

func TestKubernetesClusterLifecycleAndExec(t *testing.T) {
	d, cfg := clusterDriver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	handle, err := d.Create(ctx, "space", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err := d.Purge(ctx, "space"); err != nil {
			t.Errorf("purge: %v", err)
		}
	})

	t.Run("argv env stdin and exit", func(t *testing.T) {
		out, stderr, code := clusterExec(t, d, handle, container.ExecRequest{WorkDir: "/workspace", Env: map[string]string{"AT_VALUE": "literal; value"},
			Argv: []string{"sh", "-c", `printf '%s\n%s\n' "$PWD" "$AT_VALUE"; cat; printf 'stderr' >&2; exit 7`}, Stdin: strings.NewReader("payload")})
		if out != "/workspace\nliteral; value\npayload" || stderr != "stderr" || code != 7 {
			t.Fatalf("exec contract: out=%q err=%q code=%d", out, stderr, code)
		}
	})
	for _, transport := range []string{"WebSocket", "SPDY"} {
		t.Run(transport+" exec", func(t *testing.T) {
			original := d.stream
			d.stream = clusterTransport(d, transport == "WebSocket")
			defer func() { d.stream = original }()
			out, stderr, code := clusterExec(t, d, handle, container.ExecRequest{Argv: []string{"sh", "-c", `cat; printf 'separate stderr' >&2; exit 9`}, Stdin: strings.NewReader("separate stdin")})
			if out != "separate stdin" || stderr != "separate stderr" || code != 9 {
				t.Fatalf("transport contract: out=%q stderr=%q exit=%d", out, stderr, code)
			}
		})
	}
	t.Run("file helper and home upload", func(t *testing.T) {
		out, stderr, code := clusterExec(t, d, handle, container.ExecRequest{Argv: []string{sandboxruntime.FileHelper, "write", "", "hello.txt"}, Stdin: strings.NewReader("workspace data")})
		if code != 0 {
			t.Fatalf("helper: %d %s %s", code, out, stderr)
		}
		if err := d.InstallFile(ctx, handle, "/root/.ssh/test-key", []byte("home data"), 0o600); err != nil {
			t.Fatal(err)
		}
		out, stderr, code = clusterExec(t, d, handle, container.ExecRequest{Argv: []string{"sh", "-c", `cat /workspace/hello.txt /root/.ssh/test-key; stat -c '%a' /root/.ssh/test-key`}})
		if code != 0 || out != "workspace datahome data600\n" {
			t.Fatalf("stored files: %s %s %d", out, stderr, code)
		}
	})
	t.Run("reuse", func(t *testing.T) {
		got, err := d.Create(ctx, "space", cfg)
		if err != nil || got != handle {
			t.Fatalf("pod not reused: %s %v", got, err)
		}
	})
	t.Run("controller exclusivity", func(t *testing.T) {
		second := newControllerOwnership(d.client.CoordinationV1().Leases(d.opts.Namespace), d.opts.DeploymentID)
		defer second.Close()
		if _, _, err := second.begin(ctx); !errors.Is(err, errControllerOwnership) {
			t.Fatalf("second controller admitted: %v", err)
		}
		if err := d.ownership.renew(); err != nil {
			t.Fatalf("renew namespace ownership: %v", err)
		}
	})
	t.Run("stop keeps volumes not packages", func(t *testing.T) {
		_, _, code := clusterExec(t, d, handle, container.ExecRequest{Argv: []string{"touch", "/usr/at-root-marker"}})
		if code != 0 {
			t.Fatal("could not write root marker")
		}
		if err := d.Stop(ctx, handle); err != nil {
			t.Fatal(err)
		}
		next, err := d.Create(ctx, "space", cfg)
		if err != nil {
			t.Fatal(err)
		}
		if next == handle {
			t.Fatal("deleted pod UID was reused")
		}
		handle = next
		out, stderr, code := clusterExec(t, d, handle, container.ExecRequest{Argv: []string{"sh", "-c", `test ! -e /usr/at-root-marker && cat /workspace/hello.txt /root/.ssh/test-key`}})
		if code != 0 || out != "workspace datahome data" {
			t.Fatalf("persistence contract: %q %q %d", out, stderr, code)
		}
	})
	t.Run("RWO home refuses second space", func(t *testing.T) {
		if _, err := d.Create(ctx, "other-space", cfg); err == nil || !strings.Contains(err.Error(), "already mounted") {
			t.Fatalf("RWO mount admitted: %v", err)
		}
	})
	t.Run("home reset keeps workspace", func(t *testing.T) {
		if err := d.RemoveHome(ctx, "account"); err != nil {
			t.Fatal(err)
		}
		next, err := d.Create(ctx, "space", cfg)
		if err != nil {
			t.Fatal(err)
		}
		handle = next
		out, stderr, code := clusterExec(t, d, handle, container.ExecRequest{Argv: []string{"sh", "-c", `test ! -e /root/.ssh/test-key && cat /workspace/hello.txt`}})
		if code != 0 || out != "workspace data" {
			t.Fatalf("home reset crossed storage boundary: %q %q %d", out, stderr, code)
		}
	})
}

// notifyWriter signals process startup without sleeping/polling a remote shell.
type notifyWriter struct {
	once  sync.Once
	ready chan struct{}
}

func (w *notifyWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.ready) })
	return len(p), nil
}

func TestKubernetesClusterCancellationAndTTY(t *testing.T) {
	d, cfg := clusterDriver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	handle, err := d.Create(ctx, "space", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err := d.Purge(ctx, "space"); err != nil {
			t.Errorf("purge: %v", err)
		}
	})
	t.Run("cancel remote children", func(t *testing.T) {
		runCtx, stop := context.WithTimeout(ctx, 15*time.Second)
		defer stop()
		writer := &notifyWriter{ready: make(chan struct{})}
		done := make(chan error, 1)
		go func() {
			_, err := d.Exec(runCtx, handle, container.ExecRequest{Argv: []string{"sh", "-c", `sleep 300 & echo $! > /workspace/child.pid; echo ready; wait`}, Stdout: writer, Stderr: io.Discard})
			done <- err
		}()
		select {
		case <-writer.ready:
		case <-runCtx.Done():
			t.Fatal("remote command did not start")
		}
		stop()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("exec did not cancel")
		}
		_, stderr, code := clusterExec(t, d, handle, container.ExecRequest{Argv: []string{"sh", "-c", `p=$(cat /workspace/child.pid); test ! -d /proc/$p || grep -q 'State:.*Z' /proc/$p/status`}})
		if code != 0 {
			t.Fatalf("child survived cancellation: %d %s", code, stderr)
		}
	})
	for _, transport := range []string{"WebSocket", "SPDY"} {
		t.Run(transport+" terminal resize and commands", func(t *testing.T) {
			original := d.stream
			d.stream = clusterTransport(d, transport == "WebSocket")
			defer func() { d.stream = original }()
			termCtx, stop := context.WithTimeout(ctx, 15*time.Second)
			defer stop()
			term, err := d.Attach(termCtx, handle, "/workspace", 80, 24)
			if err != nil {
				t.Fatal(err)
			}
			defer term.Close()
			result := make(chan string, 1)
			go func() { data, _ := io.ReadAll(term); result <- string(data) }()
			if err := term.Resize(100, 32); err != nil {
				t.Fatal(err)
			}
			if _, err := term.Write([]byte("stty size\nprintf 'at-tty-%s\\n' ok\nexit\n")); err != nil {
				t.Fatal(err)
			}
			select {
			case out := <-result:
				if !strings.Contains(out, "at-tty-ok") || !strings.Contains(out, "32 100") || strings.Contains(out, "no job control") {
					t.Fatalf("TTY contract: %s", out)
				}
			case <-termCtx.Done():
				t.Fatal("TTY failed to finish")
			}
		})
	}
}

func TestKubernetesClusterRecoveryAndShutdown(t *testing.T) {
	d, cfg := clusterDriver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	handle, err := d.Create(ctx, "space", cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, stderr, code := clusterExec(t, d, handle, container.ExecRequest{Argv: []string{"sh", "-c", `printf 'persistent' > /workspace/recovery.txt`}})
	if code != 0 {
		t.Fatalf("prepare recovery: %s", stderr)
	}
	// Simulate a previously stopped controller leaving a Pod behind. This is
	// not an unsafe takeover: the old guard is closed before the new one starts.
	if err := d.ownership.Close(); err != nil {
		t.Fatal(err)
	}
	next := &Driver{client: d.client, restConfig: d.restConfig, opts: d.opts}
	next.stream = next.streamExec
	next.ownership = newControllerOwnership(d.client.CoordinationV1().Leases(d.opts.Namespace), d.opts.DeploymentID)
	t.Cleanup(func() {
		if err := next.Close(); err != nil {
			t.Errorf("close recovered controller: %v", err)
		}
	})
	newHandle, err := next.Create(ctx, "space", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if newHandle == handle {
		t.Fatal("previous controller's Pod was adopted with potentially orphan commands")
	}
	out, stderr, code := clusterExec(t, next, newHandle, container.ExecRequest{Argv: []string{"cat", "/workspace/recovery.txt"}})
	if out != "persistent" || code != 0 {
		t.Fatalf("recovery lost workspace: %q %q %d", out, stderr, code)
	}
	writer := &notifyWriter{ready: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := next.Exec(ctx, newHandle, container.ExecRequest{Argv: []string{"sh", "-c", `echo ready; sleep 300`}, Stdout: writer, Stderr: io.Discard})
		done <- err
	}()
	select {
	case <-writer.ready:
	case <-ctx.Done():
		t.Fatal("shutdown command did not start")
	}
	if err := next.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown did not cancel command: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("command did not drain")
	}
	if _, err := next.client.CoreV1().Pods(next.opts.Namespace).Get(ctx, next.name("space", "space"), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("shutdown left workload: %v", err)
	}
	if _, err := next.client.CoreV1().PersistentVolumeClaims(next.opts.Namespace).Get(ctx, next.name("data", "space"), metav1.GetOptions{}); err != nil {
		t.Fatalf("shutdown deleted workspace PVC: %v", err)
	}
	lease, err := next.ownership.leases.Get(ctx, controllerLeaseName, metav1.GetOptions{})
	if err != nil || lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != "" {
		t.Fatalf("successful drain did not release Lease: %v", err)
	}
}
