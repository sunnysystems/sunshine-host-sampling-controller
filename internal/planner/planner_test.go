package planner

import (
	"testing"
	"time"

	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/node"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/policy"
)

func configured(pct float64, floor int) policy.Policy {
	return policy.Policy{Configured: true, Spec: policy.Spec{SurgeSamplePct: pct, FloorNodes: floor}}
}

// surgeNodes returns n nodes named s0..s(n-1) with strictly increasing ages
// (s0 is the oldest).
func surgeNodes(n int) []node.Node {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	out := make([]node.Node, n)
	for i := 0; i < n; i++ {
		out[i] = node.Node{Name: "s" + string(rune('0'+i)), CreatedAt: base.Add(time.Duration(i) * time.Hour)}
	}
	return out
}

func TestPlan_unconfiguredIsEmpty(t *testing.T) {
	dec := Plan(surgeNodes(10), policy.Policy{Configured: false})
	if len(dec.Monitored) != 0 || len(dec.SampledOut) != 0 {
		t.Fatalf("unconfigured policy must yield an empty plan, got %+v", dec)
	}
}

func TestPlan_fullSamplePctKeepsEverything(t *testing.T) {
	dec := Plan(surgeNodes(10), configured(100, 3))
	if dec.Budget != 10 || len(dec.Monitored) != 10 || len(dec.SampledOut) != 0 {
		t.Fatalf("pct=100 must keep all 10 monitored, got %+v", dec)
	}
}

func TestPlan_budgetAndSampleOut(t *testing.T) {
	// 10 surge, 40% → ceil(4) = 4 monitored, 6 sampled out.
	dec := Plan(surgeNodes(10), configured(40, 3))
	if dec.Budget != 4 {
		t.Fatalf("budget = %d, want 4", dec.Budget)
	}
	if len(dec.Monitored) != 4 || len(dec.SampledOut) != 6 {
		t.Fatalf("monitored=%d sampledOut=%d, want 4/6", len(dec.Monitored), len(dec.SampledOut))
	}
	// Oldest-first: the 4 oldest (s0..s3) stay monitored.
	want := []string{"s0", "s1", "s2", "s3"}
	for i, name := range want {
		if dec.Monitored[i] != name {
			t.Fatalf("monitored[%d] = %s, want %s (oldest-first)", i, dec.Monitored[i], name)
		}
	}
}

func TestPlan_floorRaisesBudget(t *testing.T) {
	// 10 surge, 10% → ceil(1) = 1, but floor 3 wins.
	dec := Plan(surgeNodes(10), configured(10, 3))
	if dec.Budget != 3 || len(dec.Monitored) != 3 {
		t.Fatalf("floor must raise budget to 3, got budget=%d monitored=%d", dec.Budget, len(dec.Monitored))
	}
}

func TestPlan_budgetNeverExceedsSurge(t *testing.T) {
	// floor 8 but only 5 surge nodes → budget capped at 5, nothing sampled.
	dec := Plan(surgeNodes(5), configured(10, 8))
	if dec.Budget != 5 || len(dec.SampledOut) != 0 {
		t.Fatalf("budget must cap at surge total, got %+v", dec)
	}
}

func TestPlan_emptySurge(t *testing.T) {
	dec := Plan(nil, configured(40, 3))
	if len(dec.Monitored) != 0 || len(dec.SampledOut) != 0 {
		t.Fatalf("no surge nodes → empty plan, got %+v", dec)
	}
}
