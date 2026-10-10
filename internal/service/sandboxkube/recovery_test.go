package sandboxkube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/rakunlabs/at/internal/service/container"
)

func TestKubernetesRecoveryStopsLeftoversKeepsStorage(t *testing.T) {
	d, client := testDriver(t)
	ctx := context.Background()
	cfg := testConfig()
	cfg.HomeScope, cfg.HomePath = "account", "/root"
	handle, err := d.Create(ctx, "old", cfg)
	if err != nil {
		t.Fatal(err)
	}
	foreign := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "foreign", Namespace: d.opts.Namespace}}
	if _, err := client.CoreV1().Pods(d.opts.Namespace).Create(ctx, foreign, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	d.ownership = newControllerOwnership(client.CoordinationV1().Leases(d.opts.Namespace), d.opts.DeploymentID)
	t.Cleanup(func() { _ = d.Close() })
	if _, err := d.Create(ctx, "new", cfg); err != nil {
		t.Fatal(err)
	}
	name, _, _ := splitHandle(handle)
	if _, err := client.CoreV1().Pods(d.opts.Namespace).Get(ctx, name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("old workload survived recovery: %v", err)
	}
	if _, err := client.CoreV1().Pods(d.opts.Namespace).Get(ctx, "foreign", metav1.GetOptions{}); err != nil {
		t.Fatalf("foreign workload touched: %v", err)
	}
	for _, name := range []string{d.name("data", "old"), d.name("home", "account")} {
		if _, err := client.CoreV1().PersistentVolumeClaims(d.opts.Namespace).Get(ctx, name, metav1.GetOptions{}); err != nil {
			t.Fatalf("recovery deleted storage %s: %v", name, err)
		}
	}
	if _, err := d.Create(ctx, "another", testConfig()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Pods(d.opts.Namespace).Get(ctx, d.name("space", "new"), metav1.GetOptions{}); err != nil {
		t.Fatalf("recovery ran twice and deleted new work: %v", err)
	}
}

func TestKubernetesRecoveryFailureRetriesBeforeAdmission(t *testing.T) {
	d, client := testDriver(t)
	d.ownership = newControllerOwnership(client.CoordinationV1().Leases(d.opts.Namespace), d.opts.DeploymentID)
	t.Cleanup(func() { _ = d.Close() })
	fail := true
	client.PrependReactor("list", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
		if fail {
			return true, nil, fmt.Errorf("API unavailable")
		}
		return false, nil, nil
	})
	if _, err := d.Create(context.Background(), "scope", testConfig()); err == nil {
		t.Fatal("failed recovery admitted new workload")
	}
	if d.recovered {
		t.Fatal("failed recovery marked complete")
	}
	fail = false
	if _, err := d.Create(context.Background(), "scope", testConfig()); err != nil {
		t.Fatal(err)
	}
}

func TestKubernetesShutdownDrainsBeforeDeletingPods(t *testing.T) {
	d, client := testDriver(t)
	d.ownership = newControllerOwnership(client.CoordinationV1().Leases(d.opts.Namespace), d.opts.DeploymentID)
	handle, err := d.Create(context.Background(), "scope", testConfig())
	if err != nil {
		t.Fatal(err)
	}
	started, cancelled, releaseCancel := make(chan struct{}), make(chan struct{}), make(chan struct{})
	d.stream = func(ctx context.Context, _ string, argv []string, _ remotecommand.StreamOptions) error {
		if argv[1] == "cancel" {
			close(cancelled)
			<-releaseCancel
			return nil
		}
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	execDone := make(chan error, 1)
	go func() {
		_, err := d.Exec(context.Background(), handle, container.ExecRequest{Argv: []string{"sleep", "300"}, Stdout: io.Discard})
		execDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("exec did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- d.Shutdown(context.Background()) }()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel exec")
	}
	if _, err := client.CoreV1().Pods(d.opts.Namespace).Get(context.Background(), d.name("space", "scope"), metav1.GetOptions{}); err != nil {
		t.Fatalf("pod deleted before cancellation finished: %v", err)
	}
	close(releaseCancel)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish")
	}
	if err := <-execDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("exec not cancelled: %v", err)
	}
	if _, err := client.CoreV1().Pods(d.opts.Namespace).Get(context.Background(), d.name("space", "scope"), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("shutdown left pod: %v", err)
	}
	if _, err := client.CoreV1().PersistentVolumeClaims(d.opts.Namespace).Get(context.Background(), d.name("data", "scope"), metav1.GetOptions{}); err != nil {
		t.Fatalf("shutdown removed PVC: %v", err)
	}
}

func TestKubernetesShutdownFailureKeepsLease(t *testing.T) {
	d, client := testDriver(t)
	d.ownership = newControllerOwnership(client.CoordinationV1().Leases(d.opts.Namespace), d.opts.DeploymentID)
	if _, err := d.Create(context.Background(), "scope", testConfig()); err != nil {
		t.Fatal(err)
	}
	client.PrependReactor("delete", "pods", func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, fmt.Errorf("cannot delete") })
	if err := d.Shutdown(context.Background()); err == nil {
		t.Fatal("failed shutdown reported success")
	}
	lease, err := client.CoordinationV1().Leases(d.opts.Namespace).Get(context.Background(), controllerLeaseName, metav1.GetOptions{})
	if err != nil || lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != d.ownership.owner {
		t.Fatalf("failed cleanup released Lease: %v", err)
	}
}

func TestKubernetesRecoveryRejectsUnrecognizedPodsBeforeDeletion(t *testing.T) {
	d, client := testDriver(t)
	ctx := context.Background()
	if _, err := d.Create(ctx, "scope", testConfig()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Pods(d.opts.Namespace).Create(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "unknown", Labels: map[string]string{managedLabel: d.opts.DeploymentID},
	}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	d.ownership = newControllerOwnership(client.CoordinationV1().Leases(d.opts.Namespace), d.opts.DeploymentID)
	t.Cleanup(func() { _ = d.Close() })
	if _, err := d.Create(ctx, "new", testConfig()); err == nil {
		t.Fatal("unrecognized managed resource silently adopted")
	}
	if _, err := client.CoreV1().Pods(d.opts.Namespace).Get(ctx, d.name("space", "scope"), metav1.GetOptions{}); err != nil {
		t.Fatalf("recovery partially deleted workloads before validating snapshot: %v", err)
	}
}
