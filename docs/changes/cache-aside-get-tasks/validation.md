---
slug: cache-aside-get-tasks
stage: validation
status: clean
open_count: 0
issues:
  - id: DC-1
    status: resolved
    blocking: true
    summary: "Contrato operacional herdado do cairn (noeviction) era o oposto do que cistern exige"
    resolved_by: "/decide corrigiu docs/DECISIONS.md § Cache-aside (parágrafo de correção): allkeys-lru, instância dedicada"
  - id: DC-2
    status: resolved
    blocking: true
    summary: "Garantia 'correta independente de réplicas' não batia com o Bus best-effort real do cistern"
    resolved_by: "/decide reescreveu o Por quê: motivo real é pagar a infraestrutura uma vez, destravando rate limiting distribuído"
  - id: TG-1
    status: resolved
    blocking: false
    summary: "Faltava teste de invalidação cross-replica via Bus com Redis real"
    resolved_by: "context.md § Superfície de testes já inclui teste de integração explícito para isso"
  - id: TG-2
    status: resolved
    blocking: false
    summary: "Faltava teste garantindo que CountAll nunca é interceptado pelo cache"
    resolved_by: "context.md § Superfície de testes já nomeia esse teste explicitamente"
  - id: TG-3
    status: resolved
    blocking: false
    summary: "Faltava teste do fallback fail-open de leitura com Redis indisponível"
    resolved_by: "context.md § Superfície de testes já inclui teste de integração explícito para isso"
  - id: DD-1
    status: resolved
    blocking: false
    summary: "README.md: contagem de dependências e seções Requirements/Config/Testing/K8s"
    resolved_by: "totalmente especificado — variáveis REDIS_* (AM-1) e serviço k8s/25-redis.yaml (AM-2) dão o conteúdo exato; agenda como CI em /change-plan"
  - id: DD-2
    status: resolved
    blocking: false
    summary: ".env.example: variáveis Redis novas ausentes"
    resolved_by: "nomes decididos (AM-1: prefixo REDIS_*) — agenda como CI em /change-plan, junto com config.go/config_test.go/README (config-env.md)"
  - id: DD-3
    status: resolved
    blocking: false
    summary: "docs/RUNBOOK-BACKUP-RESTORE.md: seção de backup/restore de Redis"
    resolved_by: "escopo já totalmente especificado por docs/DECISIONS.md § Cache-aside (contrato allkeys-lru/ACL/TLS) — agenda como CI em /change-plan"
  - id: DD-4
    status: resolved
    blocking: false
    summary: "docker-compose.yml + k8s/*.yaml: serviço/manifesto Redis ausente"
    resolved_by: "numeração decidida (AM-2: k8s/25-redis.yaml) — agenda como CI em /change-plan"
  - id: DD-5
    status: resolved
    blocking: false
    summary: "CHANGELOG.md: entrada para o novo requisito de infraestrutura"
    resolved_by: "formato decidido (AM-5: entrada normal, sem destaque) — agenda como último CI em /change-plan"
  - id: AM-1
    status: resolved
    blocking: false
    summary: "Nomes das variáveis de ambiente Redis"
    resolved_by: "usuário escolheu prefixo REDIS_* (não CISTERN_*), consistente com o padrão de nomear o recurso, não a lib cliente"
  - id: AM-2
    status: resolved
    blocking: false
    summary: "Numeração do novo manifesto k8s"
    resolved_by: "usuário escolheu k8s/25-redis.yaml, entre 20-minio.yaml e 30-config.yaml"
  - id: AM-3
    status: resolved
    blocking: false
    summary: "Redis deve ser dedicado ao cistern, nunca compartilhado com cairn/rate limiter"
    resolved_by: "redisstore/README.md 'Operating it' — já incorporado como invariante em CLAUDE.md e na correção de D1"
  - id: AM-4
    status: resolved
    blocking: false
    summary: "cistern existe, publicado, pure Go (go-redis/v9)"
    resolved_by: "go list -m + gh api confirmaram v0.1.0/redisstore v0.1.0, cliente go-redis/v9"
  - id: AM-5
    status: resolved
    blocking: false
    summary: "Formato da entrada do CHANGELOG.md"
    resolved_by: "usuário escolheu entrada normal, sem destaque de mudança operacional"
  - id: AM-6
    status: resolved
    blocking: false
    summary: "Comportamento com Redis indisponível: leitura fail-open, invalidação logada, escrita nunca falha"
    resolved_by: "README 'Properties' + examples/taskapi/taskapi.go — já incorporado como invariante bloqueante em CLAUDE.md e context.md"
  - id: AM-7
    status: resolved
    blocking: false
    summary: "Estratégia de chave de cache para FindAll(limit,offset,statuses,priorities)"
    resolved_by: "docs/DECISIONS.md:1638 § Índices para status/priority em tasks exige chave composta (userID,statuses,priorities,limit,offset)"
  - id: AM-8
    status: resolved
    blocking: false
    summary: "Forma concreta do tipo de chave/tag do cistern.Cache[K,V] para o filtro composto"
    resolved_by: "usuário escolheu struct K (UserID, Statuses, Priorities, Limit, Offset) com função de normalização, em vez de string pré-serializada"
