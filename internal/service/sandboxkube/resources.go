package sandboxkube

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/rakunlabs/at/internal/sandboxruntime"
	"github.com/rakunlabs/at/internal/service/container"
)

func ptr[T any](v T) *T { return &v }

func (d *Driver) podSpec(scope string, cfg container.Config) (*corev1.Pod, error) {
	cpu := cfg.CPU
	if cpu == "" {
		cpu = "2"
	}
	cpuLimit, err := resource.ParseQuantity(cpu)
	if err != nil || cpuLimit.Sign() <= 0 {
		return nil, fmt.Errorf("invalid CPU limit %q", cpu)
	}
	memory, err := memoryQuantity(cfg.Memory)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(struct {
		Sandbox container.Config
		Options any
	}{cfg, d.opts})
	if err != nil {
		return nil, fmt.Errorf("hash sandbox configuration: %w", err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: d.name("space", scope), Namespace: d.opts.Namespace,
		Labels: d.labels(scope), Annotations: map[string]string{configAnnotation: hash(string(encoded))}},
		Spec: corev1.PodSpec{AutomountServiceAccountToken: ptr(false), EnableServiceLinks: ptr(false), RestartPolicy: corev1.RestartPolicyNever,
			TerminationGracePeriodSeconds: ptr[int64](5), SecurityContext: &corev1.PodSecurityContext{SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
			NodeSelector: map[string]string{"kubernetes.io/os": "linux"}}}
	if d.opts.RuntimeClass != "" {
		pod.Spec.RuntimeClassName = ptr(d.opts.RuntimeClass)
	}
	sc := &corev1.SecurityContext{AllowPrivilegeEscalation: ptr(false), Privileged: ptr(false), ReadOnlyRootFilesystem: ptr(cfg.ReadOnlyRoot), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}}
	for _, cap := range cfg.CapAdd {
		sc.Capabilities.Add = append(sc.Capabilities.Add, corev1.Capability(cap))
	}
	helperLimit := resource.MustParse("32Mi")
	tmpLimit := resource.MustParse("256Mi")
	pod.Spec.Volumes = []corev1.Volume{
		{Name: "helpers", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &helperLimit}}},
		{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &tmpLimit}}},
	}
	workspace := corev1.Volume{Name: "workspace"}
	if cfg.PersistentVolume {
		workspace.PersistentVolumeClaim = &corev1.PersistentVolumeClaimVolumeSource{ClaimName: d.name("data", scope)}
	} else {
		workspace.EmptyDir = &corev1.EmptyDirVolumeSource{}
		if cfg.DiskLimitBytes > 0 {
			workspace.EmptyDir.SizeLimit = resource.NewQuantity(cfg.DiskLimitBytes, resource.BinarySI)
		}
	}
	pod.Spec.Volumes = append(pod.Spec.Volumes, workspace)
	// The init container copies static helpers without executing anything in
	// the user's image. The launcher/helper volume is read-only in that image.
	pod.Spec.InitContainers = []corev1.Container{{Name: "at-runtime", Image: d.opts.HelperImage, Command: []string{"/at-sandbox", "init", sandboxruntime.Directory},
		SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: ptr(false), ReadOnlyRootFilesystem: ptr(true), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}},
		VolumeMounts:    []corev1.VolumeMount{{Name: "helpers", MountPath: sandboxruntime.Directory}},
		Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("16Mi")},
			Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("64Mi"), corev1.ResourceEphemeralStorage: resource.MustParse("64Mi")}}}}
	limits := corev1.ResourceList{corev1.ResourceCPU: cpuLimit, corev1.ResourceMemory: memory, corev1.ResourceEphemeralStorage: resource.MustParse("1Gi")}
	user := corev1.Container{Name: sandboxContainer, Image: cfg.Image, SecurityContext: sc, WorkingDir: "/workspace",
		Resources:    corev1.ResourceRequirements{Requests: limits.DeepCopy(), Limits: limits},
		VolumeMounts: []corev1.VolumeMount{{Name: "helpers", MountPath: sandboxruntime.Directory, ReadOnly: true}, {Name: "tmp", MountPath: "/tmp"}, {Name: "workspace", MountPath: "/workspace"}}}
	if cfg.KeepAlive {
		user.Command = []string{sandboxruntime.Launcher, "idle"}
	}
	if cfg.HomeScope != "" {
		pod.Labels[homeLabel] = hash(cfg.HomeScope)
		pod.Annotations[homePathAnnotation] = cfg.HomePath
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{Name: "home", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: d.name("home", cfg.HomeScope)}}})
		user.VolumeMounts = append(user.VolumeMounts, corev1.VolumeMount{Name: "home", MountPath: cfg.HomePath})
		user.Env = append(user.Env, corev1.EnvVar{Name: "HOME", Value: cfg.HomePath})
	}
	pod.Spec.Containers = []corev1.Container{user}
	return pod, nil
}

