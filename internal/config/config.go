// Package config loads the controller configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the controller's runtime configuration.
type Config struct {
	Endpoint     string        // Sunshine base URL
	ClusterID    string        // the cluster's resource id (matches the token scope)
	Token        string        // scoped, read-only inbound token
	PollInterval time.Duration // how often to reconcile
	DryRun       bool          // false selects the label-writing LabelActuator
	MetricsAddr  string        // metrics/health listen address

	// AgentDaemonSetNamespace/Name identify the Datadog agent DaemonSet for the
	// enforcement preflight. Both optional — when either is empty the preflight
	// is skipped (the affinity metric is not emitted).
	AgentDaemonSetNamespace string
	AgentDaemonSetName      string
}

// FromEnv reads configuration. Token comes from SUNSHINE_TOKEN or, preferred,
// a file at SUNSHINE_TOKEN_FILE (a mounted Secret).
func FromEnv() (Config, error) {
	c := Config{
		Endpoint:                strings.TrimSpace(os.Getenv("SUNSHINE_ENDPOINT")),
		ClusterID:               strings.TrimSpace(os.Getenv("CLUSTER_ID")),
		PollInterval:            60 * time.Second,
		DryRun:                  true,
		MetricsAddr:             ":9090",
		AgentDaemonSetNamespace: strings.TrimSpace(os.Getenv("AGENT_DAEMONSET_NAMESPACE")),
		AgentDaemonSetName:      strings.TrimSpace(os.Getenv("AGENT_DAEMONSET_NAME")),
	}

	token, err := loadToken()
	if err != nil {
		return c, err
	}
	c.Token = token

	if v := os.Getenv("POLL_INTERVAL_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return c, fmt.Errorf("invalid POLL_INTERVAL_SECONDS: %q", v)
		}
		c.PollInterval = time.Duration(n) * time.Second
	}
	if v := os.Getenv("DRY_RUN"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return c, fmt.Errorf("invalid DRY_RUN: %q", v)
		}
		c.DryRun = b
	}
	if v := strings.TrimSpace(os.Getenv("METRICS_ADDR")); v != "" {
		c.MetricsAddr = v
	}

	if c.Endpoint == "" {
		return c, fmt.Errorf("SUNSHINE_ENDPOINT is required")
	}
	if c.ClusterID == "" {
		return c, fmt.Errorf("CLUSTER_ID is required")
	}
	if c.Token == "" {
		return c, fmt.Errorf("a token is required (SUNSHINE_TOKEN or SUNSHINE_TOKEN_FILE)")
	}
	return c, nil
}

func loadToken() (string, error) {
	if path := strings.TrimSpace(os.Getenv("SUNSHINE_TOKEN_FILE")); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("reading SUNSHINE_TOKEN_FILE: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return strings.TrimSpace(os.Getenv("SUNSHINE_TOKEN")), nil
}
