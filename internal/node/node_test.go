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

func TestClassify(t *testing.T) {
	nodes := []Node{
		{Name: "a", Labels: map[string]string{"capacity-type": "on-demand"}},
		{Name: "b", Labels: map[string]string{"capacity-type": "spot"}},
		{Name: "c", Labels: map[string]string{"capacity-type": "spot"}},
		{Name: "d", Labels: map[string]string{"role": "other"}}, // untracked
	}
	pools := Classify(nodes, "capacity-type=on-demand", "capacity-type=spot")
	if len(pools.Stable) != 1 || pools.Stable[0].Name != "a" {
		t.Errorf("stable = %+v, want [a]", pools.Stable)
	}
	if len(pools.Surge) != 2 {
		t.Errorf("surge = %+v, want 2 nodes", pools.Surge)
	}
}

func TestClassify_emptySelectorsMatchNothing(t *testing.T) {
	nodes := []Node{{Name: "a", Labels: map[string]string{"x": "y"}}}
	pools := Classify(nodes, "", "")
	if len(pools.Stable) != 0 || len(pools.Surge) != 0 {
		t.Errorf("empty selectors must classify nothing (fail-open), got %+v", pools)
	}
}
