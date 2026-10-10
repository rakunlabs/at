package sandboxkube

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	typedcoordination "k8s.io/client-go/kubernetes/typed/coordination/v1"
)

const controllerLeaseName = "at-sandbox-controller"
const ownershipTimeout = 5 * time.Second
const ownershipRenewInterval = 5 * time.Second

var errControllerOwnership = errors.New("Kubernetes sandbox controller ownership unavailable")

// controllerOwnership is an exclusive namespace guard, NOT leader election.
// A stale lease is never automatically stolen: an API partition or paused old
// process cannot safely be fenced by a timestamp. Recovery requires stopping
// the former controller before an operator clears its lease.
type controllerOwnership struct {
	mu         sync.Mutex
	leases     typedcoordination.LeaseInterface
	owner      string
	deployment string
	uid        string
	ctx        context.Context
	cancel     context.CancelFunc
	started    bool
	closed     bool
	lost       error
	done       chan struct{}
	active     sync.WaitGroup
	closeOnce  sync.Once
	closeErr   error
}

func newControllerOwnership(leases typedcoordination.LeaseInterface, deployment string) *controllerOwnership {
	ctx, cancel := context.WithCancel(context.Background())
	return &controllerOwnership{leases: leases, owner: deployment + ":" + rand.Text(), deployment: deployment, ctx: ctx, cancel: cancel, done: make(chan struct{})}
}

func (g *controllerOwnership) refuse(err error) error {
	if g.lost == nil {
		g.lost = fmt.Errorf("%w: %w; stop this controller and review the namespace Lease before restarting", errControllerOwnership, err)
		g.cancel()
	}
	return g.lost
}

func (g *controllerOwnership) verify(lease *coordinationv1.Lease) error {
	if lease.DeletionTimestamp != nil || string(lease.UID) != g.uid || lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != g.owner {
		return fmt.Errorf("namespace controller Lease was removed or changed")
	}
	return nil
}

// begin checks ownership at admission and binds the operation to the owner's
// lifetime. Readiness failures after acquisition fail closed permanently.
func (g *controllerOwnership) begin(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil, nil, fmt.Errorf("%w: controller closed", errControllerOwnership)
	}
	if g.lost != nil {
		return nil, nil, g.lost
	}
	checkCtx, cancel := context.WithTimeout(ctx, ownershipTimeout)
	defer cancel()
	if !g.started {
		if err := g.acquire(checkCtx); err != nil {
			return nil, nil, err
		}
		g.started = true
		go g.renewLoop()
	} else {
		lease, err := g.leases.Get(checkCtx, controllerLeaseName, metav1.GetOptions{})
		if err == nil {
			err = g.verify(lease)
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			return nil, nil, g.refuse(err)
		}
	}
	operation, stop := context.WithCancel(ctx)
	watch := context.AfterFunc(g.ctx, stop)
	g.active.Add(1)
	var once sync.Once
	return operation, func() { once.Do(func() { watch(); stop(); g.active.Done() }) }, nil
}

func (g *controllerOwnership) acquire(ctx context.Context) error {
	lease, err := g.leases.Get(ctx, controllerLeaseName, metav1.GetOptions{})
	now := metav1.NewMicroTime(time.Now().UTC())
	if apierrors.IsNotFound(err) {
		lease, err = g.leases.Create(ctx, &coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Name: controllerLeaseName,
			Labels: map[string]string{managedLabel: g.deployment}}, Spec: coordinationv1.LeaseSpec{HolderIdentity: ptr(g.owner), AcquireTime: &now, RenewTime: &now, LeaseDurationSeconds: ptr[int32](30)}}, metav1.CreateOptions{})
	} else if err == nil {
		if lease.DeletionTimestamp != nil || lease.Labels[managedLabel] != g.deployment {
			return fmt.Errorf("%w: dedicated namespace already belongs to another controller deployment", errControllerOwnership)
		}
		if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != "" {
			return fmt.Errorf("%w: namespace is held by another AT process; expired timestamps do not authorize takeover", errControllerOwnership)
		}
		lease.Spec.HolderIdentity = ptr(g.owner)
		lease.Spec.AcquireTime, lease.Spec.RenewTime = &now, &now
		lease.Spec.LeaseDurationSeconds = ptr[int32](30)
		lease, err = g.leases.Update(ctx, lease, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("%w: acquire namespace Lease (check coordination.k8s.io RBAC): %w", errControllerOwnership, err)
	}
	g.uid = string(lease.UID)
	return nil
}

