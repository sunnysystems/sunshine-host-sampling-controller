# Changelog

All notable changes to the Sunshine host-sampling controller are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.2.0] - 2026-07-17

### Added

- **The controller now reports what it understood.** Each reconcile report
  echoes the surge selectors it actually applied, the version of the policy it
  acted on, and its own build version (also sent as a `User-Agent`). Sunshine
  uses this to tell an operator when the controller in their cluster cannot
  honour the policy they configured — for example, a pre-1.1.0 controller given
  four surge pools samples only the first, which was previously invisible.

  The echo describes; it never gates. Sunshine accepts and audits every report
  regardless of version, and a controller that sends none of these fields is
  treated as **unknown**, never as broken.

- **Build version stamped at link time** (`-ldflags -X …/internal/buildinfo.Version`),
  wired through the release workflow from the image tag. An unstamped build
  reports `dev` and works normally.

### Compatibility

- Fully backward compatible in both directions. The new fields are additive and
  optional; an older Sunshine ignores them, and this controller talks to any
  Sunshine that predates them. No configuration change, no chart values change.

## [1.1.0] - 2026-07-16

### Added

- **Multiple surge nodepools per cluster.** The policy now carries
  `surgePoolSelectors` (a list); a node matching **any** of them is surge. A
  Karpenter cluster routinely has several burst pools, and only one could be
  named before — the rest stayed fully monitored, silently costing the operator
  savings they thought they had configured.

### Changed

- **The permanent (stable) pool is now derived.** `stablePoolSelector` is
  optional and reporting-only: a node matching no surge selector is left
  monitored by construction, so the fixed fleet never needed declaring. When it
  is empty, the reported stable pool is "everything that is not surge", which is
  what the fleet actually does. An explicit selector still narrows the report.
  A policy declaring nothing at all (fail-open) still reports no pools.

### Compatibility

- **Both directions are safe, so controller and server can upgrade in any
  order.** This controller prefers `surgePoolSelectors` and falls back to the
  legacy `surgePoolSelector` scalar, so it keeps working against a server that
  predates the list. A current server keeps sending the scalar (the first pool),
  so a controller predating this release keeps sampling that pool rather than
  silently sampling nothing.
- Operators running several surge pools must upgrade to **1.1.0** for the extra
  pools to be honoured; on an older controller they are simply left monitored.

## [1.0.1] - 2026-07-13

### Security

- **Release workflow:** pass GHCR credentials via `env:` and
  `helm registry login --password-stdin` instead of interpolating
  `${{ github.actor }}` / `${{ secrets.GITHUB_TOKEN }}` directly into the shell
  command (defense against script injection; keeps the token off the process
  argument list).
- **RBAC least privilege:** execute mode now grants only `patch` on nodes (the
  unused `update` verb was dropped — the controller only issues a strategic-merge
  PATCH).
- **Cleartext-token guard:** warn at startup when `SUNSHINE_ENDPOINT` is not
  `https://`, since the inbound token would otherwise be sent in cleartext.

### Documentation

- **Signed artifacts documented:** the docs no longer say image
  distribution/signing are "in progress" — releases publish a public,
  cosign-signed multi-arch image and a signed OCI Helm chart to GHCR, with
  SBOM and SLSA provenance attestations.
- **Artifact verification:** `cosign verify` commands (image + chart, keyless
  identity pinned to the release workflow) in the README and both deployment
  runbooks.
- **OCI chart install:** install/upgrade examples now use the published
  `oci://ghcr.io/sunnysystems/charts/...` chart, with `./chart` kept as the
  from-source alternative.
- **Build-your-own-image path:** `make docker IMAGE=...` → push → point the
  chart at it via `image.repository`/`image.tag`.
- **Local validation:** documented the Go 1.25+ build prerequisite and the
  `e2e/run.sh` kind-based end-to-end check (same as CI).

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

[Unreleased]: https://github.com/sunnysystems/sunshine-host-sampling-controller/compare/v1.0.1...HEAD
[1.0.1]: https://github.com/sunnysystems/sunshine-host-sampling-controller/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/sunnysystems/sunshine-host-sampling-controller/releases/tag/v1.0.0
