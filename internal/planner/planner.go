// Package planner computes the sampling decision — which surge nodes to keep
// monitored and which would be sampled out. Pure and deterministic (no k8s, no
// IO), so it is the unit-tested heart of the controller.
package planner

import (
	"math"
	"sort"

	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/node"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/policy"
)

// Decision is the plan for one reconcile.
type Decision struct {
	// Budget is the number of surge nodes to keep monitored.
	Budget int
	// Monitored / SampledOut are surge node names (SampledOut = "would get the
	// sampled-out label" — never applied in dry-run).
	Monitored  []string
	SampledOut []string
}

// Plan decides which surge nodes stay monitored.
//
//   - Unconfigured policy → empty plan (fail-open: monitor everything).
//   - budget = max(floorNodes, ceil(surgeTotal * surgeSamplePct/100)), capped at
//     the surge total. surgeSamplePct=100 → budget=total → nothing sampled.
//   - Keep the OLDEST `budget` surge nodes (stable membership, no flapping); the
//     newest — the ephemeral spot/burst nodes — are the ones sampled out.
func Plan(surge []node.Node, p policy.Policy) Decision {
	if !p.Configured {
		return Decision{}
	}

	total := len(surge)
	budget := budgetFor(total, p.Spec.SurgeSamplePct, p.Spec.FloorNodes)
	if total == 0 {
		return Decision{Budget: budget}
	}

	sorted := make([]node.Node, total)
	copy(sorted, surge)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].Name < sorted[j].Name // deterministic tie-break
		}
		return sorted[i].CreatedAt.Before(sorted[j].CreatedAt)
	})

	dec := Decision{Budget: budget}
	for i, n := range sorted {
		if i < budget {
			dec.Monitored = append(dec.Monitored, n.Name)
		} else {
			dec.SampledOut = append(dec.SampledOut, n.Name)
		}
	}
	return dec
}

func budgetFor(total int, pct float64, floor int) int {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	budget := int(math.Ceil(float64(total) * pct / 100.0))
	if budget < floor {
		budget = floor
	}
	if budget > total {
		budget = total
	}
	return budget
}