func (g *controllerOwnership) renew() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.lost != nil {
		return g.lost
	}
	ctx, cancel := context.WithTimeout(g.ctx, ownershipTimeout)
	defer cancel()
	lease, err := g.leases.Get(ctx, controllerLeaseName, metav1.GetOptions{})
	if err == nil {
		err = g.verify(lease)
	}
	if err == nil {
		now := metav1.NewMicroTime(time.Now().UTC())
		lease.Spec.RenewTime = &now
		_, err = g.leases.Update(ctx, lease, metav1.UpdateOptions{})
	}
	if err != nil {
		return g.refuse(err)
	}
	return nil
}

func (g *controllerOwnership) renewLoop() {
	defer close(g.done)
	ticker := time.NewTicker(ownershipRenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-ticker.C:
			if err := g.renew(); err != nil {
				slog.Error("Kubernetes sandbox ownership lost", "error", err.Error())
				return
			}
		}
	}
}

func (g *controllerOwnership) Close() error {
	return g.closeWithCleanup(context.Background(), nil)
}

func (g *controllerOwnership) closeWithCleanup(ctx context.Context, cleanup func(context.Context) error) error {
	g.closeOnce.Do(func() { g.closeErr = g.close(ctx, cleanup) })
	return g.closeErr
}

func (g *controllerOwnership) close(parent context.Context, cleanup func(context.Context) error) error {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil
	}
	g.closed = true
	g.cancel()
	started := g.started
	g.mu.Unlock()
	if !started {
		return nil
	}
	<-g.done
	drained := make(chan struct{})
	go func() { g.active.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-parent.Done():
		return fmt.Errorf("controller shutdown cancelled; leaving namespace Lease held: %w", parent.Err())
	case <-time.After(20 * time.Second):
		return fmt.Errorf("controller operations did not drain; leaving namespace Lease held")
	}
	g.mu.Lock()
	lost := g.lost
	g.mu.Unlock()
	if lost != nil {
		return lost
	} // Leave the Lease for deliberate operator recovery.
	if cleanup != nil {
		if err := cleanup(parent); err != nil {
			return fmt.Errorf("controller cleanup failed; leaving namespace Lease held: %w", err)
		}
	}
	ctx, cancel := context.WithTimeout(parent, ownershipTimeout)
	defer cancel()
	lease, err := g.leases.Get(ctx, controllerLeaseName, metav1.GetOptions{})
	if err == nil {
		err = g.verify(lease)
	}
	if err != nil {
		return fmt.Errorf("release controller Lease: %w", err)
	}
	lease.Spec.HolderIdentity = ptr("")
	_, err = g.leases.Update(ctx, lease, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("release controller Lease: %w", err)
	}
	return nil
}

func (d *Driver) operation(ctx context.Context) (context.Context, context.CancelFunc, error) {
	// Only test fixtures construct Drivers directly without the guard.
	if d.ownership == nil {
		return ctx, func() {}, nil
	}
	operation, release, err := d.ownership.begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	d.recoveryMu.Lock()
	if !d.recovered {
		recoveryCtx, cancel := context.WithTimeout(operation, 2*time.Minute)
		err = d.stopManagedPods(recoveryCtx)
		cancel()
		if err == nil {
			d.recovered = true
		}
	}
	d.recoveryMu.Unlock()
	if err != nil {
		release()
		return nil, nil, fmt.Errorf("recover previous controller sandboxes: %w", err)
	}
	return operation, release, nil
}

func (d *Driver) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return d.Shutdown(ctx)
}

func (d *Driver) Shutdown(ctx context.Context) error {
	if d.ownership == nil {
		return nil
	}
	return d.ownership.closeWithCleanup(ctx, d.stopManagedPods)
}
