package kube

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

const sampledOut = "datadog.sunshine/sampled-out"

func mkNode(name string, labels map[string]string, created time.Time) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Labels:            labels,
			CreationTimestamp: metav1.NewTime(created),
		},
	}
}

func TestLister_mapsNodes(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	client := fake.NewSimpleClientset(
		mkNode("a", map[string]string{"capacity-type": "spot"}, created),
		mkNode("b", nil, created),
	)
	out, err := NewLister(client).ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d nodes, want 2", len(out))
	}
	byName := map[string]bool{out[0].Name: true, out[1].Name: true}
	if !byName["a"] || !byName["b"] {
		t.Fatalf("unexpected node names: %+v", out)
	}
}

func TestLabeler_setAndRemove(t *testing.T) {
	client := fake.NewSimpleClientset(
		mkNode("surge-1", map[string]string{"capacity-type": "spot"}, time.Now()),
	)
	l := NewLabeler(client)
	ctx := context.Background()

	if err := l.SetLabel(ctx, "surge-1", sampledOut, "true"); err != nil {
		t.Fatalf("SetLabel: %v", err)
	}
	got, err := client.CoreV1().Nodes().Get(ctx, "surge-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Labels[sampledOut] != "true" {
		t.Fatalf("label not set, labels=%v", got.Labels)
	}
	// The original label must be preserved by the strategic-merge patch.
	if got.Labels["capacity-type"] != "spot" {
		t.Fatalf("existing label clobbered, labels=%v", got.Labels)
	}

	if err := l.RemoveLabel(ctx, "surge-1", sampledOut); err != nil {
		t.Fatalf("RemoveLabel: %v", err)
	}
	got, err = client.CoreV1().Nodes().Get(ctx, "surge-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, ok := got.Labels[sampledOut]; ok {
		t.Fatalf("label not removed, labels=%v", got.Labels)
	}
}

func agentDaemonSet(affinity *corev1.Affinity) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "datadog-agent", Namespace: "datadog"},
		Spec: appsv1.DaemonSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Affinity: affinity},
			},
		},
	}
}

func sampledOutAffinity(op corev1.NodeSelectorOperator, values []string) *corev1.Affinity {
	return &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key:      sampledOut,
						Operator: op,
						Values:   values,
					}},
				}},
			},
		},
	}
}

func TestAffinityChecker(t *testing.T) {
	cases := []struct {
		name string
		aff  *corev1.Affinity
		want bool
	}{
		{"NotIn true present", sampledOutAffinity(corev1.NodeSelectorOpNotIn, []string{"true"}), true},
		{"DoesNotExist present", sampledOutAffinity(corev1.NodeSelectorOpDoesNotExist, nil), true},
		{"wrong operator (In)", sampledOutAffinity(corev1.NodeSelectorOpIn, []string{"true"}), false},
		{"no affinity", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewSimpleClientset(agentDaemonSet(tc.aff))
			got, err := NewAffinityChecker(client).HasSampledOutAntiAffinity(
				context.Background(), "datadog", "datadog-agent", sampledOut)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAffinityChecker_missingDaemonSet(t *testing.T) {
	client := fake.NewSimpleClientset()
	_, err := NewAffinityChecker(client).HasSampledOutAntiAffinity(
		context.Background(), "datadog", "datadog-agent", sampledOut)
	if err == nil {
		t.Fatal("expected an error when the DaemonSet does not exist")
	}
}
