# Deploying the Host-Sampling Controller at a customer

> 🌐 **English** · [Português](DEPLOYMENT.pt-BR.md)

> End-to-end runbook for installing the **Sunshine host-sampling controller** in a
> customer's Kubernetes cluster — from a safe **dry-run** pilot to **execute
> go-live**.
>
> Audience: the customer's **platform / SRE** team (who operate the cluster) plus
> the Sunny engineer shepherding the rollout.
>
> Reference docs: [`../README.md`](../README.md) (controller overview) and
> [`../chart/README.md`](../chart/README.md) (Helm chart reference). This guide is
> the **operational walkthrough**; the READMEs are the reference.

---

## 1. What it is and why it exists

Datadog bills infra/APM hosts at the **p99 of the hourly host count** — i.e. the
**recurring peaks** that define the bill. In Kubernetes the Datadog agent runs as a
**DaemonSet**, so **every surge node becomes a billable host**, even when the surge
nodes are homogeneous, disposable clones (spot/burst).

The controller solves this by keeping:

- the **fixed fleet 100% monitored** (never sampled), and
- **only a sample** of the surge pool monitored, per the policy.

It **runs inside the customer's cluster**, polls its sampling policy from Sunshine,
and reconciles the `datadog.sunshine/sampled-out` node label toward the plan. **The
default is dry-run** (reports only, never touches the cluster). It writes labels
only when the **three locks** in section 3 are satisfied.

> ⚠️ This is the first Sunny artifact that runs in a customer's environment.
> Image distribution, signing and versioning are still being finalized. Treat it
> as an early-adopter and align the distribution path with Sunny.

---

## 2. How it works (one reconcile tick)

Every `POLL_INTERVAL_SECONDS` (default 60s) the controller runs a **tick**. A tick
never brings the process down — a bad tick is logged and the next one recovers.

1. **Poll the policy** — `GET {SUNSHINE_ENDPOINT}/api/autopilot/policy/host-sampling`
   with `Authorization: Bearer <token>`, using `ETag`/`If-None-Match` (cheap
   polling: `304 Not Modified` keeps the cached policy).
2. **Classify the nodes** into two pools, by the policy's `key=value` selectors:
   - **stable** (fixed fleet) → **never** sampled;
   - **surge** → subject to sampling.
   - Nodes matching no selector stay **monitored** (untracked).
   - If a node matches both, **surge takes precedence**.
3. **Plan** how many surge nodes to keep monitored:
   ```
   budget = max(floorNodes, ceil(surgeTotal × surgeSamplePct / 100))
   budget = min(budget, surgeTotal)      // never exceeds the total
   ```
   Keep the **`budget` OLDEST nodes** monitored (stable membership, no flapping);
   the **newest** — the ephemeral spot/burst nodes — are the sampled-out
   candidates.
   `surgeSamplePct = 100` → budget = total → **nothing** is sampled.
4. **Actuate** (only when authorized — see locks): add the label to newly
   sampled-out nodes and **remove** the label from nodes that returned to the
   monitored budget (orphan cleanup). In dry-run it only **reports** via logs +
   plain-text metrics, and writes nothing.
5. **Report** the tick summary to Sunshine (best-effort). A failed report is logged
   and dropped — it **never** blocks or changes the reconcile.

### Fail-open is the core safety property

- Unreachable endpoint, error, `401/404/5xx`, or `configured:false` policy →
  **empty plan** → **nothing is sampled** → **everything stays monitored**.
- The label polarity is fail-open too: a node **without** the label is monitored.
  So "doing nothing" preserves full coverage. The controller is **never** a single
  point of failure for the customer's monitoring.

---

## 3. The three locks (safety model)

The controller writes the label on a node **only when all THREE hold**. Any one of
them at its default keeps the cluster 100% monitored.

| # | Lock | Where | How to enable | Effect |
|---|------|-------|---------------|--------|
| 1 | **Local** | customer cluster (Helm) | `dryRun: false` (`DRY_RUN=false`) | Selects the `LabelActuator` (writes labels) **and** widens RBAC to allow `patch/update` on nodes. |
| 2 | **Server** | Sunshine | `datadogCostGuardHostSamplingExecute` flag **on** for the org + **not** a demo org → server serves `mode: "active"` | Otherwise the server downgrades the policy to `dry_run`. It is Sunny's **central kill-switch**. |
| 3 | **Cluster** | Datadog agent DaemonSet | inverted `nodeAffinity` on the label (section 4) | Without it the label **has no effect**: the agent keeps scheduling on sampled-out nodes → you pay without monitoring. |

