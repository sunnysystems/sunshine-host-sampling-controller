# Implantação do Host-Sampling Controller no cliente

> Runbook de ponta a ponta para instalar o **Sunshine host-sampling controller**
> no cluster Kubernetes de um cliente — do piloto seguro em **dry-run**
> até o **go-live em execute**.
>
> Público-alvo: time de **plataforma / SRE** do cliente (quem opera o cluster) +
> engenharia da Sunny que acompanha o rollout.
>
> Documentos de referência: [`../README.md`](../README.md) (visão do controller) e
> [`../chart/README.md`](../chart/README.md) (referência do Helm chart). Este guia
> é o **passo a passo operacional**; os READMEs são a referência.

---

## 1. O que é e por que existe

O Datadog fatura hosts de infra/APM pelo **p99 da contagem horária de hosts** — ou
seja, os **picos recorrentes** que definem a conta. Em Kubernetes o agente Datadog
roda como **DaemonSet**, então **todo nó de surge vira um host faturável**, mesmo
quando os nós de surge são clones homogêneos e descartáveis (spot/burst).

O controller resolve isso mantendo:

- a **frota fixa 100% monitorada** (nunca amostrada), e
- **apenas uma amostra** do pool de surge monitorada, conforme a política.

Ele **roda dentro do cluster do cliente**, consulta a política de sampling no
Sunshine e reconcilia o label de nó `datadog.sunshine/sampled-out` em direção ao
plano. **O padrão é dry-run** (só relata, nunca mexe no cluster). Ele só escreve
labels quando os **três locks** da seção 3 estão satisfeitos.

> ⚠️ Este é o primeiro artefato da Sunny que roda no ambiente de um cliente.
> Distribuição, assinatura e versionamento da imagem ainda estão em evolução.
> Trate como early-adopter e alinhe a via de distribuição com a Sunny.

---

## 2. Como funciona (um ciclo de reconcile)

A cada `POLL_INTERVAL_SECONDS` (default 60s) o controller executa um **tick**. Um
tick nunca derruba o processo — um tick ruim é logado e o próximo se recupera.

1. **Poll da política** — `GET {SUNSHINE_ENDPOINT}/api/autopilot/policy/host-sampling`
   com `Authorization: Bearer <token>`, usando `ETag`/`If-None-Match` (polling
   barato: `304 Not Modified` mantém a política em cache).
2. **Classifica os nós** em dois pools, pelos selectors `key=value` da política:
   - **stable** (frota fixa) → **nunca** amostrado;
   - **surge** → sujeito a sampling.
   - Nós que não casam com nenhum selector ficam **monitorados** (untracked).
   - Se um nó casa com os dois, **surge tem precedência**.
3. **Planeja** quantos nós de surge manter monitorados:
   ```
   budget = max(floorNodes, ceil(surgeTotal × surgeSamplePct / 100))
   budget = min(budget, surgeTotal)      // nunca passa do total
   ```
   Mantém os **`budget` nós mais ANTIGOS** monitorados (membership estável, sem
   flapping); os **mais novos** — os efêmeros de spot/burst — são os candidatos a
   `sampled-out`.
   `surgeSamplePct = 100` → budget = total → **nada** é amostrado.
4. **Atua** (só quando autorizado — ver locks): adiciona o label aos nós
   recém-amostrados e **remove** o label de nós que voltaram ao orçamento
   monitorado (limpeza de órfãos). Em dry-run apenas **relata** via logs +
   métricas em texto plano, e não escreve nada.
5. **Reporta** ao Sunshine (best-effort) o resumo do tick. Uma falha no report é
   logada e descartada — **nunca** bloqueia nem altera o reconcile.

### Fail-open é a propriedade central de segurança

- Endpoint inacessível, erro, `401/404/5xx`, ou política `configured:false` →
  **plano vazio** → **nada é amostrado** → **tudo continua monitorado**.
- A polaridade do label também é fail-open: um nó **sem** o label é monitorado.
  Logo, "não fazer nada" preserva a cobertura total. O controller **nunca** é
  ponto único de falha para o monitoramento do cliente.

---

## 3. Os três locks (modelo de segurança)

O controller escreve o label num nó **somente quando os TRÊS valem**. Qualquer um
deles no default mantém o cluster 100% monitorado.

