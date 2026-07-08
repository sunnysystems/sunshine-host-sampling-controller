package actuator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/node"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/planner"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/policy"
)

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestDryRunApply_neverErrors(t *testing.T) {
	a := DryRun{Log: discardLog()}
	res, err := a.Apply(
		context.Background(),
		planner.Decision{Budget: 3, Monitored: []string{"a"}, SampledOut: []string{"b", "c"}},
		policy.Policy{Configured: true, Spec: policy.Spec{Mode: "dry_run"}},
		nil,
	)
	if err != nil {
		t.Fatalf("DryRun.Apply must never error, got %v", err)
	}
	if res != (Result{}) {
		t.Fatalf("DryRun.Apply must not actuate, got %+v", res)
	}
}

// fakeLabeler records SetLabel/RemoveLabel calls; it does not mutate the input
// nodes (a real tick re-lists nodes with updated labels next iteration).
type fakeLabeler struct {
	set     map[string]bool
	removed map[string]bool
	failSet bool
}

func newFakeLabeler() *fakeLabeler {
	return &fakeLabeler{set: map[string]bool{}, removed: map[string]bool{}}
}

func (f *fakeLabeler) SetLabel(_ context.Context, nodeName, key, _ string) error {
	if key != LabelSampledOut {
		return errors.New("unexpected label key")
	}
	if f.failSet {
		return errors.New("boom")
	}
	f.set[nodeName] = true
	return nil
}

func (f *fakeLabeler) RemoveLabel(_ context.Context, nodeName, key string) error {
	if key != LabelSampledOut {
		return errors.New("unexpected label key")
	}
	f.removed[nodeName] = true
	return nil
}

func surge(name string, labeled bool) node.Node {
	labels := map[string]string{"capacity-type": "spot"}
	if labeled {
		labels[LabelSampledOut] = labelValueTrue
	}
	return node.Node{Name: name, Labels: labels}
}

func stable(name string) node.Node {
	return node.Node{Name: name, Labels: map[string]string{"capacity-type": "on-demand"}}
}

func activePolicy() policy.Policy {
	return policy.Policy{Configured: true, Spec: policy.Spec{Mode: "active"}}
}

func TestLabelActuator_appliesToSampledOut(t *testing.T) {
	fl := newFakeLabeler()
	a := LabelActuator{Labeler: fl, Log: discardLog()}
	nodes := []node.Node{stable("st-0"), surge("s0", false), surge("s1", false), surge("s2", false)}
	dec := planner.Decision{Monitored: []string{"s0"}, SampledOut: []string{"s1", "s2"}}

	res, err := a.Apply(context.Background(), dec, activePolicy(), nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Applied != 2 || res.Cleared != 0 || res.Errors != 0 {
		t.Fatalf("got %+v, want applied=2 cleared=0 errors=0", res)
	}
	if !fl.set["s1"] || !fl.set["s2"] {
		t.Fatalf("expected s1,s2 labeled, got %v", fl.set)
	}
	if fl.set["s0"] || fl.set["st-0"] {
		t.Fatal("monitored surge node and stable node must never be labeled")
	}
}

func TestLabelActuator_cleansOrphanLabels(t *testing.T) {
	fl := newFakeLabeler()
	a := LabelActuator{Labeler: fl, Log: discardLog()}
	// s1 was sampled out last tick but is now back in the monitored budget.
	nodes := []node.Node{surge("s0", false), surge("s1", true), surge("s2", false)}
	dec := planner.Decision{Monitored: []string{"s0", "s1"}, SampledOut: []string{"s2"}}

	res, err := a.Apply(context.Background(), dec, activePolicy(), nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Applied != 1 || res.Cleared != 1 {
		t.Fatalf("got %+v, want applied=1 (s2) cleared=1 (s1)", res)
	}
	if !fl.removed["s1"] {
		t.Fatal("orphan label on s1 must be removed")
	}
	if !fl.set["s2"] {
		t.Fatal("s2 must be labeled")
	}
}

func TestLabelActuator_dryRunModeRemovesAllLabels(t *testing.T) {
	// Simulates pause: server downgraded mode to dry_run, so nothing should stay
	// sampled out — existing labels are cleared to restore monitoring.
	fl := newFakeLabeler()
	a := LabelActuator{Labeler: fl, Log: discardLog()}
	nodes := []node.Node{surge("s0", true), surge("s1", true), surge("s2", false)}
	dec := planner.Decision{Monitored: []string{"s0"}, SampledOut: []string{"s1", "s2"}}
	p := policy.Policy{Configured: true, Spec: policy.Spec{Mode: "dry_run"}}

	res, err := a.Apply(context.Background(), dec, p, nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Applied != 0 || res.Cleared != 2 {
		t.Fatalf("got %+v, want applied=0 cleared=2 (s0,s1)", res)
	}
	if !fl.removed["s0"] || !fl.removed["s1"] {
		t.Fatalf("both existing labels must be cleared, got %v", fl.removed)
	}
}

func TestLabelActuator_failOpenClearsLabels(t *testing.T) {
	fl := newFakeLabeler()
	a := LabelActuator{Labeler: fl, Log: discardLog()}
	nodes := []node.Node{surge("s0", true)}
	// Unconfigured policy (fail-open) with an empty plan.
	res, err := a.Apply(context.Background(), planner.Decision{}, policy.Policy{Configured: false}, nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Applied != 0 || res.Cleared != 1 {
		t.Fatalf("got %+v, want applied=0 cleared=1", res)
	}
}

func TestLabelActuator_idempotent(t *testing.T) {
	fl := newFakeLabeler()
	a := LabelActuator{Labeler: fl, Log: discardLog()}
	// Desired state already reflected in the node labels → no changes.
	nodes := []node.Node{surge("s0", false), surge("s1", true), surge("s2", true)}
	dec := planner.Decision{Monitored: []string{"s0"}, SampledOut: []string{"s1", "s2"}}

	res, err := a.Apply(context.Background(), dec, activePolicy(), nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != (Result{}) {
		t.Fatalf("steady state must be a no-op, got %+v", res)
	}
}

func TestLabelActuator_countsPatchErrors(t *testing.T) {
	fl := newFakeLabeler()
	fl.failSet = true
	a := LabelActuator{Labeler: fl, Log: discardLog()}
	nodes := []node.Node{surge("s0", false)}
	dec := planner.Decision{SampledOut: []string{"s0"}}

	res, err := a.Apply(context.Background(), dec, activePolicy(), nodes)
	if err == nil {
		t.Fatal("expected a joined error when a patch fails")
	}
	if res.Applied != 0 || res.Errors != 1 {
		t.Fatalf("got %+v, want applied=0 errors=1", res)
	}
}