**Pausing is as safe as enabling:** turn the flag off (or set the cluster back to
`mode: dry_run`, or `dryRun=true` locally) and the next tick **removes** every
`sampled-out` label, restoring full monitoring.

---

## 4. Enforcement contract (the `nodeAffinity`)

Writing the label only removes the agent from a node if the **Datadog agent
DaemonSet** carries an inverted `nodeAffinity` on that label. Add it to the agent's
pod spec:

```yaml
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
        - matchExpressions:
            # A node WITHOUT the label, or with a value != "true", still schedules
            # the agent → monitored. Fail-open by construction.
            - key: datadog.sunshine/sampled-out
              operator: NotIn
              values: ["true"]
```

- **Fail-open by polarity:** the agent is only kept *off* nodes with
  `sampled-out=true`. Any other state (no label, different value) keeps the agent →
  monitored.
- The controller runs a **read-only preflight** at startup: with
  `agent.daemonsetNamespace` / `agent.daemonsetName` set in Helm, it reads the
  DaemonSet and confirms the affinity, emitting the metric
  `sunshine_host_sampling_enforcement_affinity_present` (`1` present / `0` missing)
  and logging a warning if it is absent.

> The preflight accepts either `operator: NotIn` with `values: ["true"]` or
> `operator: DoesNotExist` on the same `key`.

---

## 5. Prerequisites (checklist before you start)

- [ ] **Kubernetes cluster** with the **Datadog agent running as a DaemonSet**.
      Note the DaemonSet's **namespace** and **name** (e.g. `datadog` /
      `datadog-agent`).
- [ ] Cluster access with `kubectl` + `helm`, with permission to create a
      **ClusterRole/ClusterRoleBinding** and a **ServiceAccount** (the controller
      uses cluster-scoped RBAC because it lists/patches nodes).
- [ ] **Labelled nodes** distinguishing the fixed fleet from the surge pool via a
      simple `key=value` selector (e.g. `sunshine.io/pool=stable` and
      `sunshine.io/pool=surge`, or reusing existing node-pool labels like
      `cloud.google.com/gke-nodepool=...`, `eks.amazonaws.com/nodegroup=...`).
      > ⚠️ The matcher only understands **exact `key=value`** — no compound
      > expressions, `In`, ranges, etc. If the pools don't yet have a deterministic
      > label, **create it on the node pools first**, before configuring the policy.
- [ ] **Access to the image** `ghcr.io/sunnysystems/host-sampling-controller` (the
      customer must be able to `pull` it; if the registry is private, configure
      `imagePullSecrets` or mirror the image to an internal registry).
      *(Image distribution is still being finalized — confirm the distribution path
      for this customer with Sunny.)*
- [ ] **Inbound token** issued in Sunshine (**Autopilot → Component tokens**),
      **scoped to (org, cluster)** and **read-only**. It is the same token used to
      fetch the policy and to report.
- [ ] **Sunshine endpoint** (base URL) and the **cluster id** — the `cluster id`
      **must match** the token's scope.
- [ ] **HTTPS egress** from the cluster to the Sunshine endpoint allowed.

---

## 6. Phase 1 — Dry-run pilot

Goal: install with no risk, observe the plan, and validate pool classification. No
node is touched.

### 6.1 Create the Secret with the token

```sh
kubectl create secret generic host-sampling-token \
  --from-literal=token=<SUNSHINE_TOKEN>
```

### 6.2 Install the chart (dry-run is the default)

```sh
helm install host-sampling ./chart \
  --set sunshine.endpoint=https://app.sunshine.example.com \
  --set sunshine.clusterId=prod-us-east-1 \
  --set sunshine.tokenSecretName=host-sampling-token \
  --set agent.daemonsetNamespace=datadog \
  --set agent.daemonsetName=datadog-agent
```

