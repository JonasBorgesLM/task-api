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

set -euo pipefail

CLUSTER=${CLUSTER:-task-api}
NS=task-api
IMAGE=task-api:dev
LOAD_SECONDS=${LOAD_SECONDS:-45}
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

if [ "$KEEP" = false ]; then
  log "Removendo o cluster"
  kind delete cluster --name "$CLUSTER" >/dev/null
else
  echo
  echo "cluster mantido: kubectl --context kind-$CLUSTER -n $NS get pods"
fi

[ "$BADCOUNT" -eq 0 ] || { echo; echo "FALHOU: $BADCOUNT requisições não retornaram 200 durante o rollout"; exit 1; }
echo
echo "OK: nenhuma requisição perdida durante o rollout."
