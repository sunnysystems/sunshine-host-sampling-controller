// Package metrics exposes controller state in Prometheus text format using only
// the standard library (no client dependency), so it stays offline-testable.
package metrics

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

// Registry holds the gauges/counters the controller reports.
type Registry struct {
	stableNodes    atomic.Int64
	surgeNodes     atomic.Int64
	monitoredNodes atomic.Int64
	sampledOut     atomic.Int64
	configured     atomic.Int64 // 1 = policy configured, 0 = fail-open
	ticks          atomic.Int64
	fetchErrors    atomic.Int64
	labelsApplied  atomic.Int64 // cumulative sampled-out labels written
	labelsCleared  atomic.Int64 // cumulative sampled-out labels removed
	labelErrors    atomic.Int64 // cumulative per-node patch failures

	enforcementChecked atomic.Bool  // true once the affinity preflight has run
	enforcementPresent atomic.Int64 // 1 = agent DaemonSet has the anti-affinity
}

func New() *Registry { return &Registry{} }

// SetPools records the current pool sizes.
func (r *Registry) SetPools(stable, surge int) {
	r.stableNodes.Store(int64(stable))
	r.surgeNodes.Store(int64(surge))
}

// SetPlan records the latest dry-run plan.
func (r *Registry) SetPlan(monitored, sampledOut int) {
	r.monitoredNodes.Store(int64(monitored))
	r.sampledOut.Store(int64(sampledOut))
}

// SetConfigured records whether the last poll returned a configured policy.
func (r *Registry) SetConfigured(configured bool) {
	if configured {
		r.configured.Store(1)
	} else {
		r.configured.Store(0)
	}
}

func (r *Registry) IncTick()       { r.ticks.Add(1) }
func (r *Registry) IncFetchError() { r.fetchErrors.Add(1) }

// SetEnforcementAffinity records the enforcement preflight result. Once set, the
// gauge is emitted; if never called (preflight skipped) the gauge is absent.
func (r *Registry) SetEnforcementAffinity(present bool) {
	r.enforcementChecked.Store(true)
	if present {
		r.enforcementPresent.Store(1)
	} else {
		r.enforcementPresent.Store(0)
	}
}

// AddActuation records the labels applied/cleared and patch errors from one
// reconcile. Zero for the dry-run actuator.
func (r *Registry) AddActuation(applied, cleared, errs int) {
	if applied != 0 {
		r.labelsApplied.Add(int64(applied))
	}
	if cleared != 0 {
		r.labelsCleared.Add(int64(cleared))
	}
	if errs != 0 {
		r.labelErrors.Add(int64(errs))
	}
}

// Handler writes the metrics in Prometheus text exposition format.
func (r *Registry) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		gauge(w, "sunshine_host_sampling_stable_nodes",
			"Nodes in the fixed (stable) pool.", r.stableNodes.Load())
		gauge(w, "sunshine_host_sampling_surge_nodes",
			"Nodes in the surge pool.", r.surgeNodes.Load())
		gauge(w, "sunshine_host_sampling_monitored_nodes",
			"Surge nodes kept monitored by the plan.", r.monitoredNodes.Load())
		gauge(w, "sunshine_host_sampling_would_sample_out_nodes",
			"Surge nodes the plan would sample out (never applied in dry-run).", r.sampledOut.Load())
		gauge(w, "sunshine_host_sampling_policy_configured",
			"1 when the last poll returned a configured policy, else 0 (fail-open).", r.configured.Load())
		counter(w, "sunshine_host_sampling_reconcile_ticks_total",
			"Total reconcile ticks.", r.ticks.Load())
		counter(w, "sunshine_host_sampling_policy_fetch_errors_total",
			"Total policy fetch errors (each fails open).", r.fetchErrors.Load())
		counter(w, "sunshine_host_sampling_labels_applied_total",
			"Total sampled-out labels written (execute mode).", r.labelsApplied.Load())
		counter(w, "sunshine_host_sampling_labels_cleared_total",
			"Total sampled-out labels removed (orphan cleanup / pause).", r.labelsCleared.Load())
		counter(w, "sunshine_host_sampling_label_errors_total",
			"Total per-node label patch failures.", r.labelErrors.Load())
		if r.enforcementChecked.Load() {
			gauge(w, "sunshine_host_sampling_enforcement_affinity_present",
				"1 when the agent DaemonSet carries the sampled-out anti-affinity (labels take effect), else 0.",
				r.enforcementPresent.Load())
		}
	}
}

func gauge(w http.ResponseWriter, name, help string, v int64) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %d\n", name, help, name, name, v)
}

func counter(w http.ResponseWriter, name, help string, v int64) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, v)
}
