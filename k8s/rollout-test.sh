#!/usr/bin/env bash
#
# Proves the rolling update loses no requests, by measuring it rather
# than by inspecting the manifests.
#
# The load runs *inside* the cluster, against the Service. That is not a
# convenience: `kubectl port-forward` attaches to one specific pod, so it
# would die exactly when that pod is replaced — the moment this test
# exists to observe — and the resulting errors would be the tunnel's, not
# the API's. Hitting the Service is also what actually exercises the
# endpoint churn a rollout causes.
#
# The load is authenticated and includes an attachment download, so the
# run also proves the two things the storage work was for: a session
# issued by the old pod still works against the new one (shared
# PostgreSQL), and a file uploaded through the old pod is readable
# through the new one (shared object storage).
#
# Usage:
#   ./k8s/rollout-test.sh            # create the cluster if needed, then test
#   ./k8s/rollout-test.sh --keep     # leave the cluster running afterwards
#
# If a SigNoz OTLP ingester container is running (see docs/DECISIONS.md's
# Fase 11 sections — Docker Compose via Foundry, on this same machine),
# this also reconciles what crier reported losing at shutdown against
# what actually landed in SigNoz's ClickHouse — issue 11.7. Without one
# running, that section is skipped with a clear message; the HTTP-level
# rollout test below is unaffected either way, since crier is opt-in and
# CRIER_OTLP_ENDPOINT only takes effect when something is listening on it.

set -euo pipefail

CLUSTER=${CLUSTER:-task-api}
NS=task-api
IMAGE=task-api:dev
LOAD_SECONDS=${LOAD_SECONDS:-45}
# Matches k8s/30-config.yaml's CRIER_OTLP_ENDPOINT and the container name
# SigNoz's Foundry-based Compose deploy produces with the default project
# name ("signoz"). Override if a different project/cluster name is in use.
SIGNOZ_INGESTER=${SIGNOZ_INGESTER:-signoz-ingester-1}
SIGNOZ_CLICKHOUSE=${SIGNOZ_CLICKHOUSE:-signoz-telemetrystore-clickhouse-0-0}
KEEP=false
[[ "${1:-}" == "--keep" ]] && KEEP=true

cd "$(dirname "$0")/.."

log() { printf '\n\033[1m== %s\033[0m\n' "$*"; }

for tool in kind kubectl docker; do
  command -v "$tool" >/dev/null || { echo "$tool não encontrado."; exit 1; }
done

log "Cluster"
if kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
  echo "reutilizando o cluster kind '$CLUSTER'"
else
  kind create cluster --name "$CLUSTER"
fi
kubectl config use-context "kind-$CLUSTER" >/dev/null

log "SigNoz (opcional — issue 11.7)"
SIGNOZ_UP=false
if docker inspect "$SIGNOZ_INGESTER" >/dev/null 2>&1 && docker inspect "$SIGNOZ_CLICKHOUSE" >/dev/null 2>&1; then
  # Idempotent: connecting a container already on the network is a no-op
  # error this script does not treat as fatal. The "kind" network
  # survives `kind delete cluster`, so this is normally a no-op after
  # the first run on a machine — see docs/DECISIONS.md.
  docker network connect kind "$SIGNOZ_INGESTER" >/dev/null 2>&1 || true
  SIGNOZ_UP=true
  echo "SigNoz encontrado ($SIGNOZ_INGESTER) — reconciliação de drain habilitada"
else
  echo "SigNoz não encontrado ($SIGNOZ_INGESTER) — pulando a reconciliação da issue 11.7"
  echo "(veja docs/DECISIONS.md's Fase 11 sections para subir um via Foundry)"
fi

log "Imagem"
docker build -q -t "$IMAGE" . >/dev/null
kind load docker-image "$IMAGE" --name "$CLUSTER"

log "Manifests"
kubectl apply -f k8s/ >/dev/null
kubectl -n "$NS" rollout status deploy/postgres --timeout=180s
kubectl -n "$NS" rollout status deploy/minio --timeout=180s
kubectl -n "$NS" wait --for=condition=complete job/minio-bucket --timeout=180s
kubectl -n "$NS" rollout status deploy/task-api --timeout=180s

