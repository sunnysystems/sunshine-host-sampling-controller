package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler(t *testing.T) {
	r := New()
	r.SetPools(2, 5)
	r.SetPlan(3, 2)
	r.SetConfigured(true)
	r.IncTick()
	r.IncFetchError()

	rec := httptest.NewRecorder()
	r.Handler()(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	for _, want := range []string{
		"sunshine_host_sampling_stable_nodes 2",
		"sunshine_host_sampling_surge_nodes 5",
		"sunshine_host_sampling_monitored_nodes 3",
		"sunshine_host_sampling_would_sample_out_nodes 2",
		"sunshine_host_sampling_policy_configured 1",
		"sunshine_host_sampling_reconcile_ticks_total 1",
		"sunshine_host_sampling_policy_fetch_errors_total 1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q\n---\n%s", want, body)
		}
	}
}

func TestAddActuation(t *testing.T) {
	r := New()
	r.AddActuation(3, 1, 0)
	r.AddActuation(2, 0, 1) // accumulates
	rec := httptest.NewRecorder()
	r.Handler()(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	for _, want := range []string{
		"sunshine_host_sampling_labels_applied_total 5",
		"sunshine_host_sampling_labels_cleared_total 1",
		"sunshine_host_sampling_label_errors_total 1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q\n---\n%s", want, body)
		}
	}
}

func TestSetConfiguredFalse(t *testing.T) {
	r := New()
	r.SetConfigured(false)
	rec := httptest.NewRecorder()
	r.Handler()(rec, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "sunshine_host_sampling_policy_configured 0") {
		t.Error("expected policy_configured 0")
	}
}