- `dryRun` is `true` by default → **read-only** RBAC (`get/list/watch` on `nodes`,
  `get/list` on `daemonsets`).
- Setting `agent.daemonsetNamespace/Name` here already enables the **affinity
  preflight**, so you find out early whether enforcement is ready.

### 6.3 Confirm it came up

```sh
kubectl get pods -l app.kubernetes.io/name=sunshine-host-sampling-controller
kubectl logs deploy/host-sampling
```

In the startup log (JSON) you should see `host-sampling-controller started` with
`dryRun=true`, the endpoint and the cluster. Health at `/healthz` (liveness +
readiness).

### 6.4 Configure the policy in Sunshine

On the Sunshine side, configure the cluster's host-sampling policy (leave it in
**`mode: dry_run`** for now):

- `stablePoolSelector` — e.g. `sunshine.io/pool=stable`
- `surgePoolSelector` — e.g. `sunshine.io/pool=surge`
- `surgeSamplePct` — % of the surge to keep monitored (e.g. `20`)
- `floorNodes` — minimum floor of monitored surge nodes (e.g. `2`)

### 6.5 Read the metrics and validate the plan

```sh
kubectl port-forward deploy/host-sampling 9090:9090
curl -s localhost:9090/metrics | grep sunshine_host_sampling
```

Interpret:

