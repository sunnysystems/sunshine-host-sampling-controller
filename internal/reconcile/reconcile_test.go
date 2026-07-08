package reconcile

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/actuator"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/metrics"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/node"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/planner"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/policy"
)

type fakePolicy struct {
	p   policy.Policy
	err error
}

func (f fakePolicy) Fetch(context.Context) (policy.Policy, error) { return f.p, f.err }

type fakeNodes struct {
	nodes []node.Node
	err   error
}

func (f fakeNodes) ListNodes(context.Context) ([]node.Node, error) { return f.nodes, f.err }

type captureActuator struct {
	dec   planner.Decision
	p     policy.Policy
	nodes []node.Node
	calls int
}

func (c *captureActuator) Apply(_ context.Context, dec planner.Decision, p policy.Policy, nodes []node.Node) (actuator.Result, error) {
	c.dec, c.p, c.nodes, c.calls = dec, p, nodes, c.calls+1
	return actuator.Result{}, nil
}

type captureReporter struct {
	last  ReportInput
	calls int
}

func (c *captureReporter) Report(_ context.Context, in ReportInput) {
	c.last, c.calls = in, c.calls+1
}

func newReconciler(pf PolicyFetcher, nl NodeLister, act actuator.Actuator) *Reconciler {
	return &Reconciler{
		Policy:   pf,
		Nodes:    nl,
		Actuator: act,
		Metrics:  metrics.New(),
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func surge(name string, age int) node.Node {
	return node.Node{
		Name:      name,
		Labels:    map[string]string{"capacity-type": "spot"},
		CreatedAt: time.Date(2026, 1, 1, age, 0, 0, 0, time.UTC),
	}
}

func TestTick_configuredPlansSurge(t *testing.T) {
	nodes := []node.Node{
		{Name: "stable-a", Labels: map[string]string{"capacity-type": "on-demand"}},
		surge("s0", 0), surge("s1", 1), surge("s2", 2), surge("s3", 3), surge("s4", 4),
	}
	p := policy.Policy{Configured: true, Spec: policy.Spec{
		SurgeSamplePct:     40,
		FloorNodes:         1,
		StablePoolSelector: "capacity-type=on-demand",
		SurgePoolSelector:  "capacity-type=spot",
	}}
	cap := &captureActuator{}
	newReconciler(fakePolicy{p: p}, fakeNodes{nodes: nodes}, cap).Tick(context.Background())

	if cap.calls != 1 {
		t.Fatalf("actuator called %d times, want 1", cap.calls)
	}
	// 5 surge, 40% → budget 2 monitored, 3 sampled out.
	if len(cap.dec.Monitored) != 2 || len(cap.dec.SampledOut) != 3 {
		t.Fatalf("plan monitored=%d sampledOut=%d, want 2/3", len(cap.dec.Monitored), len(cap.dec.SampledOut))
	}
	// The full node list (stable + surge) is forwarded so the actuator can clean
	// up orphan labels.
	if len(cap.nodes) != len(nodes) {
		t.Fatalf("actuator received %d nodes, want %d", len(cap.nodes), len(nodes))
	}
}

func TestTick_fetchErrorFailsOpen(t *testing.T) {
	cap := &captureActuator{}
	newReconciler(
		fakePolicy{p: policy.Policy{Configured: false}, err: errors.New("boom")},
		fakeNodes{nodes: []node.Node{surge("s0", 0)}},
		cap,
	).Tick(context.Background())

	if cap.p.Configured {
		t.Fatal("fetch error must leave policy unconfigured")
	}
	if len(cap.dec.SampledOut) != 0 {
		t.Fatalf("fail-open must sample nothing, got %d", len(cap.dec.SampledOut))
	}
}

func TestTick_listErrorSkipsActuation(t *testing.T) {
	cap := &captureActuator{}
	newReconciler(
		fakePolicy{p: policy.Policy{Configured: true}},
		fakeNodes{err: errors.New("nope")},
		cap,
	).Tick(context.Background())

	if cap.calls != 0 {
		t.Fatal("actuator must not run when node listing fails")
	}
}

func TestTick_reportsReconcileSummary(t *testing.T) {
	nodes := []node.Node{
		{Name: "stable-a", Labels: map[string]string{"capacity-type": "on-demand"}},
		surge("s0", 0), surge("s1", 1), surge("s2", 2), surge("s3", 3), surge("s4", 4),
	}
	p := policy.Policy{Configured: true, Spec: policy.Spec{
		Mode:               "active",
		SurgeSamplePct:     40,
		FloorNodes:         1,
		StablePoolSelector: "capacity-type=on-demand",
		SurgePoolSelector:  "capacity-type=spot",
	}}
	rep := &captureReporter{}
	r := newReconciler(fakePolicy{p: p}, fakeNodes{nodes: nodes}, &captureActuator{})
	r.Reporter = rep
	r.ExecuteEnabled = true
	r.Tick(context.Background())

	if rep.calls != 1 {
		t.Fatalf("reporter called %d times, want 1", rep.calls)
	}
	// 5 surge @ 40% → 2 monitored / 3 sampled out; execute + active → actuated.
	if rep.last.MonitoredCount != 2 || rep.last.SampledOutCount != 3 {
		t.Fatalf("report monitored=%d sampledOut=%d, want 2/3", rep.last.MonitoredCount, rep.last.SampledOutCount)
	}
	if rep.last.Mode != "active" || !rep.last.Actuated {
		t.Fatalf("report mode=%q actuated=%v, want active/true", rep.last.Mode, rep.last.Actuated)
	}
}

func TestTick_reportsNotActuatedInDryRun(t *testing.T) {
	p := policy.Policy{Configured: true, Spec: policy.Spec{
		Mode:              "active",
		SurgePoolSelector: "capacity-type=spot",
	}}
	rep := &captureReporter{}
	r := newReconciler(fakePolicy{p: p}, fakeNodes{nodes: []node.Node{surge("s0", 0)}}, &captureActuator{})
	r.Reporter = rep
	r.ExecuteEnabled = false // local DRY_RUN
	r.Tick(context.Background())

	if rep.last.Actuated {
		t.Fatal("must report actuated=false when execute is disabled locally")
	}
}
