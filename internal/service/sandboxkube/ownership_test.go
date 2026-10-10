package sandboxkube

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func testOwnership(t *testing.T) (*controllerOwnership, *fake.Clientset) {
	t.Helper()
	client := fake.NewClientset()
	g := newControllerOwnership(client.CoordinationV1().Leases("test"), "deployment")
	t.Cleanup(func() { _ = g.Close() })
	return g, client
}

func TestControllerOwnershipExclusiveAndCleanRelease(t *testing.T) {
	g, client := testOwnership(t)
	ctx, release, err := g.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	release()
	second := newControllerOwnership(client.CoordinationV1().Leases("test"), "deployment")
	defer second.Close()
	if _, _, err := second.begin(context.Background()); !errors.Is(err, errControllerOwnership) {
		t.Fatalf("second controller admitted: %v", err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	_, release, err = second.begin(context.Background())
	if err != nil {
		t.Fatalf("clean handoff: %v", err)
	}
	release()
}

func TestControllerOwnershipDoesNotStealExpiredLease(t *testing.T) {
	g, client := testOwnership(t)
	old := metav1.NewMicroTime(time.Now().Add(-time.Hour))
	_, err := client.CoordinationV1().Leases("test").Create(context.Background(), &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: controllerLeaseName, Labels: map[string]string{managedLabel: "deployment"}},
		Spec:       coordinationv1.LeaseSpec{HolderIdentity: ptr("previous"), RenewTime: &old, LeaseDurationSeconds: ptr[int32](1)},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.begin(context.Background()); !errors.Is(err, errControllerOwnership) {
		t.Fatalf("stale lease stolen: %v", err)
	}
}

func TestControllerOwnershipLossCancelsOperations(t *testing.T) {
	for _, failure := range []string{"holder", "uid", "api"} {
		t.Run(failure, func(t *testing.T) {
			g, client := testOwnership(t)
			ctx, release, err := g.begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if failure == "api" {
				client.PrependReactor("get", "leases", func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, fmt.Errorf("API unavailable") })
			} else {
				lease, _ := client.CoordinationV1().Leases("test").Get(context.Background(), controllerLeaseName, metav1.GetOptions{})
				if failure == "holder" {
					lease.Spec.HolderIdentity = ptr("foreign")
				} else {
					lease.UID = "replacement"
				}
				if _, err := client.CoordinationV1().Leases("test").Update(context.Background(), lease, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			if err := g.renew(); !errors.Is(err, errControllerOwnership) {
				t.Fatalf("lost lease accepted: %v", err)
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("active operation was not cancelled")
			}
			if _, _, err := g.begin(context.Background()); !errors.Is(err, errControllerOwnership) {
				t.Fatalf("lost controller resumed: %v", err)
			}
		})
	}
}

func TestControllerOwnershipCloseDrainsBeforeRelease(t *testing.T) {
	g, client := testOwnership(t)
	ctx, release, err := g.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- g.Close() }()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel active operation")
	}
	lease, err := client.CoordinationV1().Leases("test").Get(context.Background(), controllerLeaseName, metav1.GetOptions{})
	if err != nil || lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != g.owner {
		t.Fatalf("ownership released before drain: %v", err)
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not finish")
	}
}

func TestControllerOwnershipCallerCancellationDoesNotPoisonGuard(t *testing.T) {
	g, _ := testOwnership(t)
	_, release, err := g.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Fake clients may ignore context, so reject cancellation before API admission.
	if _, release, err := g.begin(ctx); err == nil {
		release()
		t.Fatal("cancelled request admitted")
	}
	_, release, err = g.begin(context.Background())
	if err != nil {
		t.Fatalf("caller cancellation poisoned ownership: %v", err)
	}
	release()
}

func TestControllerOwnershipFailedCancellationDuringCloseKeepsLease(t *testing.T) {
	g, client := testOwnership(t)
	ctx, release, err := g.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- g.Close() }()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel work")
	}
	d := &Driver{ownership: g}
	d.cancellationFailed(fmt.Errorf("exec API unavailable"))
	release()
	select {
	case err := <-done:
		if !errors.Is(err, errControllerOwnership) {
			t.Fatalf("uncertain cancellation released ownership: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not finish")
	}
	lease, err := client.CoordinationV1().Leases("test").Get(context.Background(), controllerLeaseName, metav1.GetOptions{})
	if err != nil || lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != g.owner {
		t.Fatalf("failed cancellation cleared holder: %v", err)
	}
	if err := g.Close(); !errors.Is(err, errControllerOwnership) {
		t.Fatalf("repeated Close lost previous failure: %v", err)
	}
}
