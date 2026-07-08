// Package actuator applies (or, in dry-run, reports) the sampling decision.
package actuator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/node"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/planner"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/policy"
)

// LabelSampledOut is the node label the controller writes to pull the Datadog
// agent off a surge node. The customer's agent DaemonSet must carry an inverted
// nodeAffinity keyed on this label (operator NotIn ["true"]) — see the chart
// README. Fail-open by construction: a node WITHOUT the label is monitored, so
// doing nothing keeps full coverage.
const (
	LabelSampledOut = "datadog.sunshine/sampled-out"
	labelValueTrue  = "true"
)

// Result summarises what an Apply did (for metrics/audit). All zero for DryRun.
type Result struct {
	Applied int // labels newly written (sampled-out=true)
	Cleared int // stale/orphan labels removed
	Errors  int // per-node patch failures
}

// Actuator carries out a decision. DryRun reports only; LabelActuator writes the
// sampled-out label when the served policy authorises it (mode == "active").
type Actuator interface {
	// Apply reconciles node labels toward the decision. It receives the full
	// node list so it can remove stale sampled-out labels (orphan cleanup) from
	// nodes no longer in the plan.
	Apply(ctx context.Context, dec planner.Decision, p policy.Policy, nodes []node.Node) (Result, error)
}

// DryRun logs what would happen and never touches the cluster.
type DryRun struct {
	Log *slog.Logger
}

// Apply reports the plan. It returns a zero Result and nil always — there is
// nothing to fail.
func (a DryRun) Apply(_ context.Context, dec planner.Decision, p policy.Policy, _ []node.Node) (Result, error) {
	a.Log.Info("dry-run: no cluster changes",
		"configured", p.Configured,
		"mode", p.Spec.Mode,
		"budget", dec.Budget,
		"monitored", len(dec.Monitored),
		"wouldSampleOut", len(dec.SampledOut),
		"sampleOutNodes", dec.SampledOut,
	)
	return Result{}, nil
}

// Labeler writes/removes a single node label. Implemented by kube.Labeler; kept
// as an interface here so the actuator stays client-go-free and unit-testable.
type Labeler interface {
	SetLabel(ctx context.Context, nodeName, key, value string) error
	RemoveLabel(ctx context.Context, nodeName, key string) error
}

// LabelActuator writes the sampled-out label. Actuation is gated on the served
// policy mode: it labels nodes only when p.Spec.Mode == "active" (the server's
// double-lock downgrades to dry_run unless execute is authorised). When not
// active it removes ANY existing sampled-out label — so pausing (mode → dry_run)
// restores full monitoring on the next tick.
type LabelActuator struct {
	Labeler Labeler
	Log     *slog.Logger
}

// Apply reconciles the cluster's sampled-out labels toward the desired set:
// the plan's SampledOut names when actuation is authorised, else the empty set.
// Nodes needing a label get one; nodes carrying a stale label get it removed.
func (a LabelActuator) Apply(ctx context.Context, dec planner.Decision, p policy.Policy, nodes []node.Node) (Result, error) {
	desired := make(map[string]bool)
	actuate := p.Configured && p.Spec.Mode == "active"
	if actuate {
		for _, name := range dec.SampledOut {
			desired[name] = true
		}
	}

	var res Result
	var errs []error
	for _, n := range nodes {
		labeled := n.Labels[LabelSampledOut] == labelValueTrue
		want := desired[n.Name]
		switch {
		case want && !labeled:
			if err := a.Labeler.SetLabel(ctx, n.Name, LabelSampledOut, labelValueTrue); err != nil {
				res.Errors++
				errs = append(errs, fmt.Errorf("label %s: %w", n.Name, err))
				continue
			}
			res.Applied++
		case !want && labeled:
			if err := a.Labeler.RemoveLabel(ctx, n.Name, LabelSampledOut); err != nil {
				res.Errors++
				errs = append(errs, fmt.Errorf("unlabel %s: %w", n.Name, err))
				continue
			}
			res.Cleared++
		}
	}

	a.Log.Info("host-sampling: reconciled labels",
		"mode", p.Spec.Mode,
		"actuate", actuate,
		"desired", len(desired),
		"applied", res.Applied,
		"cleared", res.Cleared,
		"errors", res.Errors,
	)
	return res, errors.Join(errs...)
}
