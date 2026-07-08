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
