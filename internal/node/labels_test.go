package node

import (
	"encoding/json"
	"reflect"
	"testing"
)

func nodeWith(name string, labels map[string]string) Node {
	return Node{Name: name, Labels: labels}
}

func TestSummarizeLabels_groupsDistinctValuesPerKey(t *testing.T) {
	got := SummarizeLabels([]Node{
		nodeWith("a", map[string]string{"karpenter.sh/nodepool": "high-cpu", "env": "prod"}),
		nodeWith("b", map[string]string{"karpenter.sh/nodepool": "default", "env": "prod"}),
		nodeWith("c", map[string]string{"karpenter.sh/nodepool": "high-cpu"}),
	})

	want := []LabelSummary{
		{Key: "env", Values: []string{"prod"}},
		{Key: "karpenter.sh/nodepool", Values: []string{"default", "high-cpu"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// The whole point of the feature: the key reaches Sunshine in its RAW
// Kubernetes form. Datadog reports the same label as `karpenter_nodepool`, and a
// selector built from that form matches no node
// (sunnysystems-sunshine#645).
func TestSummarizeLabels_keepsRawKubernetesKeys(t *testing.T) {
	got := SummarizeLabels([]Node{
		nodeWith("a", map[string]string{"karpenter.sh/nodepool": "high-cpu"}),
	})
	if len(got) != 1 || got[0].Key != "karpenter.sh/nodepool" {
		t.Fatalf("key must survive verbatim, got %+v", got)
	}
}

func TestSummarizeLabels_dropsHighCardinalityKeys(t *testing.T) {
	// A per-node identity label cannot describe a pool, and enumerating it would
	// blow up the payload on a large fleet.
	nodes := make([]Node, maxValuesPerLabel+5)
	for i := range nodes {
		nodes[i] = nodeWith("n", map[string]string{
			"kubernetes.io/hostname": string(rune('a' + i)),
			"pool":                   "fixed",
		})
	}
	got := SummarizeLabels(nodes)
	for _, l := range got {
		if l.Key == "kubernetes.io/hostname" {
			t.Fatalf("high-cardinality key must be dropped, got %+v", got)
		}
	}
	if len(got) != 1 || got[0].Key != "pool" {
		t.Fatalf("got %+v, want only the pool key", got)
	}
}

func TestSummarizeLabels_isDeterministic(t *testing.T) {
	// An unchanged fleet must produce a byte-identical summary every reconcile —
	// otherwise each tick writes a different audit row for the same reality.
	nodes := []Node{
		nodeWith("a", map[string]string{"z": "2", "a": "1"}),
		nodeWith("b", map[string]string{"z": "1", "a": "2"}),
	}
	first, _ := json.Marshal(SummarizeLabels(nodes))
	for i := 0; i < 20; i++ {
		next, _ := json.Marshal(SummarizeLabels(nodes))
		if string(first) != string(next) {
			t.Fatalf("non-deterministic: %s vs %s", first, next)
		}
	}
}

func TestSummarizeLabels_emptyFleetMarshalsAsArray(t *testing.T) {
	// Never nil: `null` would read as "this controller cannot report labels",
	// which is a different answer from "there are none". Same null-vs-empty trap
	// as sunnysystems-sunshine#641.
	b, err := json.Marshal(SummarizeLabels(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "[]" {
		t.Fatalf("got %s, want []", b)
	}
}

func TestSummarizeLabels_capsKeyCount(t *testing.T) {
	labels := map[string]string{}
	for i := 0; i < maxLabelKeys+20; i++ {
		labels[string(rune('A'+i%26))+string(rune('a'+i/26))] = "v"
	}
	if got := len(SummarizeLabels([]Node{nodeWith("a", labels)})); got > maxLabelKeys {
		t.Fatalf("key count = %d, want <= %d", got, maxLabelKeys)
	}
}
