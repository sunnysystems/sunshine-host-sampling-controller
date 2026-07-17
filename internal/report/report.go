// Package report ships the per-reconcile summary to Sunshine.
// Best-effort telemetry: a failed report is logged and dropped — it must
// never block or fail a reconcile, and it never changes what the controller
// does. Uses the SAME scoped inbound token the policy client uses; the server
// scopes the persisted row to that token's (org, cluster).
package report

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/buildinfo"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/reconcile"
)

// maxSampledNodes bounds the payload; the server rejects larger lists.
const maxSampledNodes = 200

// Client posts reconcile summaries to the Sunshine report endpoint.
type Client struct {
	url   string
	token string
	http  *http.Client
	log   *slog.Logger
}

// NewClient builds a report client. endpoint is the Sunshine base URL.
func NewClient(endpoint, token string, timeout time.Duration, log *slog.Logger) *Client {
	return &Client{
		url:   strings.TrimRight(endpoint, "/") + "/api/autopilot/report/host-sampling",
		token: token,
		http:  &http.Client{Timeout: timeout},
		log:   log,
	}
}

type payload struct {
	Mode            string   `json:"mode"`
	Actuated        bool     `json:"actuated"`
	MonitoredCount  int      `json:"monitoredCount"`
	SampledOutCount int      `json:"sampledOutCount"`
	LabelsApplied   int      `json:"labelsApplied"`
	LabelsCleared   int      `json:"labelsCleared"`
	LabelErrors     int      `json:"labelErrors"`
	SampledNodes    []string `json:"sampledNodes"`

	// The capability echo (#572). Sunshine reads these to warn when a cluster's
	// controller cannot honour the configured policy — never to reject a report.
	//
	// HonoredSurgeSelectors carries NO omitempty, and Report() replaces a nil
	// slice with an empty one, ON PURPOSE. Sunshine distinguishes three states
	// by presence alone: absent = a controller too old to echo (unknown), `[]` =
	// this controller honoured no surge pool (unconfigured policy), `[...]` =
	// these pools were applied. A nil slice marshals to `null`, which would
	// collapse "I honoured nothing" into "I cannot tell you" and make an
	// unconfigured cluster indistinguishable from a stale binary.
	PolicyVersion         string   `json:"policyVersion,omitempty"`
	HonoredSurgeSelectors []string `json:"honoredSurgeSelectors"`
	ControllerVersion     string   `json:"controllerVersion,omitempty"`
}

// Report ships one reconcile summary. Errors are logged and swallowed.
func (c *Client) Report(ctx context.Context, in reconcile.ReportInput) {
	nodes := in.SampledNodes
	if len(nodes) > maxSampledNodes {
		nodes = nodes[:maxSampledNodes]
	}
	// Never nil — see the payload doc: `[]` and absent mean different things to
	// the server, and only a non-nil slice marshals to `[]`.
	honored := in.HonoredSurgeSelectors
	if honored == nil {
		honored = []string{}
	}
	body, err := json.Marshal(payload{
		Mode:                  in.Mode,
		Actuated:              in.Actuated,
		MonitoredCount:        in.MonitoredCount,
		SampledOutCount:       in.SampledOutCount,
		LabelsApplied:         in.LabelsApplied,
		LabelsCleared:         in.LabelsCleared,
		LabelErrors:           in.LabelErrors,
		SampledNodes:          nodes,
		PolicyVersion:         in.PolicyVersion,
		HonoredSurgeSelectors: honored,
		ControllerVersion:     buildinfo.Version,
	})
	if err != nil {
		c.log.Warn("report: marshal failed", "err", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		c.log.Warn("report: request build failed", "err", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", buildinfo.UserAgent())

	resp, err := c.http.Do(req)
	if err != nil {
		c.log.Warn("report: post failed (dropped)", "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		c.log.Warn("report: server rejected", "status", resp.StatusCode)
	}
}
