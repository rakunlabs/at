package sandboxkube

import (
	"context"
	"fmt"
	"net/netip"

	corev1 "k8s.io/api/core/v1"
	networkv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func (d *Driver) policy(scope string, network bool) *networkv1.NetworkPolicy {
	p := &networkv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: d.name("network", scope), Namespace: d.opts.Namespace, Labels: d.labels(scope)},
		Spec: networkv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: d.labels(scope)}, PolicyTypes: []networkv1.PolicyType{networkv1.PolicyTypeIngress, networkv1.PolicyTypeEgress}}}
	if !network {
		return p
	}
	blocked := []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4", "240.0.0.0/4",
		"::/128", "::1/128", "::ffff:0:0/96", "fc00::/7", "fe80::/10", "ff00::/8"}
	blocked = append(blocked, d.opts.BlockedCIDRs...)
	v4, v6 := []string{}, []string{}
	for _, cidr := range blocked {
		prefix, _ := netip.ParsePrefix(cidr)
		if prefix.Addr().Is4() {
			v4 = append(v4, cidr)
		} else {
			v6 = append(v6, cidr)
		}
	}
	dns := networkv1.NetworkPolicyEgressRule{To: []networkv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"}}, PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"k8s-app": "kube-dns"}}}},
		Ports: []networkv1.NetworkPolicyPort{{Protocol: ptr(corev1.ProtocolUDP), Port: ptr(intstr.FromInt32(53))}, {Protocol: ptr(corev1.ProtocolTCP), Port: ptr(intstr.FromInt32(53))}}}
	p.Spec.Egress = []networkv1.NetworkPolicyEgressRule{dns, {To: []networkv1.NetworkPolicyPeer{{IPBlock: &networkv1.IPBlock{CIDR: "0.0.0.0/0", Except: v4}}, {IPBlock: &networkv1.IPBlock{CIDR: "::/0", Except: v6}}}}}
	return p
}

func (d *Driver) ensurePolicy(ctx context.Context, scope string, network bool) error {
	want := d.policy(scope, network)
	api := d.client.NetworkingV1().NetworkPolicies(d.opts.Namespace)
	current, err := api.Get(ctx, want.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = api.Create(ctx, want, metav1.CreateOptions{})
		if !apierrors.IsAlreadyExists(err) {
			if err != nil {
				return fmt.Errorf("create sandbox network policy: %w", err)
			}
			return nil
		}
		current, err = api.Get(ctx, want.Name, metav1.GetOptions{})
	}
	if err != nil {
		return fmt.Errorf("read sandbox network policy: %w", err)
	}
	if err := d.owned(current, scope); err != nil {
		return err
	}
	want.ResourceVersion = current.ResourceVersion
	if _, err := api.Update(ctx, want, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update sandbox network policy: %w", err)
	}
	return nil
}

func (d *Driver) deletePolicy(ctx context.Context, scope string) error {
	api := d.client.NetworkingV1().NetworkPolicies(d.opts.Namespace)
	p, err := api.Get(ctx, d.name("network", scope), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read sandbox policy for deletion: %w", err)
	}
	if err := d.owned(p, scope); err != nil {
		return err
	}
	uid := p.UID
	if err := api.Delete(ctx, p.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete sandbox policy: %w", err)
	}
	return nil
}
