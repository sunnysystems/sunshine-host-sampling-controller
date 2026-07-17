package policy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetch_configured(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("ETag", `"v1"`)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"configured":true,"policy":{"mode":"dry_run","surgeSamplePct":40,"stablePoolSelector":"capacity-type=on-demand","surgePoolSelector":"capacity-type=spot","floorNodes":3},"version":"v1"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "TOK", 2*time.Second)
	p, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer TOK" {
		t.Fatalf("Authorization = %q, want Bearer TOK", gotAuth)
	}
	if !p.Configured || p.Spec.SurgeSamplePct != 40 || p.Spec.FloorNodes != 3 {
		t.Fatalf("unexpected policy: %+v", p)
	}
}

func TestFetch_unconfigured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"configured":false,"policy":null,"version":"0"}`))
	}))
	defer srv.Close()

	p, err := NewClient(srv.URL, "TOK", 2*time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Configured {
		t.Fatalf("expected unconfigured, got %+v", p)
	}
}

func TestFetch_serverErrorFailsOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	p, err := NewClient(srv.URL, "TOK", 2*time.Second).Fetch(context.Background())
	if err == nil {
		t.Fatal("expected an error for 5xx")
	}
	if p.Configured {
		t.Fatalf("5xx must fail open (unconfigured), got %+v", p)
	}
}

func TestFetch_notModifiedReturnsCached(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"configured":true,"policy":{"surgeSamplePct":50,"floorNodes":1},"version":"v1"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "TOK", 2*time.Second)
	if _, err := c.Fetch(context.Background()); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	p, err := c.Fetch(context.Background()) // should send If-None-Match and get 304
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if !p.Configured || p.Spec.SurgeSamplePct != 50 {
		t.Fatalf("304 must return the cached policy, got %+v", p)
	}
	if calls != 2 {
		t.Fatalf("expected 2 server calls, got %d", calls)
	}
}

func TestFetch_transportErrorFailsOpen(t *testing.T) {
	// Nothing listening → transport error → fail open.
	p, err := NewClient("http://127.0.0.1:0", "TOK", 500*time.Millisecond).Fetch(context.Background())
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if p.Configured {
		t.Fatalf("transport error must fail open, got %+v", p)
	}
}

// The list is the canonical surge encoding.
func TestSurgeSelectors_listWins(t *testing.T) {
	s := Spec{
		SurgePoolSelectors: []string{"k=a", "k=b"},
		SurgePoolSelector:  "k=a",
	}
	got := s.SurgeSelectors()
	if len(got) != 2 || got[0] != "k=a" || got[1] != "k=b" {
		t.Fatalf("want the full list, got %+v", got)
	}
}

// A server predating the list sends only the scalar. Honouring it is what keeps an
// upgraded controller sampling instead of silently sampling nothing.
func TestSurgeSelectors_legacyScalarFallback(t *testing.T) {
	got := Spec{SurgePoolSelector: "capacity-type=spot"}.SurgeSelectors()
	if len(got) != 1 || got[0] != "capacity-type=spot" {
		t.Fatalf("want the legacy scalar, got %+v", got)
	}
}

func TestSurgeSelectors_blanksDropped(t *testing.T) {
	if got := (Spec{SurgePoolSelectors: []string{"  ", ""}, SurgePoolSelector: " k=a "}).SurgeSelectors(); len(got) != 1 || got[0] != "k=a" {
		t.Fatalf("blank list members must not shadow the scalar, got %+v", got)
	}
	if got := (Spec{}).SurgeSelectors(); got != nil {
		t.Fatalf("an empty spec must yield no selectors (fail-open), got %+v", got)
	}
}

// End-to-end over the wire: the payload the current server actually sends.
func TestFetch_parsesSurgeList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"configured":true,"version":"1:dry_run","policy":{
			"mode":"dry_run","surgeSamplePct":50,"stablePoolSelector":null,
			"surgePoolSelectors":["karpenter_nodepool=high-cpu","karpenter_nodepool=default"],
			"surgePoolSelector":"karpenter_nodepool=high-cpu","floorNodes":1}}`))
	}))
	defer srv.Close()

	p, err := NewClient(srv.URL, "TOK", 2*time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !p.Configured {
		t.Fatal("want configured")
	}
	if got := p.Spec.SurgeSelectors(); len(got) != 2 {
		t.Fatalf("want both surge pools, got %+v", got)
	}
	// stablePoolSelector is null on the wire now — it must decode as empty, not
	// as a selector that matches nothing in particular.
	if p.Spec.StablePoolSelector != "" {
		t.Fatalf("want an empty stable selector, got %q", p.Spec.StablePoolSelector)
	}
}