| # | Lock | Onde | Como habilitar | Efeito |
|---|------|------|----------------|--------|
| 1 | **Local** | cluster do cliente (Helm) | `dryRun: false` (`DRY_RUN=false`) | Seleciona o `LabelActuator` (escreve labels) **e** amplia o RBAC pra permitir `patch/update` em nodes. |
| 2 | **Server** | Sunshine | flag `datadogCostGuardHostSamplingExecute` **on** na org + **não** ser org demo → servidor serve `mode: "active"` | Sem isso o servidor rebaixa a política pra `dry_run`. É o **kill-switch central** da Sunny. |
| 3 | **Cluster** | DaemonSet do agente Datadog | `nodeAffinity` invertida no label (seção 4) | Sem ela o label **não tem efeito**: o agente continua agendando nos nós amostrados → você paga sem monitorar. |

**Pausar é tão seguro quanto ligar:** desligue a flag (ou volte o cluster pra
`mode: dry_run`, ou `dryRun=true` local) e o próximo tick **remove** todos os
labels `sampled-out`, restaurando o monitoramento total.

---

## 4. Contrato de enforcement (a `nodeAffinity`)

Escrever o label só remove o agente de um nó se o **DaemonSet do agente Datadog**
tiver uma `nodeAffinity` invertida nesse label. Adicione ao pod spec do agente:

```yaml
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
        - matchExpressions:
            # Um nó SEM o label, ou com valor != "true", ainda agenda o agente
            # → monitorado. Fail-open por construção.
            - key: datadog.sunshine/sampled-out
              operator: NotIn
              values: ["true"]
```

- **Fail-open por polaridade:** o agente só é mantido *fora* de nós com
  `sampled-out=true`. Qualquer outro estado (sem label, valor diferente) mantém o
  agente → monitorado.
- O controller roda um **preflight read-only** no startup: com
  `agent.daemonsetNamespace` / `agent.daemonsetName` setados no Helm, ele lê o
  DaemonSet e confirma a affinity, emitindo a métrica
  `sunshine_host_sampling_enforcement_affinity_present` (`1` presente / `0`
  ausente) e logando um aviso se estiver faltando.

> O preflight aceita tanto `operator: NotIn` com `values: ["true"]` quanto
> `operator: DoesNotExist` no mesmo `key`.

---

## 5. Pré-requisitos (checklist antes de começar)

- [ ] **Cluster Kubernetes** com o **agente Datadog rodando como DaemonSet**.
      Anote o **namespace** e o **nome** do DaemonSet (ex.: `datadog` /
      `datadog-agent`).
- [ ] Acesso ao cluster com `kubectl` + `helm`, com permissão de criar
      **ClusterRole/ClusterRoleBinding** e um **ServiceAccount** (o controller usa
      RBAC cluster-scoped porque lista/patcheia nós).
- [ ] **Nós rotulados** de forma a distinguir a frota fixa do pool de surge por um
      selector `key=value` simples (ex.: `sunshine.io/pool=stable` e
      `sunshine.io/pool=surge`, ou reuso de labels de node pool já existentes como
      `cloud.google.com/gke-nodepool=...`, `eks.amazonaws.com/nodegroup=...`).
      > ⚠️ O matcher só entende **`key=value` exato** — nada de expressões
      > compostas, `In`, faixas, etc. Se os pools ainda não têm um label
      > determinístico, **crie-o nos node pools antes** de configurar a política.
- [ ] **Acesso à imagem** `ghcr.io/sunnysystems/host-sampling-controller`
      (o cliente precisa conseguir dar `pull`; se o registry for privado,
      configure `imagePullSecrets` ou espelhe a imagem num registry interno).
      *(Distribuição da imagem é tech-debt — confirme com a Sunny a via de
      distribuição para este cliente.)*
- [ ] **Token de entrada** emitido no Sunshine (**Autopilot → Component tokens**),
      **escopado por (org, cluster)** e **read-only**. É o mesmo token usado
      para consultar a política e para reportar.
- [ ] **Endpoint do Sunshine** (base URL) e o **cluster id** — o `cluster id`
      **precisa bater** com o escopo do token.
- [ ] **Egress HTTPS** do cluster para o endpoint do Sunshine liberado.

---

## 6. Fase 1 — Piloto em dry-run

Objetivo: instalar sem risco, observar o plano e validar a classificação de pools.
Nenhum nó é tocado.

### 6.1 Criar o Secret com o token

```sh
kubectl create secret generic host-sampling-token \
  --from-literal=token=<TOKEN_DO_SUNSHINE>
```

### 6.2 Instalar o chart (dry-run é o default)

```sh
helm install host-sampling ./chart \
  --set sunshine.endpoint=https://app.sunshine.example.com \
  --set sunshine.clusterId=prod-us-east-1 \
  --set sunshine.tokenSecretName=host-sampling-token \
  --set agent.daemonsetNamespace=datadog \
  --set agent.daemonsetName=datadog-agent
```

