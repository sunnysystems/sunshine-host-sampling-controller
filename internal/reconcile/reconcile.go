// Package reconcile is the controller loop. It depends only on interfaces for
// policy + node listing, so it is unit-testable offline (no Kubernetes client).
package reconcile

import (
	"context"
	"log/slog"
	"time"

	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/actuator"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/metrics"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/node"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/planner"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/policy"
)

// PolicyFetcher fetches the current policy (fail-open on error).
type PolicyFetcher interface {
	Fetch(ctx context.Context) (policy.Policy, error)
}

// NodeLister lists the cluster's nodes.
type NodeLister interface {
	ListNodes(ctx context.Context) ([]node.Node, error)
}

// ReportInput is the per-reconcile summary the controller reports to Sunshine.
type ReportInput struct {
	Mode            string
	Actuated        bool
	MonitoredCount  int
	SampledOutCount int
	LabelsApplied   int
	LabelsCleared   int
	LabelErrors     int
	SampledNodes    []string
}

// Reporter ships the reconcile summary to Sunshine (best-effort; never blocks or
// fails the tick). Optional — nil disables reporting.
type Reporter interface {
	Report(ctx context.Context, in ReportInput)
}

// Reconciler runs one reconcile per tick.
type Reconciler struct {
	Policy   PolicyFetcher
	Nodes    NodeLister
	Actuator actuator.Actuator
	Metrics  *metrics.Registry
	Log      *slog.Logger
	// Reporter is optional; when set, each tick's summary is shipped to Sunshine.
	Reporter Reporter
	// ExecuteEnabled mirrors the local DRY_RUN=false switch — used only to label
	// a report as actuated (labels are still gated by the actuator + served mode).
	ExecuteEnabled bool
}

// Tick performs a single reconcile. It never panics or exits the process — a
// bad tick is logged and the next tick recovers.
func (r *Reconciler) Tick(ctx context.Context) {
	r.Metrics.IncTick()

	p, err := r.Policy.Fetch(ctx)
	if err != nil {
		r.Metrics.IncFetchError()
		// Fail open: an empty/unconfigured policy means "monitor everything".
		r.Log.Warn("policy fetch failed — failing open (monitoring everything)", "err", err)
	}
	r.Metrics.SetConfigured(p.Configured)

	nodes, err := r.Nodes.ListNodes(ctx)
	if err != nil {
		r.Log.Error("list nodes failed — skipping tick", "err", err)
		return
	}

	pools := node.Classify(nodes, p.Spec.StablePoolSelector, p.Spec.SurgeSelectors())
	r.Metrics.SetPools(len(pools.Stable), len(pools.Surge))

	dec := planner.Plan(pools.Surge, p)
	r.Metrics.SetPlan(len(dec.Monitored), len(dec.SampledOut))

	res, err := r.Actuator.Apply(ctx, dec, p, nodes)
	if err != nil {
		r.Log.Error("actuator failed", "err", err)
	}
	r.Metrics.AddActuation(res.Applied, res.Cleared, res.Errors)

	if r.Reporter != nil {
		mode := "dry_run"
		if p.Configured && p.Spec.Mode != "" {
			mode = p.Spec.Mode
		}
		r.Reporter.Report(ctx, ReportInput{
			Mode:            mode,
			Actuated:        r.ExecuteEnabled && p.Configured && p.Spec.Mode == "active",
			MonitoredCount:  len(dec.Monitored),
			SampledOutCount: len(dec.SampledOut),
			LabelsApplied:   res.Applied,
			LabelsCleared:   res.Cleared,
			LabelErrors:     res.Errors,
			SampledNodes:    dec.SampledOut,
		})
	}
}

// Run reconciles immediately, then every interval until ctx is cancelled.
func (r *Reconciler) Run(ctx context.Context, interval time.Duration) {
	r.Tick(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.Tick(ctx)
		}
	}
}