# The load pod does its own setup so the token and storage key never
# leave the cluster, and so the whole sequence is one script whose output
# can be counted afterwards.
log "Carga (${LOAD_SECONDS}s) — inicia e roda em background"
kubectl -n "$NS" delete pod load --ignore-not-found >/dev/null 2>&1 || true
kubectl -n "$NS" run load --image=curlimages/curl:8.11.1 --restart=Never --command -- \
  /bin/sh -c '
    set -e
    API=http://task-api:8080/v1
    CRED='"'"'{"email":"rollout@example.com","password":"password12345"}'"'"'

    curl -s -o /dev/null -X POST "$API/auth/register" -H "Content-Type: application/json" -d "$CRED"
    TOKEN=$(curl -s -X POST "$API/auth/login" -H "Content-Type: application/json" -d "$CRED" \
      | sed -n "s/.*\"token\":\"\([^\"]*\)\".*/\1/p")
    [ -n "$TOKEN" ] || { echo "SETUP_FAILED login"; exit 1; }

    TASK=$(curl -s -X POST "$API/tasks" -H "Authorization: Bearer $TOKEN" \
      -H "Content-Type: application/json" -d "{\"title\":\"rollout\"}" \
      | sed -n "s/.*\"id\":\"\([^\"]*\)\".*/\1/p")
    [ -n "$TASK" ] || { echo "SETUP_FAILED create task"; exit 1; }

    printf "%%PDF-1.7\nuploaded before the rollout\n" > /tmp/f.pdf
    KEY=$(curl -s -X POST "$API/tasks/$TASK/attachments" -H "Authorization: Bearer $TOKEN" \
      -F "file=@/tmp/f.pdf" | sed -n "s/.*\"storage_key\":\"\([^\"]*\)\".*/\1/p")
    [ -n "$KEY" ] || { echo "SETUP_FAILED upload"; exit 1; }

    echo "SETUP_OK task=$TASK key=$KEY"

    END=$(( $(date +%s) + '"$LOAD_SECONDS"' ))
    while [ "$(date +%s)" -lt "$END" ]; do
      # Authenticated list: proves the session survives the pod swap.
      echo "LIST $(curl -s -o /dev/null -w "%{http_code}" --max-time 5 \
        "$API/tasks" -H "Authorization: Bearer $TOKEN")"
      # Attachment download: proves the bytes are reachable from whatever
      # pod answers, which is the whole reason for object storage.
      echo "FILE $(curl -s -o /dev/null -w "%{http_code}" --max-time 5 \
        "$API/files/$KEY" -H "Authorization: Bearer $TOKEN")"
      sleep 0.1
    done
    echo "LOAD_DONE"
  ' >/dev/null

# Wait for setup to finish before disturbing anything, so the rollout
# lands during the request loop rather than during registration.
for _ in $(seq 1 60); do
  kubectl -n "$NS" logs load 2>/dev/null | grep -q "SETUP_OK" && break
  sleep 1
done
kubectl -n "$NS" logs load 2>/dev/null | grep "SETUP_OK" || { echo "setup da carga falhou"; kubectl -n "$NS" logs load; exit 1; }

# Captured before the restart so the *old* pod's own logs — including
# the "crier drain completed/incomplete" line it emits while
# terminating — are on disk before that pod object is gone and
# `kubectl logs` against its name stops working. ClickHouse's query
# below is windowed from this same instant, so both readings describe
# the same interval.
OLD_POD=""
DRAIN_LOG=""
CH_WINDOW_START=""
if [ "$SIGNOZ_UP" = true ]; then
  OLD_POD=$(kubectl -n "$NS" get pods -l app=task-api -o jsonpath='{.items[0].metadata.name}')
  DRAIN_LOG=$(mktemp)
  CH_WINDOW_START=$(date -u +%Y-%m-%dT%H:%M:%S)
  kubectl -n "$NS" logs -f "$OLD_POD" >"$DRAIN_LOG" 2>&1 &
  DRAIN_LOG_PID=$!
fi

log "Rollout restart, com a carga em voo"
kubectl -n "$NS" rollout restart deploy/task-api
kubectl -n "$NS" rollout status deploy/task-api --timeout=180s

log "Aguardando a carga terminar"
for _ in $(seq 1 $((LOAD_SECONDS + 60))); do
  kubectl -n "$NS" logs load 2>/dev/null | grep -q "LOAD_DONE" && break
  sleep 1
done

log "Resultado"
OUT=$(kubectl -n "$NS" logs load)
# grep -c exits non-zero when it matches nothing, which under `set -e`
# would abort the run precisely when the result is "no failures" — the
# outcome this test hopes for. Hence `|| true` on every count.
TOTAL=$(echo "$OUT" | grep -cE '^(LIST|FILE) [0-9]+$' || true)
OK=$(echo "$OUT" | grep -cE '^(LIST|FILE) 200$' || true)
BAD=$(echo "$OUT" | grep -E '^(LIST|FILE) [0-9]+$' | grep -vE ' 200$' || true)
BADCOUNT=$(printf '%s' "$BAD" | grep -cE '^(LIST|FILE) ' || true)