- `dryRun` já é `true` por default → RBAC **read-only** (`get/list/watch` em
  `nodes`, `get/list` em `daemonsets`).
- Setar `agent.daemonsetNamespace/Name` já aqui ativa o **preflight de affinity**,
  então você descobre cedo se o enforcement está pronto.

### 6.3 Conferir que subiu

```sh
kubectl get pods -l app.kubernetes.io/name=sunshine-host-sampling-controller
kubectl logs deploy/host-sampling
```

No log de startup (JSON) você deve ver `host-sampling-controller started` com
`dryRun=true`, o endpoint e o cluster. Health em `/healthz` (liveness +
readiness).

### 6.4 Configurar a política no Sunshine

No lado Sunshine, configure a política de host-sampling do cluster (deixe em
**`mode: dry_run`** por enquanto):

- `stablePoolSelector` — ex.: `sunshine.io/pool=stable`
- `surgePoolSelector` — ex.: `sunshine.io/pool=surge`
- `surgeSamplePct` — % do surge a manter monitorada (ex.: `20`)
- `floorNodes` — piso mínimo de nós de surge monitorados (ex.: `2`)

### 6.5 Ler as métricas e validar o plano

```sh
kubectl port-forward deploy/host-sampling 9090:9090
curl -s localhost:9090/metrics | grep sunshine_host_sampling
```

Interprete:

| Métrica | Esperado no piloto |
|---------|--------------------|
| `sunshine_host_sampling_policy_configured` | `1` (se `0`, a política não chegou — ver Troubleshooting) |
| `sunshine_host_sampling_stable_nodes` | = nº real de nós da frota fixa |
| `sunshine_host_sampling_surge_nodes` | = nº real de nós de surge |
| `sunshine_host_sampling_monitored_nodes` | = `budget` calculado |
| `sunshine_host_sampling_would_sample_out_nodes` | surge que **seria** amostrado (só relatado em dry-run) |
| `sunshine_host_sampling_enforcement_affinity_present` | idealmente `1` (ver Fase 2) |

Deixe rodar por **alguns ciclos de pico reais** para confirmar que a classificação
de pools e o baseline por pool ficam estáveis (sem flapping) e que os nós que
apareceriam em `would_sample_out` são realmente os descartáveis.

---

## 7. Fase 2 — Validação antes do go-live

Só avance para execute com **todos** os itens abaixo verdes:

- [ ] **Enforcement pronto:** `sunshine_host_sampling_enforcement_affinity_present == 1`.
      Se `0`, adicione a `nodeAffinity` da seção 4 ao DaemonSet do agente Datadog e
      confirme que o preflight passa a reportar `1`. **Sem isso, amostrar um nó não
      remove o agente → nenhuma economia (savings fantasma).**
- [ ] **Alvo correto:** os nós em `would_sample_out` são de fato surge/spot
      descartáveis, e **nenhum** nó da frota fixa aparece ali.
- [ ] **Gate 1 — acurácia do forecast:** a economia projetada bate com a
      fatura/snapshot de custo (o pipeline de reconciliação de custo já validado
      alimenta este gate).
- [ ] **Gate 2 — validação viva:** a mutação foi validada numa conta Datadog real
      (o label realmente drena o agente e a contagem de hosts cai).
- [ ] **Escopo do token** correto (org, cluster) e a org **não é demo**.

---

## 8. Fase 3 — Go-live em execute (ligar os três locks)

Ligue os locks **nesta ordem** — assim, mesmo se parar no meio, o cluster segue
seguro:

1. **Cluster lock (já feito na Fase 2):** garanta a `nodeAffinity` no DaemonSet do
   agente (`enforcement_affinity_present == 1`).
2. **Server lock (lado Sunshine):** ligue a flag
   `datadogCostGuardHostSamplingExecute` na org (não-demo) e coloque a política em
   **`mode: active`**.
3. **Local lock (cluster do cliente):** habilite o `LabelActuator` e amplie o RBAC:
   ```sh
   helm upgrade host-sampling ./chart --reuse-values --set dryRun=false
   ```

### O que observar após o go-live

- Logs passam a mostrar `host-sampling: reconciled labels` com `actuate=true`.
- Métricas: `sunshine_host_sampling_labels_applied_total` sobe; os nós
  `would_sample_out` viram `sampled-out=true` de fato.
- No Datadog: o agente é **drenado** desses nós e a **contagem de hosts cai**
  (o efeito faturável que buscamos).

