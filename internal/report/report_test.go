package report

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/buildinfo"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/node"
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

// A nil SampledNodes must go on the wire as `[]`, never as `null`.
//
// planner.Plan builds Decision.SampledOut with append over a nil slice, so every
// reconcile that samples nothing — the steady state of a healthy dry-run cluster
// — hands this package a nil slice. Marshalling that as `null` got 400'd by
// Sunshine, silently costing the customer their entire audit trail (issue #8).
// Assert on the RAW BODY: decoding into payload would turn both `null` and `[]`
// back into a nil slice and hide the very difference under test.
func TestReport_nilSampledNodesMarshalsAsEmptyArray(t *testing.T) {
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	NewClient(srv.URL, "t", 2*time.Second, discardLog()).Report(
		context.Background(),
		reconcile.ReportInput{Mode: "dry_run", SampledNodes: nil},
	)

	if !bytes.Contains(raw, []byte(`"sampledNodes":[]`)) {
		t.Fatalf(`body must carry "sampledNodes":[], got %s`, raw)
	}
	if bytes.Contains(raw, []byte(`"sampledNodes":null`)) {
		t.Fatalf("body must never carry a null sampledNodes, got %s", raw)
	}
}

// The observed-label summary must reach the wire in raw Kubernetes form, and
// must never be null (sunnysystems-sunshine#645, and the #8 null lesson).
func TestReport_carriesObservedNodeLabels(t *testing.T) {
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	NewClient(srv.URL, "t", 2*time.Second, discardLog()).Report(
		context.Background(),
		reconcile.ReportInput{
			Mode: "dry_run",
			ObservedLabels: []node.LabelSummary{
				{Key: "karpenter.sh/nodepool", Values: []string{"default", "high-cpu"}},
			},
		},
	)

	want := `"observedNodeLabels":[{"key":"karpenter.sh/nodepool","values":["default","high-cpu"]}]`
	if !bytes.Contains(raw, []byte(want)) {
		t.Fatalf("body missing raw k8s label key.\ngot:  %s\nwant: %s", raw, want)
	}
}

func TestReport_nilObservedLabelsMarshalsAsEmptyArray(t *testing.T) {
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	NewClient(srv.URL, "t", 2*time.Second, discardLog()).Report(
		context.Background(),
		reconcile.ReportInput{Mode: "dry_run", ObservedLabels: nil},
	)

	if !bytes.Contains(raw, []byte(`"observedNodeLabels":[]`)) {
		t.Fatalf(`want "observedNodeLabels":[], got %s`, raw)
	}
}

// The enforcement preflight verdict must reach Sunshine (#657) — until now it
// lived only in this cluster's logs and gauge, which is why the console could
// not tell an operator that labelling a node would change nothing billed.
func TestReport_carriesEnforcementAffinity(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   reconcile.EnforcementAffinity
		want string
	}{
		{"present", reconcile.EnforcementPresent, "present"},
		{"absent", reconcile.EnforcementAbsent, "absent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body payload
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&body)
				w.WriteHeader(http.StatusAccepted)
			}))
			defer srv.Close()

			NewClient(srv.URL, "tok", 2*time.Second, discardLog()).Report(
				context.Background(),
				reconcile.ReportInput{Mode: "dry_run", EnforcementAffinity: tc.in},
			)
			if body.EnforcementAffinity != tc.want {
				t.Fatalf("enforcementAffinity = %q, want %q", body.EnforcementAffinity, tc.want)
			}
		})
	}
}

// Unknown must leave the wire entirely rather than travel as a value. A bool
// here would have made "nobody looked" indistinguishable from "looked, and it
// is missing" — the #8 mistake, which accuses a correctly-configured cluster.
func TestReport_unknownEnforcementIsAbsentFromPayload(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &raw)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	NewClient(srv.URL, "tok", 2*time.Second, discardLog()).Report(
		context.Background(),
		// Zero value: the preflight was skipped or could not read the DaemonSet.
		reconcile.ReportInput{Mode: "dry_run"},
	)
	if _, present := raw["enforcementAffinity"]; present {
		t.Fatalf("unknown must be omitted, got %v", raw["enforcementAffinity"])
	}
}

func TestReport_carriesNodeAges(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&raw)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	NewClient(srv.URL, "tok", 2*time.Second, discardLog()).Report(
		context.Background(),
		reconcile.ReportInput{
			Mode: "dry_run",
			NodeAges: []node.NodeAge{
				{Name: "ip-10-0-0-1.ec2.internal", CreatedAt: "2026-05-01T10:00:00Z"},
			},
		},
	)

	ages, ok := raw["nodeAges"].([]any)
	if !ok || len(ages) != 1 {
		t.Fatalf("nodeAges missing from payload: %#v", raw["nodeAges"])
	}
	first, _ := ages[0].(map[string]any)
	// The plain Kubernetes name: Sunshine owns the translation to its own
	// inventory's naming, and sending anything else moves that knowledge here.
	if first["name"] != "ip-10-0-0-1.ec2.internal" {
		t.Fatalf("wrong node name: %#v", first)
	}
	if first["createdAt"] != "2026-05-01T10:00:00Z" {
		t.Fatalf("wrong timestamp: %#v", first)
	}
}

func TestReport_omitsNodeAgesWhenThereIsNoRanking(t *testing.T) {
	// Absent means "no ranking from here" and sends Sunshine to its own census.
	// An empty array would instead read as "I rank, and the fleet is empty" —
	// a claim this controller has no way to make truthfully when it is over the
	// cap or saw no timestamps.
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&raw)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	NewClient(srv.URL, "tok", 2*time.Second, discardLog()).Report(
		context.Background(),
		reconcile.ReportInput{Mode: "dry_run", NodeAges: nil},
	)

	if _, present := raw["nodeAges"]; present {
		t.Fatalf("nodeAges should be absent, got %#v", raw["nodeAges"])
	}
}
