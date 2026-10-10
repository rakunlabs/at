package sandboxkube

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/sandboxruntime"
	"github.com/rakunlabs/at/internal/service/container"
)

const managedLabel = "at.rakunlabs.io/managed-by"
const scopeLabel = "at.rakunlabs.io/scope"
const homeLabel = "at.rakunlabs.io/home"
const configAnnotation = "at.rakunlabs.io/config"
const homePathAnnotation = "at.rakunlabs.io/home-path"
const sandboxContainer = "sandbox"

type Driver struct {
	client     kubernetes.Interface
	restConfig *rest.Config
	opts       config.KubernetesSandbox
	ownership  *controllerOwnership
	recoveryMu sync.Mutex
	recovered  bool
	// stream is injectable so exec/TTY semantics can be tested without a cluster.
	stream streamFunc
}

var _ container.Driver = (*Driver)(nil)
var _ container.FileInstaller = (*Driver)(nil)
var _ container.HomeRemover = (*Driver)(nil)

func New(opts config.KubernetesSandbox) (*Driver, error) {
	opts, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	var rc *rest.Config
	if opts.Kubeconfig != "" {
		rc, err = clientcmd.BuildConfigFromFlags("", opts.Kubeconfig)
	} else {
		rc, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("load Kubernetes credentials: %w", err)
	}
	rc = rest.CopyConfig(rc)
	rc.UserAgent = "at-sandbox"
	rc.Timeout = 30 * time.Second
	client, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}
	d := &Driver{client: client, restConfig: rc, opts: opts}
	d.ownership = newControllerOwnership(client.CoordinationV1().Leases(opts.Namespace), opts.DeploymentID)
	d.stream = d.streamExec
	return d, nil
}

func (d *Driver) Name() string { return "kubernetes" }

func (d *Driver) Capabilities() container.RuntimeCapabilities {
	return container.RuntimeCapabilities{Backend: d.Name(), PersistentHome: true, FileHelperPath: sandboxruntime.FileHelper,
		Notice: "Single AT replica only. Stop/restart recreates the pod: workspace/home volumes persist, installed system packages do not. PID isolation depends on the declared kubelet limit; disk checks are best effort."}
}

func hash(value string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))[:20] }
func (d *Driver) name(kind, scope string) string {
	return "at-" + kind + "-" + hash(d.opts.DeploymentID+":"+scope)
}
func (d *Driver) labels(scope string) map[string]string {
	return map[string]string{managedLabel: d.opts.DeploymentID, scopeLabel: hash(scope)}
}
func (d *Driver) owned(meta metav1.Object, scope string) error {
	if meta.GetLabels()[managedLabel] != d.opts.DeploymentID || (scope != "" && meta.GetLabels()[scopeLabel] != hash(scope)) {
		return fmt.Errorf("refusing foreign Kubernetes resource %q", meta.GetName())
	}
	return nil
}
func podHandle(pod *corev1.Pod) string { return pod.Name + "/" + string(pod.UID) }
func splitHandle(handle string) (string, types.UID, error) {
	name, uid, ok := strings.Cut(handle, "/")
	if !ok || name == "" || uid == "" || strings.Contains(uid, "/") {
		return "", "", fmt.Errorf("invalid sandbox handle")
	}
	return name, types.UID(uid), nil
}

