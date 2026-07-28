package report

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/buildinfo"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/reconcile"
)

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestReport_postsScopedSummary(t *testing.T) {
	var gotAuth, gotPath string
	var body payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	NewClient(srv.URL, "tok-123", 2*time.Second, discardLog()).Report(
		context.Background(),
		reconcile.ReportInput{
			Mode:            "active",
			Actuated:        true,
			MonitoredCount:  2,
			SampledOutCount: 3,
			LabelsApplied:   3,
			SampledNodes:    []string{"s2", "s3", "s4"},
		},
	)

	if gotPath != "/api/autopilot/report/host-sampling" {
		t.Fatalf("posted to %q", gotPath)
	}
	if gotAuth != "Bearer tok-123" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if body.MonitoredCount != 2 || body.SampledOutCount != 3 || !body.Actuated {
		t.Fatalf("unexpected payload: %+v", body)
	}
	if len(body.SampledNodes) != 3 {
		t.Fatalf("sampledNodes = %v", body.SampledNodes)
	}
}

func TestReport_swallowsServerErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	// Must not panic or block — a failed report is dropped.
	NewClient(srv.URL, "t", time.Second, discardLog()).Report(
		context.Background(), reconcile.ReportInput{Mode: "dry_run"},
	)
}

func TestReport_capsSampledNodes(t *testing.T) {
	var body payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	big := make([]string, maxSampledNodes+50)
	for i := range big {
		big[i] = "n"
	}
	NewClient(srv.URL, "t", 2*time.Second, discardLog()).Report(
		context.Background(),
		reconcile.ReportInput{Mode: "active", SampledNodes: big},
	)
	if len(body.SampledNodes) != maxSampledNodes {
		t.Fatalf("sampledNodes not capped: got %d, want %d", len(body.SampledNodes), maxSampledNodes)
	}
}

// ─── Capability echo (#572) ─────────────────────────────────────────────────

// captureRaw returns a server that records the request's raw JSON body and
// User-Agent. Raw, not decoded into payload: the properties below are about what
// goes ON THE WIRE, and decoding erases the very distinction under test
// (an absent field and an explicit null both land as a nil slice).
func captureRaw(t *testing.T, raw *map[string]json.RawMessage, ua *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*ua = r.Header.Get("User-Agent")
		if err := json.NewDecoder(r.Body).Decode(raw); err != nil {
			t.Errorf("decode raw body: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
}

func TestReport_echoesPolicyVersionAndHonoredSelectors(t *testing.T) {
	var raw map[string]json.RawMessage
	var ua string
	srv := captureRaw(t, &raw, &ua)
	defer srv.Close()

	NewClient(srv.URL, "t", 2*time.Second, discardLog()).Report(
		context.Background(),
		reconcile.ReportInput{
			Mode:                  "active",
			PolicyVersion:         "1750000000000:active",
			HonoredSurgeSelectors: []string{"karpenter.sh/nodepool=surge-a", "karpenter.sh/nodepool=surge-b"},
		},
	)

	if got := string(raw["policyVersion"]); got != `"1750000000000:active"` {
		t.Fatalf("policyVersion = %s", got)
	}
	if got := string(raw["honoredSurgeSelectors"]); got != `["karpenter.sh/nodepool=surge-a","karpenter.sh/nodepool=surge-b"]` {
		t.Fatalf("honoredSurgeSelectors = %s", got)
	}
	if _, ok := raw["controllerVersion"]; !ok {
		t.Fatal("controllerVersion absent — Sunshine cannot show which build a cluster runs")
	}
	if ua != buildinfo.UserAgent() {
		t.Fatalf("User-Agent = %q, want %q", ua, buildinfo.UserAgent())
	}
}

// The load-bearing wire property of #572. Sunshine reads this field's PRESENCE
// to tell "a controller too old to echo" (absent → unknown, warn if the config
// needs >1 pool) from "this controller honoured no pool" (`[]` → an unconfigured
// policy, silent). A nil slice marshals to `null`, which collapses the two and
// would make every unconfigured cluster look like a stale binary.
func TestReport_honoredSelectorsMarshalsEmptyArrayNotNull(t *testing.T) {
	var raw map[string]json.RawMessage
	var ua string
	srv := captureRaw(t, &raw, &ua)
	defer srv.Close()

	// An unconfigured (fail-open) tick: SurgeSelectors() returned nil.
	NewClient(srv.URL, "t", 2*time.Second, discardLog()).Report(
		context.Background(),
		reconcile.ReportInput{Mode: "dry_run", HonoredSurgeSelectors: nil},
	)

	got, ok := raw["honoredSurgeSelectors"]
	if !ok {
		t.Fatal("honoredSurgeSelectors absent — the server would read this as an old controller")
	}
	if string(got) != "[]" {
		t.Fatalf("honoredSurgeSelectors = %s, want [] (null is indistinguishable from an old controller)", got)
	}
}

// An unstamped build must still report — "dev" is informational to Sunshine,
// never a gate, so nothing here may become conditional on a real version.
func TestReport_unstampedBuildStillReports(t *testing.T) {
	var raw map[string]json.RawMessage
	var ua string
	srv := captureRaw(t, &raw, &ua)
	defer srv.Close()

	NewClient(srv.URL, "t", 2*time.Second, discardLog()).Report(
		context.Background(), reconcile.ReportInput{Mode: "dry_run"},
	)
	if got := string(raw["controllerVersion"]); got != `"`+buildinfo.Version+`"` {
		t.Fatalf("controllerVersion = %s, want %q", got, buildinfo.Version)
	}
}
