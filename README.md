# Sunshine host-sampling controller

An in-cluster Kubernetes controller for **peak host sampling**. It runs
in the customer's cluster, polls its host-sampling policy from Sunshine, and
reconciles the `datadog.sunshine/sampled-out` node label toward the plan to trim
the Datadog host-count bill. It **defaults to dry-run** (reports only, never
mutates) and writes labels only when all three locks below are satisfied.

> ⚠️ This is the first Sunshine artifact that runs in a customer's environment.
> Versioning, license, and support policy are in place (v1.0.0, Apache-2.0 —
> see [`LICENSE`](LICENSE), [`CONTRIBUTING.md`](CONTRIBUTING.md),
> [`SECURITY.md`](SECURITY.md)). Each release publishes a cosign-signed
> multi-arch image and a signed OCI Helm chart to GHCR — see
> [Verifying release artifacts](#verifying-release-artifacts).

## What it does

Datadog bills infra/APM hosts at the **p99 of the hourly host count**, driven by
recurring peaks. In Kubernetes the agent runs as a DaemonSet, so every surge node
is a billable host — even when surge nodes are homogeneous, disposable clones.
This controller keeps the fixed fleet 100% monitored and, per the policy, keeps
only a sample of the surge pool monitored.

- **Poll** `GET {SUNSHINE_ENDPOINT}/api/autopilot/policy/host-sampling` with
  `Authorization: Bearer <token>` (a scoped, read-only inbound token),
  using ETag/`If-None-Match` for cheap polling.
- **Classify** nodes into the stable pool (never sampled) and the surge pool,
  using the `key=value` selectors from the policy.
- **Plan**: keep `budget = max(floorNodes, ceil(surgeTotal × surgeSamplePct/100))`
  surge nodes monitored, **oldest-first** (stable membership, no flapping); the
  rest are labelled `datadog.sunshine/sampled-out=true`.
- **Reconcile** labels toward the plan when actuating: add the label to newly
  sampled-out nodes, and remove it from nodes that are back in the monitored
  budget (orphan cleanup). In dry-run it only **reports** the plan via logs +
  plain-text metrics on `:9090/metrics` (OpenMetrics-compatible) and writes nothing.

## Enabling execute (the three locks)

The controller labels a node only when **all three** hold — any one left at its
default keeps the cluster fully monitored:

1. **Local:** `DRY_RUN=false` (Helm `dryRun: false`) selects the label-writing
   actuator and widens RBAC to allow node patches.
2. **Server:** Sunshine serves policy `mode: "active"`. The server downgrades the
   served mode to `dry_run` unless the org's `datadogCostGuardHostSamplingExecute`
   flag is on and it is not a demo org — so Sunshine is the central kill-switch.
3. **Cluster:** the Datadog agent DaemonSet carries the inverted nodeAffinity
   below. **Without it the label has no effect** — the agent keeps scheduling on
   sampled-out nodes, so you pay for them with no monitoring benefit.

Pausing is just as safe: flip `datadogCostGuardHostSamplingExecute` off (or set a
cluster back to `mode: dry_run`) and the next tick **removes** every sampled-out
label, restoring full monitoring.

## Enforcement contract (required for execute)

Add this to the **Datadog agent DaemonSet** pod spec so its pods refuse to
schedule on sampled-out nodes:

```yaml
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
        - matchExpressions:
            # A node WITHOUT the label, or with a value != "true", still
            # schedules the agent → monitored. Fail-open by construction.
            - key: datadog.sunshine/sampled-out
              operator: NotIn
              values: ["true"]
```

The controller runs a read-only **preflight** on startup: set
`agent.daemonsetNamespace` / `agent.daemonsetName` (Helm) and it verifies the
affinity is present, logging a warning and reporting
`sunshine_host_sampling_enforcement_affinity_present 0` if it is missing.

## Fail-open (safety)

The controller is never a single point of failure for monitoring. If the policy
endpoint is unreachable, returns an error, or reports `configured:false`, the plan
is **empty** → nothing is sampled → everything stays monitored. The label polarity
is also fail-open by design: a node **without** the label is monitored, so doing
nothing keeps full coverage.

## Configuration (env)

| Var                                      | Required | Default | Meaning                                           |
| ---------------------------------------- | -------- | ------- | ------------------------------------------------- |
| `SUNSHINE_ENDPOINT`                      | yes      | —       | Sunshine base URL                                 |
| `CLUSTER_ID`                             | yes      | —       | cluster id (must match the token's scope)         |
| `SUNSHINE_TOKEN` / `SUNSHINE_TOKEN_FILE` | yes      | —       | inbound token (file preferred — a mounted Secret) |
| `POLL_INTERVAL_SECONDS`                  | no       | `60`    | reconcile interval                                |
| `DRY_RUN`                                | no       | `true`  | `false` selects the label-writing actuator        |
| `AGENT_DAEMONSET_NAMESPACE`              | no       | —       | agent DaemonSet namespace (enforcement preflight) |
| `AGENT_DAEMONSET_NAME`                   | no       | —       | agent DaemonSet name (enforcement preflight)      |
| `METRICS_ADDR`                           | no       | `:9090` | metrics/health listen address                     |

## Build & test

Building locally requires **Go 1.25+**; `make docker` needs only Docker (the
image is a self-contained multi-stage build).

```sh
make check          # gofmt + vet + test + build
make docker         # build the container image
```

The pure packages (`policy`, `node`, `planner`, `actuator`, `reconcile`,
`metrics`, `config`) have **no Kubernetes dependency** and unit-test offline;
`kube`/`cmd` are the thin client-go integration layer.

[`e2e/run.sh`](e2e/run.sh) validates the full execute path on a local
[kind](https://kind.sigs.k8s.io) cluster — the enforcement contract (label →
agent eviction) and the controller actuating against a stub policy. It is the
same check CI runs (`.github/workflows/e2e.yml`):

```sh
kind create cluster --name hs-e2e --config e2e/kind.yaml --wait 120s
KIND_CLUSTER=hs-e2e bash e2e/run.sh
```

### Using your own image

To deploy a build of your own (e.g. after an audit, or to serve it from an
internal registry), build and push the image, then point the chart at it:

```sh
make docker IMAGE=registry.example.com/host-sampling-controller:1.0.1-audit
docker push registry.example.com/host-sampling-controller:1.0.1-audit
helm install host-sampling ... \
  --set image.repository=registry.example.com/host-sampling-controller \
  --set image.tag=1.0.1-audit
```

## Deploy

For the full operational runbook — dry-run pilot through execute go-live — see
**[`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md)** ([Português](docs/DEPLOYMENT.pt-BR.md)).
See [`chart/README.md`](chart/README.md) for the Helm chart reference. In short:

```sh
kubectl create secret generic host-sampling-token --from-literal=token=<token>
helm install host-sampling \
  oci://ghcr.io/sunnysystems/charts/sunshine-host-sampling-controller \
  --version 1.0.1 \
  --set sunshine.endpoint=https://app.sunshine.example.com \
  --set sunshine.clusterId=prod-us-east-1 \
  --set sunshine.tokenSecretName=host-sampling-token
```

(From a source checkout, `./chart` works in place of the OCI reference.)

The chart grants **read-only** node access in dry-run (`get/list/watch` on nodes,
`get/list` on daemonsets for the preflight); the `patch` verb on nodes is
added only when `dryRun=false`. Before setting `dryRun=false`, apply the
[enforcement contract](#enforcement-contract-required-for-execute) and set
`agent.daemonsetNamespace`/`agent.daemonsetName` so the preflight can verify it.

## Compatibility & updates

**Versioning.** The controller and chart follow [SemVer](https://semver.org). Each
release is tagged, recorded in [`CHANGELOG.md`](CHANGELOG.md), and ships a
cosign-signed multi-arch image plus a signed OCI Helm chart.

**Server API compatibility.** The policy contract this controller polls
(`/api/autopilot/policy/host-sampling`) evolves **additively / backward-compatible
only**: new optional fields may appear, but existing fields are never removed or
repurposed. Any released controller keeps working against the current Sunshine
server — you don't have to upgrade in lockstep.

**Getting updates.** Watch this repo's **Releases** for new versions (especially
security fixes) and upgrade with `helm upgrade` to the new chart version (the image
is pinned by the chart `appVersion`). Report vulnerabilities privately per
[`SECURITY.md`](SECURITY.md).

### Verifying release artifacts

Both the image and the chart are signed keylessly with
[cosign](https://docs.sigstore.dev/) by the release workflow; the signing
identity is this repo's `release.yml` on the version tag:

```sh
cosign verify ghcr.io/sunnysystems/host-sampling-controller:1.0.1 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp \
  '^https://github\.com/sunnysystems/sunshine-host-sampling-controller/\.github/workflows/release\.yml@refs/tags/v'

cosign verify ghcr.io/sunnysystems/charts/sunshine-host-sampling-controller:1.0.1 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp \
  '^https://github\.com/sunnysystems/sunshine-host-sampling-controller/\.github/workflows/release\.yml@refs/tags/v'
```

The image also carries SBOM and SLSA provenance attestations, inspectable with
`docker buildx imagetools inspect ghcr.io/sunnysystems/host-sampling-controller:1.0.1`.

## Contributing & support

Issues are welcome (bug reports, questions, feature requests) and auditing is
encouraged — this is open source precisely so the teams that run it can read it.
**External pull requests are not accepted:** the canonical source is maintained by
Sunny and mirrored here. See [`CONTRIBUTING.md`](CONTRIBUTING.md). Report
vulnerabilities privately per [`SECURITY.md`](SECURITY.md).

## License

Licensed under the Apache License, Version 2.0 — see [`LICENSE`](LICENSE) and
[`NOTICE`](NOTICE). Copyright 2026 Sunny Systems.
