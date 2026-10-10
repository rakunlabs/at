package sandboxkube

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// stopManagedPods ends leftover workloads before this controller accepts new
// work, and after its streams drain on shutdown. Storage and policies survive.
// It requires an acquired exclusive namespace Lease, not merely matching labels.
func (d *Driver) stopManagedPods(ctx context.Context) error {
	pods, err := d.client.CoreV1().Pods(d.opts.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.Set{managedLabel: d.opts.DeploymentID}.String(),
	})
	if err != nil {
		return fmt.Errorf("list managed sandbox pods: %w", err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if err := d.owned(pod, ""); err != nil {
			return err
		}
		if !strings.HasPrefix(pod.Name, "at-space-") || pod.Labels[scopeLabel] == "" || pod.Annotations[configAnnotation] == "" {
			return fmt.Errorf("refusing unrecognized managed pod %q during controller recovery", pod.Name)
		}
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		// Cleanup runs while the local gate is closed at shutdown; explicitly
		// recheck the Lease rather than bypassing ownership at the API boundary.
		if d.ownership != nil {
			lease, err := d.ownership.leases.Get(ctx, controllerLeaseName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("check cleanup ownership: %w", err)
			}
			if err := d.ownership.verify(lease); err != nil {
				return err
			}
		}
		if err := d.removePod(ctx, podHandle(pod)); err != nil {
			return err
		}
	}
	return nil
}