sources_mtime:
  docs/changes/cache-aside-get-tasks/context.md: 2026-09-24T13:12:17-03:00
  docs/DECISIONS.md: 2026-09-24T13:05:25-03:00
  CLAUDE.md: 2026-09-24T13:05:44-03:00
---

# cache-aside-get-tasks — Validação

## Veredito

**status: clean** — 0 questões abertas. As duas bloqueantes (DC-1, DC-2)
fecharam com a correção escrita via `/decide`. As três lacunas de teste
(TG-1/2/3) fecharam porque `context.md` já nomeia os testes explicitamente.
As quatro ambiguidades restantes (AM-1, AM-2, AM-5, AM-8) foram decididas
pelo usuário nesta rodada; com elas respondidas, os cinco itens de drift de
documentação (DD-1..5) deixaram de ser incertos — cada um agora tem conteúdo
exato o suficiente para virar um `CI` em `/change-plan` sem nenhuma decisão
técnica pendente.

## Questões abertas

_nenhuma_

## Checagens sem achados

- **Conflitos de decisão (`DC-N`)** — nenhum achado novo nesta rodada. A
  tarefa é exatamente o que a decisão corrigida (D1) prescreve.
- **Conflitos de invariante (`IV-N`)** — nenhum achado.
- **Cobertura de contrato (`CG-N`)** — nenhum achado. `docs/openapi.yaml`
  corretamente `n/a`.
- **Paridade de Repository (`PG-N`)** — nenhum achado. O decorator envolve a
  interface `Repository` inteira.
- **Item já diferido (`AD`)** — confirmado, advisório: as duas menções a
  Redis em Future Improvements continuam trabalho adjacente, não fechado por
  esta mudança. Não afeta o veredito.

## Questões resolvidas

### DC-1 — Contrato operacional herdado do cairn estava errado
- **Fechada por:** `/decide` acrescentou o parágrafo de correção a
  `docs/DECISIONS.md` § "Cache-aside para GET /v1/tasks": `maxmemory-policy
  allkeys-lru`, instância dedicada, nunca compartilhada com `cairn`/`moat`.

### DC-2 — Garantia de consistência superestimada
- **Fechada por:** `/decide` reescreveu o "Por quê" da mesma seção.

### TG-1 — Faltava teste de invalidação cross-replica via Bus
- **Fechada por:** `context.md` § "Superfície de testes" nomeia
  explicitamente um teste de integração para essa propriedade.

### TG-2 — Faltava teste de que `CountAll` nunca é cacheado
- **Fechada por:** `context.md` § "Superfície de testes" nomeia esse teste
  explicitamente.

### TG-3 — Faltava teste do fallback fail-open com Redis indisponível
- **Fechada por:** `context.md` § "Superfície de testes" nomeia
  explicitamente um teste de integração para esse fallback.

### DD-1 — README.md desatualizado
- **Fechada por:** conteúdo totalmente especificado (variáveis `REDIS_*`,
  serviço `k8s/25-redis.yaml`) — vira `CI` em `/change-plan`.

### DD-2 — `.env.example` sem as variáveis Redis
- **Fechada por:** nomes decididos (`REDIS_*`) — vira `CI` em
  `/change-plan`, junto com `config.go`/`config_test.go`/README.

### DD-3 — `docs/RUNBOOK-BACKUP-RESTORE.md` sem seção de Redis
- **Fechada por:** escopo já especificado pelo contrato operacional de D1 —
  vira `CI` em `/change-plan`.

### DD-4 — `docker-compose.yml` + `k8s/*.yaml` sem serviço Redis
- **Fechada por:** numeração decidida (`k8s/25-redis.yaml`) — vira `CI` em
  `/change-plan`.

### DD-5 — `CHANGELOG.md` sem entrada prevista
- **Fechada por:** formato decidido (entrada normal) — vira último `CI` em
  `/change-plan`.

### AM-1 — Nomes das variáveis de ambiente Redis
- **Fechada por:** usuário escolheu prefixo `REDIS_*`.

### AM-2 — Numeração do manifesto k8s
- **Fechada por:** usuário escolheu `k8s/25-redis.yaml`.

### AM-3 — Redis dedicado, nunca compartilhado
- **Fechada por:** já resolvida em rodada anterior; sem mudança nesta.

### AM-4 — `cistern` existe, pure Go
- **Fechada por:** já resolvida em rodada anterior; sem mudança nesta.

### AM-5 — Formato da entrada do CHANGELOG.md
- **Fechada por:** usuário escolheu entrada normal, sem destaque.

### AM-6 — Comportamento com Redis indisponível
- **Fechada por:** já resolvida em rodada anterior; sem mudança nesta.

### AM-7 — Estratégia de chave de cache
- **Fechada por:** `docs/DECISIONS.md:1638` resolve qual leitura é correta
  (chave composta).

### AM-8 — Forma concreta do tipo de chave/tag
- **Fechada por:** usuário escolheu `struct` `K` com função de normalização.

## Próximo

`validation.md: clean. Próximo: /change-plan cache-aside-get-tasks`
