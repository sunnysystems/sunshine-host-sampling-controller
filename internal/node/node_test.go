package node

import "testing"

func TestMatchSelector(t *testing.T) {
	labels := map[string]string{"capacity-type": "spot", "env": "prod"}
	cases := []struct {
		selector string
		want     bool
	}{
		{"capacity-type=spot", true},
		{"capacity-type=on-demand", false},
		{"env=prod", true},
		{"missing=x", false},
		{"no-equals", false},
		{"=spot", false},
		{"capacity-type=", false},
	}
	for _, c := range cases {
		if got := MatchSelector(labels, c.selector); got != c.want {
			t.Errorf("MatchSelector(%q) = %v, want %v", c.selector, got, c.want)
		}
	}
}

func TestMatchAnySelector(t *testing.T) {
	labels := map[string]string{"karpenter_nodepool": "high-cpu"}
	if !MatchAnySelector(labels, []string{"karpenter_nodepool=default", "karpenter_nodepool=high-cpu"}) {
		t.Error("want match on the second selector")
	}
	if MatchAnySelector(labels, []string{"karpenter_nodepool=default"}) {
		t.Error("want no match")
	}
	if MatchAnySelector(labels, nil) {
		t.Error("empty selector list must match nothing (fail-open)")
	}
}

func TestClassify(t *testing.T) {
	nodes := []Node{
		{Name: "a", Labels: map[string]string{"capacity-type": "on-demand"}},
		{Name: "b", Labels: map[string]string{"capacity-type": "spot"}},
		{Name: "c", Labels: map[string]string{"capacity-type": "spot"}},
		{Name: "d", Labels: map[string]string{"role": "other"}}, // untracked
	}
	pools := Classify(nodes, "capacity-type=on-demand", []string{"capacity-type=spot"})
	if len(pools.Stable) != 1 || pools.Stable[0].Name != "a" {
		t.Errorf("stable = %+v, want [a]", pools.Stable)
	}
	if len(pools.Surge) != 2 {
		t.Errorf("surge = %+v, want 2 nodes", pools.Surge)
	}
}

// The typical Karpenter shape: several nodepools, more than one of them surge.
// Before the list, only one pool could be named and the rest stayed fully
// monitored — lost savings, silently.
func TestClassify_multipleSurgePools(t *testing.T) {
	nodes := []Node{
		{Name: "a", Labels: map[string]string{"karpenter_nodepool": "default"}},
		{Name: "b", Labels: map[string]string{"karpenter_nodepool": "high-cpu"}},
		{Name: "c", Labels: map[string]string{"karpenter_nodepool": "high-memory"}},
		{Name: "d", Labels: map[string]string{"karpenter_nodepool": "burst-arm64"}},
	}
	pools := Classify(nodes, "", []string{
		"karpenter_nodepool=high-cpu",
		"karpenter_nodepool=burst-arm64",
	})
	if len(pools.Surge) != 2 {
		t.Errorf("surge = %+v, want b and d", pools.Surge)
	}
	// Derived stable: everything not surge, with nothing declared permanent.
	if len(pools.Stable) != 2 {
		t.Errorf("stable = %+v, want a and c derived", pools.Stable)
	}
}

// An explicit stable selector only narrows REPORTING — it must never pull a node
// out of surge, or naming a pool "permanent" would be a way to silently unsample.
func TestClassify_explicitStableNarrowsReportingOnly(t *testing.T) {
	nodes := []Node{
		{Name: "a", Labels: map[string]string{"pool": "fixed"}},
		{Name: "b", Labels: map[string]string{"pool": "spot"}},
		{Name: "c", Labels: map[string]string{"pool": "unlabelled-ish"}},
	}
	pools := Classify(nodes, "pool=fixed", []string{"pool=spot"})
	if len(pools.Surge) != 1 || pools.Surge[0].Name != "b" {
		t.Errorf("surge = %+v, want [b]", pools.Surge)
	}
	if len(pools.Stable) != 1 || pools.Stable[0].Name != "a" {
		t.Errorf("stable = %+v, want [a] only — c is untracked", pools.Stable)
	}
}

// An older config could name the same pool as both permanent and temporary. The
// server refuses that now; the controller still has to resolve it the safe,
// predictable way rather than by map iteration order.
func TestClassify_overlapResolvesToSurge(t *testing.T) {
	nodes := []Node{{Name: "a", Labels: map[string]string{"pool": "high-memory"}}}
	pools := Classify(nodes, "pool=high-memory", []string{"pool=high-memory"})
	if len(pools.Surge) != 1 || len(pools.Stable) != 0 {
		t.Errorf("overlap must resolve to surge, got %+v", pools)
	}
}

func TestClassify_emptySelectorsMatchNothing(t *testing.T) {
	nodes := []Node{{Name: "a", Labels: map[string]string{"x": "y"}}}
	pools := Classify(nodes, "", nil)
	if len(pools.Stable) != 0 || len(pools.Surge) != 0 {
		t.Errorf("empty selectors must classify nothing (fail-open), got %+v", pools)
	}
}
