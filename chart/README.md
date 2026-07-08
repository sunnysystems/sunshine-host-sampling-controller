# sunshine-host-sampling-controller (Helm chart)

Installs the Sunshine host-sampling controller. It polls the cluster's sampling
policy from Sunshine and reconciles the `datadog.sunshine/sampled-out` node label
toward the plan. It **defaults to dry-run** (reports only, never mutates the
cluster) and writes labels only when all three locks are satisfied — see the
[controller README](../README.md) for the full model.

## Install

1. Create a Secret with the inbound token issued in Sunshine (Autopilot →
   Component tokens):

   ```sh
   kubectl create secret generic host-sampling-token --from-literal=token=<token>
   ```

2. Install the chart (dry-run is the default):

   ```sh
   helm install host-sampling ./chart \
     --set sunshine.endpoint=https://app.sunshine.example.com \
     --set sunshine.clusterId=prod-us-east-1 \
     --set sunshine.tokenSecretName=host-sampling-token
   ```

## Values

| Key                        | Default                                         | Notes                                          |
| -------------------------- | ----------------------------------------------- | ---------------------------------------------- |
| `sunshine.endpoint`        | `""`                                            | **required** — Sunshine base URL               |
| `sunshine.clusterId`       | `""`                                            | **required** — must match the token scope      |
| `sunshine.tokenSecretName` | `""`                                            | **required** — Secret with the token           |
| `sunshine.tokenSecretKey`  | `token`                                         | key within that Secret                         |
| `pollIntervalSeconds`      | `60`                                            | reconcile interval                             |
| `dryRun`                   | `true`                                          | `false` enables the label-writing actuator     |
| `agent.daemonsetNamespace` | `""`                                            | agent DaemonSet namespace (enforcement preflight) |
| `agent.daemonsetName`      | `""`                                            | agent DaemonSet name (enforcement preflight)   |
| `metrics.port`             | `9090`                                          | `/metrics` + `/healthz`                        |
| `image.repository`         | `ghcr.io/sunnysystems/host-sampling-controller` |                                                |
| `image.tag`                | `""`                                            | defaults to the chart `appVersion`             |

## RBAC

The chart grants a ClusterRole scoped to `nodes` with **`get/list/watch` only**
while `dryRun=true`, plus `get/list` on `daemonsets` for the enforcement preflight.
The `patch/update` verbs on `nodes` (needed to write the sampled-out label) are
added **only** when `dryRun=false`. Even then, the controller writes labels only
when Sunshine serves policy `mode: "active"` — see the three locks in the
[controller README](../README.md).

Before setting `dryRun=false`, apply the enforcement contract (the inverted
`nodeAffinity` on the Datadog agent DaemonSet) and set
`agent.daemonsetNamespace`/`agent.daemonsetName` so the preflight can verify it.

## Metrics

`GET :9090/metrics` returns plain text (OpenMetrics-compatible — the Datadog agent
can scrape it): stable/surge/monitored/would-sample-out node counts,
`policy_configured` (0 = fail-open), reconcile ticks, fetch errors, and the
labels-applied/cleared/errors counters.
