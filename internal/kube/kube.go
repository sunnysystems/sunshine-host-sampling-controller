// Package kube adapts the Kubernetes API to the controller's plain node model.
// This is the only internal package that imports client-go.
package kube

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/node"
)

// Lister reads nodes from the cluster.
type Lister struct {
	client kubernetes.Interface
}

// NewLister builds a Lister from a Kubernetes clientset.
func NewLister(client kubernetes.Interface) *Lister {
	return &Lister{client: client}
}

// ListNodes returns all cluster nodes as the controller's node model.
func (l *Lister) ListNodes(ctx context.Context) ([]node.Node, error) {
	list, err := l.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]node.Node, 0, len(list.Items))
	for i := range list.Items {
		n := &list.Items[i]
		out = append(out, node.Node{
			Name:      n.Name,
			Labels:    n.Labels,
			CreatedAt: n.CreationTimestamp.Time,
		})
	}
	return out, nil
}

// Labeler writes/removes a single node label via a strategic-merge patch. This
// is the ONLY write path to the cluster; it needs the `patch` verb on nodes
// (granted by the chart only when dryRun is false).
type Labeler struct {
	client kubernetes.Interface
}

// NewLabeler builds a Labeler from a Kubernetes clientset.
func NewLabeler(client kubernetes.Interface) *Labeler {
	return &Labeler{client: client}
}

// SetLabel adds or updates a label on the node.
func (l *Labeler) SetLabel(ctx context.Context, nodeName, key, value string) error {
	patch := []byte(fmt.Sprintf(`{"metadata":{"labels":{%q:%q}}}`, key, value))
	return l.patch(ctx, nodeName, patch)
}

// RemoveLabel removes a label from the node (a no-op if the key is absent). A
// null value in a strategic-merge patch deletes the map key.
func (l *Labeler) RemoveLabel(ctx context.Context, nodeName, key string) error {
	patch := []byte(fmt.Sprintf(`{"metadata":{"labels":{%q:null}}}`, key))
	return l.patch(ctx, nodeName, patch)
}

func (l *Labeler) patch(ctx context.Context, nodeName string, patch []byte) error {
	_, err := l.client.CoreV1().Nodes().Patch(
		ctx, nodeName, types.StrategicMergePatchType, patch, metav1.PatchOptions{},
	)
	return err
}

// AffinityChecker reads a DaemonSet to verify the enforcement contract.
type AffinityChecker struct {
	client kubernetes.Interface
}

// NewAffinityChecker builds an AffinityChecker from a Kubernetes clientset.
func NewAffinityChecker(client kubernetes.Interface) *AffinityChecker {
	return &AffinityChecker{client: client}
}

// HasSampledOutAntiAffinity reports whether the named DaemonSet's pod template
// carries a REQUIRED node anti-affinity on `key` that keeps its pods off
// sampled-out nodes — i.e. a matchExpression with operator NotIn (values
// containing "true") or DoesNotExist. Without this on the Datadog agent
// DaemonSet, writing the label has no effect (the agent keeps running → no
// savings). Preflight only: never mutates.
func (c *AffinityChecker) HasSampledOutAntiAffinity(ctx context.Context, namespace, name, key string) (bool, error) {
	ds, err := c.client.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	aff := ds.Spec.Template.Spec.Affinity
	if aff == nil || aff.NodeAffinity == nil ||
		aff.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		return false, nil
	}
	for _, term := range aff.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
		for _, expr := range term.MatchExpressions {
			if expr.Key != key {
				continue
			}
			switch expr.Operator {
			case corev1.NodeSelectorOpDoesNotExist:
				return true, nil
			case corev1.NodeSelectorOpNotIn:
				for _, v := range expr.Values {
					if v == "true" {
						return true, nil
					}
				}
			}
		}
	}
	return false, nil
}