func (d *Driver) pod(ctx context.Context, handle string) (*corev1.Pod, error) {
	name, uid, err := splitHandle(handle)
	if err != nil {
		return nil, err
	}
	pod, err := d.client.CoreV1().Pods(d.opts.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if err := d.owned(pod, ""); err != nil {
		return nil, err
	}
	if pod.UID != uid {
		return nil, fmt.Errorf("sandbox handle is stale; pod identity changed")
	}
	return pod, nil
}

func (d *Driver) Create(ctx context.Context, scope string, cfg container.Config) (string, error) {
	if scope == "" {
		return "", fmt.Errorf("sandbox scope is required")
	}
	if err := validateSandbox(cfg, d.opts); err != nil {
		return "", err
	}
	ctx, release, err := d.operation(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	spec, err := d.podSpec(scope, cfg)
	if err != nil {
		return "", err
	}
	// Bound provisioning even when the request itself has no deadline.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	api := d.client.CoreV1().Pods(d.opts.Namespace)
	existing, err := api.Get(ctx, spec.Name, metav1.GetOptions{})
	if err == nil {
		if err := d.owned(existing, scope); err != nil {
			return "", err
		}
		if existing.Annotations[configAnnotation] == spec.Annotations[configAnnotation] && existing.DeletionTimestamp == nil && existing.Status.Phase != corev1.PodFailed && existing.Status.Phase != corev1.PodSucceeded {
			// Reusing a pod after an AT restart must also reconcile its external
			// dependencies. A healthy pod is not proof that its policy/PVCs still
			// belong to this controller or match current storage requirements.
			if err := d.ensureDependencies(ctx, scope, cfg); err != nil {
				return "", err
			}
			return d.waitReady(ctx, podHandle(existing))
		}
		if err := d.Remove(ctx, podHandle(existing)); err != nil {
			return "", err
		}
	} else if !apierrors.IsNotFound(err) {
		return "", fmt.Errorf("read sandbox pod: %w", err)
	}
	if err := d.ensureDependencies(ctx, scope, cfg); err != nil {
		return "", err
	}
	pod, err := api.Create(ctx, spec, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		pod, err = api.Get(ctx, spec.Name, metav1.GetOptions{})
		if err == nil {
			if err := d.owned(pod, scope); err != nil {
				return "", err
			}
			if pod.Annotations[configAnnotation] != spec.Annotations[configAnnotation] {
				return "", fmt.Errorf("concurrent sandbox configuration change; retry")
			}
		}
	}
	if err != nil {
		return "", fmt.Errorf("create sandbox pod: %w", err)
	}
	return d.waitReady(ctx, podHandle(pod))
}

func (d *Driver) ensureDependencies(ctx context.Context, scope string, cfg container.Config) error {
	if cfg.HomeScope != "" {
		if err := d.checkHomeMounts(ctx, scope, cfg.HomeScope); err != nil {
			return err
		}
		if err := d.ensurePVC(ctx, cfg.HomeScope, true, cfg); err != nil {
			return err
		}
	}
	if cfg.PersistentVolume {
		if err := d.ensurePVC(ctx, scope, false, cfg); err != nil {
			return err
		}
	}
	// Policy is installed before any user code can run.
	return d.ensurePolicy(ctx, scope, cfg.Network)
}

func (d *Driver) waitReady(ctx context.Context, handle string) (string, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		pod, err := d.pod(ctx, handle)
		if err != nil {
			return "", fmt.Errorf("wait for sandbox: %w", err)
		}
		if pod.DeletionTimestamp != nil || pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			return "", fmt.Errorf("sandbox pod terminated: %s %s", pod.Status.Reason, pod.Status.Message)
		}
		if ready(pod) {
			return handle, nil
		}
		for _, status := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
			if state := status.State.Waiting; state != nil && (state.Reason == "ImagePullBackOff" || state.Reason == "ErrImagePull" || state.Reason == "CreateContainerConfigError") {
				return "", fmt.Errorf("sandbox %s: %s", state.Reason, state.Message)
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("sandbox provisioning timed out or was cancelled: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func ready(pod *corev1.Pod) bool {
	if pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == sandboxContainer {
			return status.Ready
		}
	}
	return false
}

func (d *Driver) Running(ctx context.Context, handle string) bool {
	ctx, release, err := d.operation(ctx)
	if err != nil {
		return false
	}
	defer release()
	pod, err := d.pod(ctx, handle)
	return err == nil && ready(pod)
}

func (d *Driver) Stop(ctx context.Context, handle string) error { return d.Remove(ctx, handle) }

func (d *Driver) StopScope(ctx context.Context, scope string) error {
	ctx, release, err := d.operation(ctx)
	if err != nil {
		return err
	}
	defer release()
	pod, err := d.client.CoreV1().Pods(d.opts.Namespace).Get(ctx, d.name("space", scope), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read sandbox for stop: %w", err)
	}
	if err := d.owned(pod, scope); err != nil {
		return err
	}
	return d.removePod(ctx, podHandle(pod))
}

func (d *Driver) Remove(ctx context.Context, handle string) error {
	ctx, release, err := d.operation(ctx)
	if err != nil {
		return err
	}
	defer release()
	return d.removePod(ctx, handle)
}

func (d *Driver) removePod(ctx context.Context, handle string) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	pod, err := d.pod(ctx, handle)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	uid := pod.UID
	// Respect the pod's termination grace. Force deletion can discard the API
	// object while the old sandbox still runs, allowing a replacement too soon.
	if err := d.client.CoreV1().Pods(d.opts.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete sandbox pod: %w", err)
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, err := d.client.CoreV1().Pods(d.opts.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("wait for pod deletion: %w", err)
		}
		if current.UID != uid {
			return fmt.Errorf("sandbox was replaced during deletion; refusing to continue")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (d *Driver) Purge(ctx context.Context, scope string) error {
	ctx, release, err := d.operation(ctx)
	if err != nil {
		return err
	}
	defer release()
	pod, err := d.client.CoreV1().Pods(d.opts.Namespace).Get(ctx, d.name("space", scope), metav1.GetOptions{})
	if err == nil {
		if err := d.owned(pod, scope); err != nil {
			return err
		}
		if err := d.Remove(ctx, podHandle(pod)); err != nil {
			return err
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("read sandbox for purge: %w", err)
	}
	if err := d.deletePVC(ctx, scope, false); err != nil {
		return err
	}
	return d.deletePolicy(ctx, scope)
}