```sh
# Nós efetivamente amostrados:
kubectl get nodes -l datadog.sunshine/sampled-out=true
```

---

## 9. Operação e observabilidade

### Métricas (`:9090/metrics`)

Endpoint HTTP que devolve **texto plano** com o estado do controller. Não exige
nenhuma ferramenta extra: lê-se direto com `curl` (ver 6.5) e, se o cliente
quiser ingerir, o **próprio agente Datadog** raspa esse endpoint via o check
**OpenMetrics** — sem coletor adicional no meio.

| Métrica | Tipo | Significado |
|---------|------|-------------|
| `sunshine_host_sampling_stable_nodes` | gauge | Nós na frota fixa (stable). |
| `sunshine_host_sampling_surge_nodes` | gauge | Nós no pool de surge. |
| `sunshine_host_sampling_monitored_nodes` | gauge | Nós de surge mantidos monitorados (budget). |
| `sunshine_host_sampling_would_sample_out_nodes` | gauge | Surge que o plano amostraria (nunca aplicado em dry-run). |
| `sunshine_host_sampling_policy_configured` | gauge | `1` = política configurada; `0` = fail-open. |
| `sunshine_host_sampling_enforcement_affinity_present` | gauge | `1` = DaemonSet tem a anti-affinity (só emitida se o preflight rodou). |
| `sunshine_host_sampling_reconcile_ticks_total` | counter | Total de ticks de reconcile. |
| `sunshine_host_sampling_policy_fetch_errors_total` | counter | Erros de fetch de política (cada um falha open). |
| `sunshine_host_sampling_labels_applied_total` | counter | Labels `sampled-out` escritos (execute). |
| `sunshine_host_sampling_labels_cleared_total` | counter | Labels removidos (limpeza de órfão / pausa). |
| `sunshine_host_sampling_label_errors_total` | counter | Falhas de patch por nó. |

### Logs

JSON estruturado em stdout. Linhas-chave:
- `host-sampling-controller started` — startup (mostra `dryRun`, endpoint, cluster).
- `dry-run: no cluster changes` — plano em dry-run.
- `host-sampling: reconciled labels` — atuação (mostra `actuate`, `applied`, `cleared`, `errors`).
- `policy fetch failed — failing open ...` — fetch falhou; monitorando tudo.
- `enforcement preflight: ...` — resultado do preflight de affinity.

### Health

`/healthz` responde `200 ok` — usado como liveness **e** readiness probe.

---

## 10. Pausar, rollback e kill-switch

Todas as formas abaixo são **seguras**: o próximo tick **remove** os labels
`sampled-out` e restaura o monitoramento total.

| Onde | Ação | Quando usar |
|------|------|-------------|
| **Server (Sunshine)** | Desligar `datadogCostGuardHostSamplingExecute` **ou** política → `mode: dry_run` | Kill-switch central da Sunny; pausa **sem** tocar no cluster. |
| **Local (Helm)** | `helm upgrade ... --set dryRun=true` | Pausa iniciada pelo cliente; volta ao actuator read-only. |
| **Automático** | Endpoint do Sunshine inacessível/erro | Fail-open: plano vazio, nada amostrado. |

Para **desinstalar** por completo:

```sh
helm uninstall host-sampling
# Se algum nó ficou com o label (ex.: uninstall no meio de execute), limpe:
kubectl label nodes --all datadog.sunshine/sampled-out-
```

---

## 11. Segurança e footprint

- **RBAC mínimo:** em dry-run, só `get/list/watch` em `nodes` e `get/list` em
  `daemonsets` (preflight). Os verbos `patch/update` em `nodes` só são concedidos
  quando `dryRun=false`.
- **Container endurecido:** imagem distroless, `runAsNonRoot`,
  `readOnlyRootFilesystem`, `allowPrivilegeEscalation: false`, `drop: ["ALL"]`,
  `seccompProfile: RuntimeDefault`.
- **Token** montado como Secret read-only em `/var/run/sunshine/token`
  (via `SUNSHINE_TOKEN_FILE`, preferido a variável de ambiente).
- **Footprint:** 1 réplica; requests `25m` CPU / `32Mi` mem, limits `100m` / `64Mi`.
- **Única via de escrita** ao cluster: `PATCH` de label de nó (strategic-merge),
  no `LabelActuator`.

---

## 12. Troubleshooting