func (d *Driver) pvc(scope string, home bool, cfg container.Config) *corev1.PersistentVolumeClaim {
	kind, size, class, mode := "data", d.opts.WorkspaceSize, d.opts.StorageClass, corev1.ReadWriteOnce
	if home {
		kind, size, class, mode = "home", d.opts.HomeSize, d.opts.HomeStorageClass, corev1.PersistentVolumeAccessMode(d.opts.HomeAccessMode)
	}
	quantity := resource.MustParse(size)
	if !home && cfg.DiskLimitBytes > quantity.Value() {
		quantity = *resource.NewQuantity(cfg.DiskLimitBytes, resource.BinarySI)
	}
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: d.name(kind, scope), Namespace: d.opts.Namespace, Labels: d.labels(scope)},
		Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{mode}, VolumeMode: ptr(corev1.PersistentVolumeFilesystem), Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: quantity}}}}
	if class != "" {
		pvc.Spec.StorageClassName = ptr(class)
	}
	return pvc
}

func (d *Driver) ensurePVC(ctx context.Context, scope string, home bool, cfg container.Config) error {
	want := d.pvc(scope, home, cfg)
	api := d.client.CoreV1().PersistentVolumeClaims(d.opts.Namespace)
	current, err := api.Get(ctx, want.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = api.Create(ctx, want, metav1.CreateOptions{})
		if !apierrors.IsAlreadyExists(err) {
			if err != nil {
				return fmt.Errorf("create persistent volume: %w", err)
			}
			return nil
		}
		current, err = api.Get(ctx, want.Name, metav1.GetOptions{})
	}
	if err != nil {
		return fmt.Errorf("read persistent volume: %w", err)
	}
	if err := d.owned(current, scope); err != nil {
		return err
	}
	if current.DeletionTimestamp != nil {
		return fmt.Errorf("persistent volume is being deleted; retry after cleanup")
	}
	if len(current.Spec.AccessModes) != 1 || current.Spec.AccessModes[0] != want.Spec.AccessModes[0] {
		return fmt.Errorf("persistent volume access mode changed; explicit storage migration required")
	}
	if want.Spec.StorageClassName != nil && (current.Spec.StorageClassName == nil || *current.Spec.StorageClassName != *want.Spec.StorageClassName) {
		return fmt.Errorf("persistent volume storage class changed; explicit storage migration required")
	}
	actual := current.Spec.Resources.Requests[corev1.ResourceStorage]
	wanted := want.Spec.Resources.Requests[corev1.ResourceStorage]
	if wanted.Cmp(actual) > 0 {
		current.Spec.Resources.Requests[corev1.ResourceStorage] = wanted
		if _, err := api.Update(ctx, current, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("expand persistent volume (storage class must support expansion): %w", err)
		}
	}
	return nil
}

func (d *Driver) deletePVC(ctx context.Context, scope string, home bool) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	want := d.pvc(scope, home, container.Config{})
	api := d.client.CoreV1().PersistentVolumeClaims(d.opts.Namespace)
	pvc, err := api.Get(ctx, want.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read PVC for deletion: %w", err)
	}
	if err := d.owned(pvc, scope); err != nil {
		return err
	}
	uid := pvc.UID
	if err := api.Delete(ctx, pvc.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete PVC: %w", err)
	}
	// Accepted deletion is not completed deletion. Preserve Manager tracking
	// and report a retryable failure when protection/CSI finalizers delay it.
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, err := api.Get(ctx, pvc.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("wait for PVC deletion: %w", err)
		}
		if current.UID != uid {
			return fmt.Errorf("PVC was replaced during deletion; refusing to continue")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("PVC deletion is still pending; inspect storage finalizers before retrying: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (d *Driver) checkHomeMounts(ctx context.Context, scope, homeScope string) error {
	if d.opts.HomeAccessMode == string(corev1.ReadWriteMany) {
		return nil
	}
	pods, err := d.client.CoreV1().Pods(d.opts.Namespace).List(ctx, metav1.ListOptions{LabelSelector: labels.Set{managedLabel: d.opts.DeploymentID, homeLabel: hash(homeScope)}.String()})
	if err != nil {
		return fmt.Errorf("check persistent home mounts: %w", err)
	}
	for _, pod := range pods.Items {
		if pod.Name != d.name("space", scope) {
			return fmt.Errorf("ReadWriteOnce home is already mounted by another space; stop it first or configure ReadWriteMany storage")
		}
	}
	return nil
}

func (d *Driver) RemoveHome(ctx context.Context, scope string) error {
	ctx, release, err := d.operation(ctx)
	if err != nil {
		return err
	}
	defer release()
	if scope == "" {
		return fmt.Errorf("home scope is required")
	}
	pods, err := d.client.CoreV1().Pods(d.opts.Namespace).List(ctx, metav1.ListOptions{LabelSelector: labels.Set{managedLabel: d.opts.DeploymentID, homeLabel: hash(scope)}.String()})
	if err != nil {
		return fmt.Errorf("list persistent home mounts: %w", err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		// Labels alone are not evidence of mounting this exact PVC.
		mounts := false
		for _, vol := range pod.Spec.Volumes {
			if vol.PersistentVolumeClaim != nil && vol.PersistentVolumeClaim.ClaimName == d.name("home", scope) {
				mounts = true
			}
		}
		if !mounts {
			return fmt.Errorf("home mount metadata disagrees with pod volume; refusing deletion")
		}
		if err := d.Remove(ctx, podHandle(pod)); err != nil {
			return err
		}
	}
	return d.deletePVC(ctx, scope, true)
}
