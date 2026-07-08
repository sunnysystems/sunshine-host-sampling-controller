# Changelog

All notable changes to the Sunshine host-sampling controller are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.0] - 2026-07-08

First public release (Apache-2.0). Peak host sampling for Kubernetes: keep the
fixed fleet fully monitored and monitor only a sample of the surge pool to trim
the Datadog host-count bill.

### Added

- **Policy polling** — fetches the cluster's host-sampling policy from Sunshine
  (`GET /api/autopilot/policy/host-sampling`) with a scoped, read-only inbound
  token, using ETag / conditional `GET` for cheap polling.
- **Fail-open safety** — any policy fetch error, `4xx/5xx`, or unconfigured policy
  yields an empty plan (monitor everything); a node without the label is always
  monitored. The controller is never a single point of failure for monitoring.
- **Pool classification** — splits nodes into a stable (fixed) pool and a surge
  pool via simple `key=value` selectors; the stable pool is never sampled.
- **Deterministic planner** — keeps `budget = max(floorNodes, ceil(surgeTotal ×
  surgeSamplePct/100))` surge nodes monitored, oldest-first (stable membership, no
  flapping).
- **Triple-locked execute** — labels a node only when all three hold: local
  `DRY_RUN=false`, server-served policy `mode: "active"`, and the agent DaemonSet's
  inverted `nodeAffinity` on `datadog.sunshine/sampled-out`. Dry-run is the
  default; pausing any lock restores full monitoring on the next tick.
- **Label reconcile with orphan cleanup** — writes/removes the sampled-out label
  toward the plan; removes stale labels when paused or when a node re-enters the
  monitored budget.
- **Enforcement preflight** — read-only startup check that the agent DaemonSet
  carries the required anti-affinity, surfaced as
  `sunshine_host_sampling_enforcement_affinity_present`.
- **Best-effort reporting** — posts each reconcile summary to Sunshine; a failed
  report never blocks or changes a reconcile.
- **Metrics & health** — plain-text metrics endpoint (OpenMetrics-compatible) on
  `:9090/metrics` and a `/healthz` liveness/readiness probe.
- **Helm chart** — `chart/` with RBAC that is read-only in dry-run and widens to
  node `patch/update` only when `dryRun=false`.

[Unreleased]: https://github.com/sunnysystems/sunshine-host-sampling-controller/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/sunnysystems/sunshine-host-sampling-controller/releases/tag/v1.0.0