| Sintoma | Causa provável | Ação |
|---------|----------------|------|
| `policy_configured = 0` | Política não configurada no Sunshine, ou `401/404/5xx`, ou token errado | Conferir token/endpoint/cluster id e a política no Sunshine. Fail-open enquanto isso: tudo monitorado. |
| `policy_fetch_errors_total` subindo | Egress/DNS/rede ou token inválido | Testar conectividade HTTPS do pod ao endpoint; revalidar o token. |
| `enforcement_affinity_present = 0` | DaemonSet do agente **sem** a `nodeAffinity` | Adicionar a affinity da seção 4 ao DaemonSet do agente. |
| Preflight não emite a métrica | `agent.daemonsetNamespace/Name` não setados | Setar ambos no Helm. |
| `dryRun=false` mas nenhum label aplicado | Server **não** serve `mode: active` (flag off ou org demo) | Ligar `datadogCostGuardHostSamplingExecute`, garantir não-demo, política `active`. |
| Nós com `sampled-out=true` mas host count **não** cai no Datadog | Falta o enforcement (nodeAffinity) → agente segue no nó | Adicionar a `nodeAffinity` (seção 4). |
| Plano oscila (nós entram/saem) | `surgeSamplePct`/`floorNodes` no limiar de um pool volátil | Ajustar `floorNodes`/`surgeSamplePct`; membership é oldest-first, mas surge muito volátil ainda oscila. |
| `label_errors_total > 0` | RBAC ou conflito no patch | Confirmar que `dryRun=false` concedeu `patch/update` em `nodes`; ver logs por nó. |

---

## 13. Referência de configuração

### Variáveis de ambiente (o chart preenche via `values`)

| Var | Obrigatória | Default | Significado |
|-----|-------------|---------|-------------|
| `SUNSHINE_ENDPOINT` | sim | — | Base URL do Sunshine |
| `CLUSTER_ID` | sim | — | id do cluster (deve bater com o escopo do token) |
| `SUNSHINE_TOKEN_FILE` / `SUNSHINE_TOKEN` | sim | — | token de entrada (file preferido — Secret montado) |
| `POLL_INTERVAL_SECONDS` | não | `60` | intervalo de reconcile |
| `DRY_RUN` | não | `true` | `false` seleciona o `LabelActuator` |
| `AGENT_DAEMONSET_NAMESPACE` | não | — | namespace do DaemonSet do agente (preflight) |
| `AGENT_DAEMONSET_NAME` | não | — | nome do DaemonSet do agente (preflight) |
| `METRICS_ADDR` | não | `:9090` | endereço de metrics/health |

### Values do Helm chart

| Key | Default | Notas |
|-----|---------|-------|
| `sunshine.endpoint` | `""` | **obrigatório** |
| `sunshine.clusterId` | `""` | **obrigatório** — bate com o escopo do token |
| `sunshine.tokenSecretName` | `""` | **obrigatório** — Secret com o token |
| `sunshine.tokenSecretKey` | `token` | chave dentro do Secret |
| `pollIntervalSeconds` | `60` | intervalo de reconcile |
| `dryRun` | `true` | Local lock #1 — deixar `true` até validar |
| `agent.daemonsetNamespace` | `""` | ativa o preflight de affinity |
| `agent.daemonsetName` | `""` | ativa o preflight de affinity |
| `metrics.port` | `9090` | `/metrics` + `/healthz` |
| `image.repository` | `ghcr.io/sunnysystems/host-sampling-controller` | |
| `image.tag` | `""` | default = `appVersion` do chart |

### Contrato da API de política (referência)

```
GET {endpoint}/api/autopilot/policy/host-sampling
Authorization: Bearer <token>
200 → {"configured":bool,
       "policy":{"mode","surgeSamplePct","stablePoolSelector",
                 "surgePoolSelector","floorNodes"},
       "version":string}          (+ header ETag)
304 → não modificado (mantém a política em cache)
401/404/5xx → tratado como não-configurado (FAIL OPEN)

POST {endpoint}/api/autopilot/report/host-sampling   (best-effort, mesma auth)
```

---

## 14. Limitações conhecidas / tech-debt

- **Distribuição/assinatura da imagem** ainda está em evolução. Alinhe a via de
  distribuição com a Sunny antes de instalar no cliente.
- **Selector de pool** só entende `key=value` **exato** — nada de expressões
  compostas. Garanta labels determinísticos nos node pools.
- **Primeiro artefato in-customer** — trate como early-adopter
  (suporte/versionamento em evolução).
- **Só reduz host count** (infra/APM host-based). Outras dimensões de custo (RUM,
  logs, APM ingestion, custom metrics) são cobertas por outros mecanismos do
  autopilot.