echo "requisições:      $TOTAL"
echo "200:              $OK"
echo "não-200/timeout:  $BADCOUNT"
if [ -n "$BAD" ]; then
  echo "--- respostas que não foram 200 ---"
  echo "$BAD" | sort | uniq -c
fi

if [ "$SIGNOZ_UP" = true ]; then
  log "Drain do crier vs. SigNoz (issue 11.7)"

  # The old pod is gone by now (the rollout above waited for it), so its
  # `kubectl logs -f` background process has already exited on its own;
  # this just reaps it and makes sure the file is flushed.
  wait "$DRAIN_LOG_PID" 2>/dev/null || true

  # extract_lost prints the "lost" count from a captured pod log's own
  # "crier drain" line, or the literal string "MISSING" if the pod never
  # logged one (endpoint misconfigured, or it never got a chance to shut
  # down gracefully). Kept as a function because this run needs it
  # twice: once for the pod the rollout replaced, once for the pod the
  # load was still hitting when it finished (see below) — an early
  # version of this script only checked the first, which left the last
  # few seconds of every run's records permanently unaccounted for.
  extract_lost() {
    local log_file="$1" pod_name="$2"
    if grep -q "crier drain completed" "$log_file"; then
      echo "0"
    elif grep -q "crier drain incomplete" "$log_file"; then
      # logDrainSummary (cmd/api/crier.go) logs "lost" as its own
      # structured attribute — pulled from the JSON rather than parsed
      # out of the human-readable summary string, so a wording change
      # there cannot silently break this extraction.
      grep "crier drain incomplete" "$log_file" | tail -1 | sed -n 's/.*"lost":\([0-9]*\).*/\1/p'
    else
      echo "AVISO: nenhuma linha \"crier drain\" encontrada nos logs de $pod_name" >&2
      echo "       — CRIER_OTLP_ENDPOINT pode não ter sido lido, ou o pod não chegou a desligar a tempo" >&2
      echo "MISSING"
    fi
  }

  OLD_LOST=$(extract_lost "$DRAIN_LOG" "$OLD_POD (substituído durante o rollout)")
  if [ "$OLD_LOST" = "MISSING" ]; then
    CRIER_LOST=""
  else
    echo "crier ($OLD_POD, durante o rollout) reportou: $OLD_LOST registro(s) perdido(s)"
    CRIER_LOST=$OLD_LOST
  fi
  rm -f "$DRAIN_LOG"

  LIST_SENT=$(echo "$OUT" | grep -cE '^LIST [0-9]+$' || true)

  # The pod the rollout produced keeps serving the load for whatever
  # remains of LOAD_SECONDS after it — its crier buffer is still open
  # when the load finishes, so its last few seconds of records may still
  # be sitting in memory rather than exported. An arbitrary sleep before
  # querying ClickHouse "fixed" most of this but not reliably: measured
  # directly across three separate runs, waiting 8s and then 20s still
  # left the count 1–7 records short, not zero — because the pod was
  # never actually asked to drain; it was just given time and hoped to
  # have done so on its own periodic export cycle. Forcing a real
  # graceful shutdown here — the same `closeAll` path a rollout or a
  # `kubectl delete pod` triggers — is what makes this deterministic
  # instead of a guessed number of seconds: a genuine `Shutdown()` closes
  # the buffer immediately, which per its own doc comment "wakes...
  # consumers so they drain instead of waiting out a window".
  NEW_POD=$(kubectl -n "$NS" get pods -l app=task-api -o jsonpath='{.items[0].metadata.name}')
  NEW_DRAIN_LOG=$(mktemp)
  kubectl -n "$NS" logs -f "$NEW_POD" >"$NEW_DRAIN_LOG" 2>&1 &
  NEW_DRAIN_LOG_PID=$!
  kubectl -n "$NS" delete pod "$NEW_POD" --wait=true --timeout=40s >/dev/null
  wait "$NEW_DRAIN_LOG_PID" 2>/dev/null || true

  NEW_LOST=$(extract_lost "$NEW_DRAIN_LOG" "$NEW_POD (fim do teste)")
  if [ "$NEW_LOST" = "MISSING" ]; then
    CRIER_LOST=""
  elif [ -n "$CRIER_LOST" ]; then
    echo "crier ($NEW_POD, fim do teste) reportou: $NEW_LOST registro(s) perdido(s)"
    CRIER_LOST=$((CRIER_LOST + NEW_LOST))
  fi
  rm -f "$NEW_DRAIN_LOG"

  if [ -n "$CRIER_LOST" ]; then
    echo "crier reportou no total: $CRIER_LOST registro(s) perdido(s), somando os dois pods deste teste"
  fi

  # A graceful crier.Shutdown() on both pods (above) makes crier's own
  # side of the accounting deterministic — and, measured directly, both
  # pods reported a clean drain (0 lost) while ClickHouse still fell
  # short of the sent count immediately afterwards. That gap is not
  # task-api's: Export() returning success only means SigNoz's OTLP
  # receiver *accepted* the batch (see crier/exporters/otlp's own doc
  # comment on Export), not that it has already reached ClickHouse —
  # SigNoz's own ingestion pipeline batches internally before the
  # insert, on a cycle crier has no visibility into or control over.
  # This wait is for *that* latency, downstream of everything this
  # codebase owns.
  sleep 15

  # signoz_logs.logs_v2 is what ClickHouse's own SHOW TABLES names the
  # ingested table (verified by inspection when this was written — not
  # an API SigNoz documents, so re-check if this ever needs updating).
  # `timestamp` is a raw UInt64 nanosecond epoch, not a native
  # DateTime64 — comparing it against toDateTime64(...) directly
  # overflows ClickHouse's decimal comparison (DECIMAL_OVERFLOW),
  # discovered by running this query, not by reading the schema's
  # column-type name. toUnixTimestamp(...) * 1e9 matches the column's
  # actual representation. Windowed from CH_WINDOW_START (captured right
  # before the restart) to now, per method+path, so it counts only LIST
  # requests from this run — not readiness-probe traffic or previous runs.
  SIGNOZ_RECEIVED=$(docker exec "$SIGNOZ_CLICKHOUSE" clickhouse-client --query "
    SELECT count() FROM signoz_logs.logs_v2
    WHERE resources_string['service.name'] = 'task-api'
      AND attributes_string['method'] = 'GET'
      AND attributes_string['path'] = '/v1/tasks'
      AND timestamp >= toUnixTimestamp('$CH_WINDOW_START') * 1000000000
  " 2>/dev/null || echo "")

  echo "LIST enviadas pela carga:        $LIST_SENT"
  echo "registros recebidos no SigNoz:   ${SIGNOZ_RECEIVED:-<consulta falhou>}"

  if [ -n "$CRIER_LOST" ] && [ -n "$SIGNOZ_RECEIVED" ]; then
    EXPECTED_MIN=$((LIST_SENT - CRIER_LOST))
    if [ "$SIGNOZ_RECEIVED" -lt "$EXPECTED_MIN" ]; then
      echo "DIVERGÊNCIA: SigNoz recebeu menos do que (enviadas - perda reportada pelo crier = $EXPECTED_MIN)"
      echo "             raiz não identificada automaticamente — investigar antes de fechar a issue 11.7"
      SIGNOZ_RECONCILED=false
    else
      echo "OK: SigNoz recebeu pelo menos (enviadas - perda reportada) — consistente com at-least-once"
      SIGNOZ_RECONCILED=true
    fi
  else
    echo "reconciliação incompleta — ver avisos acima"
    SIGNOZ_RECONCILED=false
  fi

  rm -f "$DRAIN_LOG"
fi

if [ "$KEEP" = false ]; then
  log "Removendo o cluster"
  kind delete cluster --name "$CLUSTER" >/dev/null
else
  echo
  echo "cluster mantido: kubectl --context kind-$CLUSTER -n $NS get pods"
fi

[ "$BADCOUNT" -eq 0 ] || { echo; echo "FALHOU: $BADCOUNT requisições não retornaram 200 durante o rollout"; exit 1; }

if [ "$SIGNOZ_UP" = true ] && [ "${SIGNOZ_RECONCILED:-false}" != true ]; then
  echo
  echo "FALHOU: reconciliação do drain do crier com o SigNoz não fechou — ver \"Fase 11.7\" acima"
  exit 1
fi

echo
echo "OK: nenhuma requisição perdida durante o rollout."
[ "$SIGNOZ_UP" = true ] && echo "OK: drain do crier reconciliado com o SigNoz (issue 11.7)."
