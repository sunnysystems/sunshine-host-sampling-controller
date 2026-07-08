// Package policy fetches the host-sampling policy from Sunshine over HTTP.
//
// Contract (kept in sync with the Sunshine server):
//
//	GET {endpoint}/api/autopilot/policy/host-sampling
//	Authorization: Bearer <token>
//	200 → {"configured":bool,"policy":{"mode","surgeSamplePct","stablePoolSelector",
//	       "surgePoolSelector","floorNodes"},"version":string}  (+ ETag header)
//	304 → not modified (keep the cached policy)
//	401/404/5xx → treated as unconfigured (FAIL OPEN)
//
// Fail-open is the core safety property: any error, or a non-configured
// response, yields Policy{Configured:false}, which the planner turns into an
// empty plan — i.e. "monitor everything". The controller is never a single
// point of failure for the customer's monitoring.
package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Spec is the policy payload the controller acts on.
type Spec struct {
	Mode               string  `json:"mode"`
	SurgeSamplePct     float64 `json:"surgeSamplePct"`
	StablePoolSelector string  `json:"stablePoolSelector"`
	SurgePoolSelector  string  `json:"surgePoolSelector"`
	FloorNodes         int     `json:"floorNodes"`
}

// Policy is the resolved policy for the cluster.
type Policy struct {
	Configured bool
	Spec       Spec
	Version    string
}

type apiResponse struct {
	Configured bool   `json:"configured"`
	Policy     *Spec  `json:"policy"`
	Version    string `json:"version"`
}

// Client polls the Sunshine policy endpoint with a scoped inbound token.
type Client struct {
	url   string
	token string
	http  *http.Client
	etag  string // last seen ETag, for conditional GET
	last  Policy // last good policy, returned on 304
}

// NewClient builds a policy client. endpoint is the Sunshine base URL.
func NewClient(endpoint, token string, timeout time.Duration) *Client {
	return &Client{
		url:   strings.TrimRight(endpoint, "/") + "/api/autopilot/policy/host-sampling",
		token: token,
		http:  &http.Client{Timeout: timeout},
	}
}

// failOpen is the safe default: monitor everything.
func failOpen() Policy { return Policy{Configured: false} }

// Fetch returns the current policy, failing OPEN on any error. The returned
// error is for logging only — the Policy is always safe to act on.
func (c *Client) Fetch(ctx context.Context) (Policy, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return failOpen(), err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if c.etag != "" {
		req.Header.Set("If-None-Match", c.etag)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return failOpen(), err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified:
		return c.last, nil
	case http.StatusOK:
		var body apiResponse
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return failOpen(), err
		}
		if et := resp.Header.Get("ETag"); et != "" {
			c.etag = et
		}
		p := Policy{Configured: body.Configured, Version: body.Version}
		if body.Configured && body.Policy != nil {
			p.Spec = *body.Policy
		} else {
			p.Configured = false
		}
		c.last = p
		return p, nil
	default:
		// Unauthorized / not found / server error → fail open and re-fetch
		// fresh next time (drop the conditional-GET etag).
		c.etag = ""
		return failOpen(), fmt.Errorf("policy endpoint returned %d", resp.StatusCode)
	}
}
