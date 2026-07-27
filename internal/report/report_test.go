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
