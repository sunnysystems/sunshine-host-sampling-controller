# Changelog

All notable changes to the Sunshine host-sampling controller are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Documentation

- **Every install command pinned `1.0.1`**, two releases behind. Following the
  runbook literally installed a controller that honours only the first surge
  pool and reports neither what it applied nor the labels it observes — which is
  to say, the docs reproduced the exact failure 1.2.0 exists to prevent.
  Repinned to `1.2.0` across the README, the chart README and both deployment
  guides, with a table of what each version lets Sunshine see, so "should we
  upgrade?" has an answer that is about consequences rather than version numbers.
- **Install the controller BEFORE configuring the policy**, now stated where the
  policy is configured. From 1.2.0 the label dropdown in Sunshine is built from
  what the controller observed (raw Kubernetes keys, badged "from the cluster");
  configure first and it falls back to inferring names from Datadog host tags,
  which rewrite punctuation and yield a selector that matches no node.
- **Two symptoms added to troubleshooting**, both of which cost a design partner
  weeks: `configured: true` with `budget: 0, monitored: 0` tick after tick (a
  selector matching nothing — indistinguishable from a healthy idle fleet), and
  an empty monitored panel while the controller logs reconciles normally
  (reports rejected, or no token issued).

## [1.2.0] - 2026-07-28

### Added

- **The controller now reports the node labels it actually observes.** Each
  reconcile summarizes the label keys present on the cluster's nodes, with the
  distinct values of each, and sends them to Sunshine as raw **Kubernetes** keys.

  This kills an inference that was producing selectors which could never match.
  Sunshine's config UI used to build its "which label distinguishes the nodes"
  dropdown from **Datadog host tags**, and Datadog normalizes punctuation when it
  turns a label into a tag — `karpenter.sh/nodepool` arrives as
  `karpenter_nodepool`. The chosen value was written straight into the policy as
  a Kubernetes selector, so `MatchSelector` looked up a key no node carries: the
  surge pool resolved empty, the budget capped at zero, and the cluster reported
  a serene `monitored: 0` that is indistinguishable from a healthy fleet with
  nothing to sample. Three clusters at one design partner ran that way for weeks,
  saving nothing, with no error surfaced on either side.

  The cluster is the only authority on what its own labels are called, and the
  controller is the only thing standing in it. Purely descriptive: the controller
  acts on the policy it was given and never on this, and Sunshine never refuses a
  report over the field. Size bounds only — a key with more than 12 distinct
  values is dropped **whole** rather than truncated, so a key that is reported
  carries its complete value set. Deciding which labels are *interesting* stays
  on the server, so the two sides cannot drift. Server side:
  sunnysystems/sunnysystems-sunshine#645.

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

### Fixed
- **A nil `SampledNodes` is no longer marshalled as `null`.** `planner.Plan`
  builds `Decision.SampledOut` with `append` over a nil slice, so every reconcile
  that samples nothing — the steady state of a healthy dry-run cluster — reached
  the report client as nil, and Go marshals that as `null` rather than `[]`.
  Sunshine answered `400`, the report was logged and dropped, and the operator's
  audit trail and monitored/dark panel stayed empty. Reconciles were never
  affected (reporting is best-effort and never blocks one, so no node was ever
  mislabelled), but the cluster became invisible from the console. The report now
  always sends an array, keeping "no node was sampled out" an affirmative answer
  rather than a shape the server has to interpret. Present in v1.0.0–v1.1.0
  (#8). The server side accepts `null` as of
  sunnysystems/sunnysystems-sunshine#644, so upgrading is not required to restore
  reporting.

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