| Metric | Expected in the pilot |
|--------|-----------------------|
| `sunshine_host_sampling_policy_configured` | `1` (if `0`, the policy didn't arrive — see Troubleshooting) |
| `sunshine_host_sampling_stable_nodes` | = real count of fixed-fleet nodes |
| `sunshine_host_sampling_surge_nodes` | = real count of surge nodes |
| `sunshine_host_sampling_monitored_nodes` | = computed `budget` |
| `sunshine_host_sampling_would_sample_out_nodes` | surge that **would** be sampled (reported only in dry-run) |
| `sunshine_host_sampling_enforcement_affinity_present` | ideally `1` (see Phase 2) |

Let it run for **a few real peak cycles** to confirm that pool classification and
the per-pool baseline are stable (no flapping) and that the nodes appearing in
`would_sample_out` are genuinely the disposable ones.

---

## 7. Phase 2 — Validation before go-live

Only proceed to execute with **every** item below green:

- [ ] **Enforcement ready:** `sunshine_host_sampling_enforcement_affinity_present == 1`.
      If `0`, add the `nodeAffinity` from section 4 to the Datadog agent DaemonSet
      and confirm the preflight starts reporting `1`. **Without it, sampling a node
      does not remove the agent → no savings (phantom savings).**
- [ ] **Right target:** the nodes in `would_sample_out` are genuinely
      disposable surge/spot, and **no** fixed-fleet node appears there.
- [ ] **Gate 1 — forecast accuracy:** the projected savings match the
      cost invoice/snapshot (the already-validated cost-reconciliation pipeline
      feeds this gate).
- [ ] **Gate 2 — live validation:** the mutation was validated against a real
      Datadog account (the label actually drains the agent and the host count drops).
- [ ] **Token scope** correct (org, cluster) and the org is **not** a demo.

---

## 8. Phase 3 — Execute go-live (turn on the three locks)

Turn the locks on **in this order** — so that even if you stop midway, the cluster
stays safe:

1. **Cluster lock (already done in Phase 2):** ensure the `nodeAffinity` on the
   agent DaemonSet (`enforcement_affinity_present == 1`).
2. **Server lock (Sunshine side):** turn on the
   `datadogCostGuardHostSamplingExecute` flag for the org (non-demo) and set the
   policy to **`mode: active`**.
3. **Local lock (customer cluster):** enable the `LabelActuator` and widen RBAC:
   ```sh
   helm upgrade host-sampling ./chart --reuse-values --set dryRun=false
   ```

### What to watch after go-live

- Logs start showing `host-sampling: reconciled labels` with `actuate=true`.
- Metrics: `sunshine_host_sampling_labels_applied_total` rises; the
  `would_sample_out` nodes actually become `sampled-out=true`.
- In Datadog: the agent is **drained** from those nodes and the **host count
  drops** (the billable effect we're after).

```sh
# Effectively sampled nodes:
kubectl get nodes -l datadog.sunshine/sampled-out=true
```

---

## 9. Operations and observability

### Metrics (`:9090/metrics`)

An HTTP endpoint that returns **plain text** with the controller's state. It needs
no extra tooling: read it directly with `curl` (see 6.5) and, if the customer wants
to ingest it, the **Datadog agent itself** scrapes this endpoint via its
**OpenMetrics** check — no additional collector in between.

| Metric | Type | Meaning |
|--------|------|---------|
| `sunshine_host_sampling_stable_nodes` | gauge | Nodes in the fixed (stable) fleet. |
| `sunshine_host_sampling_surge_nodes` | gauge | Nodes in the surge pool. |
| `sunshine_host_sampling_monitored_nodes` | gauge | Surge nodes kept monitored (budget). |
| `sunshine_host_sampling_would_sample_out_nodes` | gauge | Surge the plan would sample out (never applied in dry-run). |
| `sunshine_host_sampling_policy_configured` | gauge | `1` = policy configured; `0` = fail-open. |
| `sunshine_host_sampling_enforcement_affinity_present` | gauge | `1` = DaemonSet has the anti-affinity (only emitted if the preflight ran). |
| `sunshine_host_sampling_reconcile_ticks_total` | counter | Total reconcile ticks. |
| `sunshine_host_sampling_policy_fetch_errors_total` | counter | Policy fetch errors (each fails open). |
| `sunshine_host_sampling_labels_applied_total` | counter | `sampled-out` labels written (execute). |
| `sunshine_host_sampling_labels_cleared_total` | counter | Labels removed (orphan cleanup / pause). |
| `sunshine_host_sampling_label_errors_total` | counter | Per-node patch failures. |

### Logs

Structured JSON on stdout. Key lines:
- `host-sampling-controller started` — startup (shows `dryRun`, endpoint, cluster).
- `dry-run: no cluster changes` — plan in dry-run.
- `host-sampling: reconciled labels` — actuation (shows `actuate`, `applied`, `cleared`, `errors`).
- `policy fetch failed — failing open ...` — fetch failed; monitoring everything.
- `enforcement preflight: ...` — affinity preflight result.

### Health

`/healthz` responds `200 ok` — used as both liveness **and** readiness probe.

---

## 10. Pause, rollback and kill-switch

All the ways below are **safe**: the next tick **removes** the `sampled-out` labels
and restores full monitoring.

| Where | Action | When to use |
|-------|--------|-------------|
| **Server (Sunshine)** | Turn off `datadogCostGuardHostSamplingExecute` **or** set policy → `mode: dry_run` | Sunny's central kill-switch; pauses **without** touching the cluster. |
| **Local (Helm)** | `helm upgrade ... --set dryRun=true` | Customer-initiated pause; returns to the read-only actuator. |
| **Automatic** | Sunshine endpoint unreachable/error | Fail-open: empty plan, nothing sampled. |

To **uninstall** completely:

```sh
helm uninstall host-sampling
# If any node kept the label (e.g. uninstall mid-execute), clean it:
kubectl label nodes --all datadog.sunshine/sampled-out-
```

---

## 11. Security and footprint

- **Minimal RBAC:** in dry-run, only `get/list/watch` on `nodes` and `get/list` on
  `daemonsets` (preflight). The `patch/update` verbs on `nodes` are granted only
  when `dryRun=false`.
- **Hardened container:** distroless image, `runAsNonRoot`,
  `readOnlyRootFilesystem`, `allowPrivilegeEscalation: false`, `drop: ["ALL"]`,
  `seccompProfile: RuntimeDefault`.
- **Token** mounted as a read-only Secret at `/var/run/sunshine/token` (via
  `SUNSHINE_TOKEN_FILE`, preferred over an env var).
- **Footprint:** 1 replica; requests `25m` CPU / `32Mi` mem, limits `100m` / `64Mi`.
- **Only write path** to the cluster: a `PATCH` of a node label (strategic-merge),
  in the `LabelActuator`.

---

## 12. Troubleshooting

| Symptom | Likely cause | Action |
|---------|--------------|--------|
| `policy_configured = 0` | Policy not configured in Sunshine, or `401/404/5xx`, or wrong token | Check the token/endpoint/cluster id and the policy in Sunshine. Fail-open meanwhile: everything monitored. |
| `policy_fetch_errors_total` rising | Egress/DNS/network or invalid token | Test HTTPS connectivity from the pod to the endpoint; revalidate the token. |
| `enforcement_affinity_present = 0` | Agent DaemonSet **without** the `nodeAffinity` | Add the affinity from section 4 to the agent DaemonSet. |
| Preflight doesn't emit the metric | `agent.daemonsetNamespace/Name` not set | Set both in Helm. |
| `dryRun=false` but no label applied | Server does **not** serve `mode: active` (flag off or demo org) | Turn on `datadogCostGuardHostSamplingExecute`, ensure non-demo, policy `active`. |
| Nodes with `sampled-out=true` but the host count **doesn't** drop in Datadog | Enforcement missing (nodeAffinity) → agent stays on the node | Add the `nodeAffinity` (section 4). |
| Plan oscillates (nodes come/go) | `surgeSamplePct`/`floorNodes` on the edge of a volatile pool | Adjust `floorNodes`/`surgeSamplePct`; membership is oldest-first, but a very volatile surge still oscillates. |
| `label_errors_total > 0` | RBAC or patch conflict | Confirm `dryRun=false` granted `patch/update` on `nodes`; check per-node logs. |

---

## 13. Configuration reference

### Environment variables (the chart fills these via `values`)

| Var | Required | Default | Meaning |
|-----|----------|---------|---------|
| `SUNSHINE_ENDPOINT` | yes | — | Sunshine base URL |
| `CLUSTER_ID` | yes | — | cluster id (must match the token's scope) |
| `SUNSHINE_TOKEN_FILE` / `SUNSHINE_TOKEN` | yes | — | inbound token (file preferred — a mounted Secret) |
| `POLL_INTERVAL_SECONDS` | no | `60` | reconcile interval |
| `DRY_RUN` | no | `true` | `false` selects the `LabelActuator` |
| `AGENT_DAEMONSET_NAMESPACE` | no | — | agent DaemonSet namespace (preflight) |
| `AGENT_DAEMONSET_NAME` | no | — | agent DaemonSet name (preflight) |
| `METRICS_ADDR` | no | `:9090` | metrics/health listen address |

### Helm chart values

| Key | Default | Notes |
|-----|---------|-------|
| `sunshine.endpoint` | `""` | **required** |
| `sunshine.clusterId` | `""` | **required** — matches the token's scope |
| `sunshine.tokenSecretName` | `""` | **required** — Secret with the token |
| `sunshine.tokenSecretKey` | `token` | key within the Secret |
| `pollIntervalSeconds` | `60` | reconcile interval |
| `dryRun` | `true` | Local lock #1 — leave `true` until validated |
| `agent.daemonsetNamespace` | `""` | enables the affinity preflight |
| `agent.daemonsetName` | `""` | enables the affinity preflight |
| `metrics.port` | `9090` | `/metrics` + `/healthz` |
| `image.repository` | `ghcr.io/sunnysystems/host-sampling-controller` | |
| `image.tag` | `""` | defaults to the chart `appVersion` |

### Policy API contract (reference)

```
GET {endpoint}/api/autopilot/policy/host-sampling
Authorization: Bearer <token>
200 → {"configured":bool,
       "policy":{"mode","surgeSamplePct","stablePoolSelector",
                 "surgePoolSelector","floorNodes"},
       "version":string}          (+ ETag header)
304 → not modified (keeps the cached policy)
401/404/5xx → treated as unconfigured (FAIL OPEN)

POST {endpoint}/api/autopilot/report/host-sampling   (best-effort, same auth)
```

---

## 14. Known limitations

- **Image distribution/signing** is still being finalized. Align the distribution
  path with Sunny before installing at a customer.
- **Pool selector** only understands **exact `key=value`** — no compound
  expressions. Ensure deterministic labels on the node pools.
- **First in-customer artifact** — treat it as an early-adopter
  (support/versioning evolving).
- **Only reduces host count** (infra/APM host-based). Other cost dimensions (RUM,
  logs, APM ingestion, custom metrics) are covered by other autopilot mechanisms.
