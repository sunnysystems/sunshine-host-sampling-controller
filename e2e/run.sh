#!/usr/bin/env bash
# End-to-end validation of the host-sampling EXECUTE path on a real (kind)
# cluster — the de-risk for the label→eviction "wire shape".
#
# It proves three things against a live API server:
#   1. Enforcement contract: labelling a node datadog.sunshine/sampled-out=true
#      evicts the agent DaemonSet pod (and removing it restores the pod).
#   2. Controller actuation: the real controller, in execute mode against a stub
#      policy (mode=active), labels exactly one of two surge nodes (oldest-first)
#      and never touches the stable node.
#   3. Full chain: that controller-sampled node then loses its agent pod, while
#      the monitored surge node and the stable node keep theirs.
#
# Run locally with a kind cluster already created from e2e/kind.yaml:
#   KIND_CLUSTER=hs-e2e bash e2e/run.sh
set -euo pipefail

KIND_CLUSTER="${KIND_CLUSTER:-hs-e2e}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
LABEL="datadog.sunshine/sampled-out"

log() { echo -e "\n=== $* ==="; }

node_label() { kubectl get node "$1" -o jsonpath="{.metadata.labels.datadog\.sunshine/sampled-out}"; }
agent_pods_on() {
  kubectl -n datadog get pods -l app=datadog-agent \
    --field-selector "spec.nodeName=$1,status.phase=Running" -o name 2>/dev/null | grep -c . || true
}

dump_debug() {
  log "DEBUG DUMP"
  kubectl get nodes --show-labels || true
  kubectl -n datadog get pods -o wide || true
  kubectl -n hostsampling get pods -o wide || true
  kubectl -n hostsampling logs -l app.kubernetes.io/name=host-sampling-controller --tail=80 || true
}

# wait_until "<desc>" "<shell condition>" — retries the condition for ~2 min.
wait_until() {
  local desc="$1" cond="$2"
  for _ in $(seq 1 60); do
    if eval "$cond"; then echo "OK: $desc"; return 0; fi
    sleep 2
  done
  echo "TIMEOUT: $desc"
  dump_debug
  exit 1
}

# ── Identify the three worker nodes. The control-plane carries the
# node-role.kubernetes.io/control-plane label with an EMPTY value, so we select
# by ABSENCE of that key (a value check can't tell empty-value from absent).
mapfile -t WORKERS < <(
  kubectl get nodes -l '!node-role.kubernetes.io/control-plane' -o name \
    | sed 's|^node/||' | sort
)
if [ "${#WORKERS[@]}" -lt 3 ]; then
  echo "expected >=3 worker nodes, got ${#WORKERS[@]}: ${WORKERS[*]:-none}"
  kubectl get nodes
  exit 1
fi
STABLE="${WORKERS[0]}"
SURGE_A="${WORKERS[1]}"
SURGE_B="${WORKERS[2]}"
log "pools: stable=$STABLE surge=$SURGE_A,$SURGE_B"

kubectl label node "$STABLE" pool=stable --overwrite
kubectl label node "$SURGE_A" pool=surge --overwrite
kubectl label node "$SURGE_B" pool=surge --overwrite

# ── Phase 1: enforcement contract (manual label → eviction → restore) ─────────
log "Phase 1: deploy stub agent DaemonSet"
kubectl apply -f "$HERE/manifests/agent-daemonset.yaml"
kubectl -n datadog rollout status ds/datadog-agent --timeout=120s
wait_until "agent scheduled on stable node $STABLE" "[ \"\$(agent_pods_on $STABLE)\" = 1 ]"

log "Phase 1: labelling stable node sampled-out=true must evict its agent"
kubectl label node "$STABLE" "$LABEL=true" --overwrite
wait_until "agent evicted from $STABLE" "[ \"\$(agent_pods_on $STABLE)\" = 0 ]"

log "Phase 1: removing the label must restore the agent"
kubectl label node "$STABLE" "${LABEL}-"
wait_until "agent restored on $STABLE" "[ \"\$(agent_pods_on $STABLE)\" = 1 ]"

# ── Phase 2: real controller actuates in execute mode ─────────────────────────
log "Phase 2: build + load the controller image"
docker build -t host-sampling-controller:e2e "$ROOT"
kind load docker-image host-sampling-controller:e2e --name "$KIND_CLUSTER"

log "Phase 2: deploy the stub policy endpoint (mode=active)"
kubectl apply -f "$HERE/manifests/stub-policy.yaml"
kubectl rollout status deploy/stub-policy --timeout=120s

log "Phase 2: install the controller (execute, 5s poll) via the Helm chart"
kubectl create namespace hostsampling --dry-run=client -o yaml | kubectl apply -f -
kubectl -n hostsampling create secret generic hs-token \
  --from-literal=token=e2e-dummy --dry-run=client -o yaml | kubectl apply -f -
helm install hs "$ROOT/chart" -n hostsampling \
  --set sunshine.endpoint=http://stub-policy.default.svc.cluster.local \
  --set sunshine.clusterId=e2e \
  --set sunshine.tokenSecretName=hs-token \
  --set dryRun=false \
  --set pollIntervalSeconds=5 \
  --set agent.daemonsetNamespace=datadog \
  --set agent.daemonsetName=datadog-agent \
  --set image.repository=host-sampling-controller \
  --set image.tag=e2e \
  --set image.pullPolicy=IfNotPresent
kubectl -n hostsampling wait --for=condition=available deploy --all --timeout=120s

log "Phase 2: controller must sample out exactly one surge node, never the stable one"
# Concatenating the two surge labels: exactly one "true" → "true"; none → ""; both → "truetrue".
wait_until "one surge node sampled out" \
  "[ \"\$(node_label $SURGE_A)\$(node_label $SURGE_B)\" = true ]"
if [ -n "$(node_label "$STABLE")" ]; then
  echo "FAIL: stable node $STABLE was labelled sampled-out"
  dump_debug
  exit 1
fi
echo "OK: stable node untouched"

# ── Phase 3: full chain — the controller-sampled node loses its agent ─────────
SAMPLED="$SURGE_A"; MONITORED="$SURGE_B"
if [ "$(node_label "$SURGE_B")" = "true" ]; then SAMPLED="$SURGE_B"; MONITORED="$SURGE_A"; fi
log "Phase 3: agent must be evicted from the controller-sampled node $SAMPLED"
wait_until "agent evicted from $SAMPLED" "[ \"\$(agent_pods_on $SAMPLED)\" = 0 ]"

if [ "$(agent_pods_on "$MONITORED")" != "1" ] || [ "$(agent_pods_on "$STABLE")" != "1" ]; then
  echo "FAIL: monitored surge ($MONITORED) or stable ($STABLE) lost its agent"
  dump_debug
  exit 1
fi

log "e2e PASSED: enforcement contract + controller actuation + full chain verified"
