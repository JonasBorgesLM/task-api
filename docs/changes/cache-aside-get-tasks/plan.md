---
slug: cache-aside-get-tasks
stage: plan
tier: full
items: 10
sources_mtime:
  docs/changes/cache-aside-get-tasks/context.md: 2026-09-24T13:12:17-03:00
  docs/changes/cache-aside-get-tasks/validation.md: 2026-09-24T13:17:34-03:00
---

# cache-aside-get-tasks — Plano

## Objetivo

Depois desta mudança, `GET /v1/tasks` é servido por uma camada cache-aside
(`cistern`, L1 em processo + L2 Redis via `redisstore` + `Bus` de
invalidação), sem que `task.Service`/`task.Handler` saibam que Redis existe.
Toda escrita que afeta a listagem invalida o cache do dono na mesma chamada,
mas uma falha de Redis nunca falha a escrita nem a leitura. Redis passa a
existir como infraestrutura real do projeto (`docker-compose.yml`, `k8s/`),
dedicado a este uso, nunca compartilhado com `moat`/`cairn`.

## Restrições herdadas

- `Service`/`Handler` em `internal/task` nunca importam `cistern`/Redis.
- Toda invalidação é síncrona na chamada de escrita; uma falha de
  `Bus.Publish` **nunca** é retornada como erro da escrita — só logada via
  callback, nunca propagada por `errors.Is`/sentinel.
- `pageETag`/`X-Total-Count` continuam vindo das linhas/contagem
  efetivamente devolvidas pelo `Repository` (cache incluso) — nunca de outra
  fonte.
- `CountAll` e `CountByStatusAndPriority` nunca passam pelo cache — sempre
  delegam direto a `next`.
- Redis roda `maxmemory-policy allkeys-lru`, nunca `noeviction`; instância
  dedicada, nunca compartilhada com `moat`/`cairn`.
- `internal/config` importa só a stdlib; o cliente Redis real e a instância
  `cistern` são montados só em `cmd/api`.
- `cmd/api/newServer` é o único lugar que decide se a camada de cache está
  ativa — `REDIS_ADDR` vazio significa sem cache, `taskRepo` sem decorator,
  espelhando exatamente como `DATABASE_URL` vazio já significa "sem
  PostgreSQL" hoje.
- A chave de cache de `FindAll` cobre `(userID, statuses, priorities, limit,
  offset)` — nunca só `userID` com filtro aplicado em Go (`docs/DECISIONS.md:1638`).
- `go.mod`: `cistern`/`redisstore` são pure Go, sem cgo — propriedade a
  preservar no build `scratch`.

## Itens

### CI-1 — Dependência `cistern` + esqueleto do decorator (passthrough)

- **Arquivos:**
  - `go.mod`, `go.sum` — adiciona `github.com/JonasBorgesLM/cistern` e
    `github.com/JonasBorgesLM/cistern/redisstore` como dependências diretas.
  - `internal/task/cached_repository.go` (novo) — `CachedRepository`
    implementando `Repository` por inteiro, delegando todos os 7 métodos
    (`Create`, `FindByID`, `FindAll`, `Update`, `Delete`, `CountAll`,
    `CountByStatusAndPriority`) direto para `next`, sem cache ainda.
- **Faz:** `type CachedRepository struct { next Repository }`;
  `func NewCachedRepository(next Repository) *CachedRepository` (assinatura
  final ganha o parâmetro de cache em CI-2, o de invalidação em CI-3 — este
  CI só estabelece a forma, espelhando `postgres_breaker.go`'s
  `breakerRepository`). Todos os métodos delegam sem alterar comportamento —
  `var _ Repository = (*CachedRepository)(nil)` garante a implementação
  completa da interface.
- **Não faz:** nenhuma lógica de cache, TTL, invalidação ou config Redis —
  isso é CI-2/CI-3. Não toca `cmd/api` (wiring é CI-5).
- **Testes:** `internal/task/cached_repository_test.go` (sem build tag,
  novo) — `TestNewCachedRepository_DelegatesEveryMethodToNext`: um `next`
  fake mínimo (reaproveita o padrão de `raceRepository`, só com contadores
  de chamada) provando que cada um dos 7 métodos chama o correspondente em
  `next` exatamente uma vez e devolve o valor/erro dele sem alteração.
- **Verificação:** `make check`
- **Depende de:** _nada_

### CI-2 — Cache-aside em `FindAll` (L1+L2, chave composta, TTL)

- **Arquivos:**
  - `internal/task/cached_repository.go` — adiciona `cacheKey` struct e
    `FindAll` cacheado.
  - `internal/task/cached_repository_test.go` — testes de hit/miss/TTL.
  - `internal/task/cached_repository_redis_test.go` (novo,
    `//go:build integration`) — teste do fallback fail-open com L2
    indisponível.
  - `README.md` — tabela de dependências externas de teste (seção Testing)
    ganha `TEST_REDIS_ADDR`, mesmo padrão de `TEST_DATABASE_URL`/
    `TEST_S3_ENDPOINT`.
- **Faz:**
  - `cacheKey struct { UserID string; Statuses []Status; Priorities
    []Priority; Limit, Offset int }`.
  - `cacheKeyString(k cacheKey) string` — serializa determinística: `UserID`,
    depois `Statuses`/`Priorities` **ordenados alfabeticamente** antes de
    juntar (o pedido já chega validado/deduplicado de `Service.ListTasks`,
    mas não necessariamente na mesma ordem entre chamadas — ordenar aqui
    evita miss desnecessário sem mudar corretude), depois `Limit`/`Offset`.
  - `cacheKeyTag(k cacheKey) []string` — só `{"user:" + k.UserID + ":lists"}`,
    **sem** os filtros/paginação — é isso que permite `InvalidateTag`
    derrubar todas as variantes cacheadas de um usuário de uma vez (CI-3),
    sem enumerar chaves.
  - `NewCachedRepository(next Repository, opts ...cistern.Option) (*CachedRepository, error)`
    passa a construir `cistern.New[cacheKey, []Task]("tasks", cacheKeyString,
    append(opts, cistern.WithTags(cacheKeyTag))...)` internamente — espelha
    a assinatura do próprio exemplo de referência do `cistern`
    (`examples/taskapi/taskapi.go`).
  - `FindAll` monta a `cacheKey` dos parâmetros recebidos e chama
    `c.cache.GetOrLoad(ctx, key, func(ctx) ([]Task, error) { return
    c.next.FindAll(ctx, userID, limit, offset, statuses, priorities) })`.
  - `FindByID`, `CountAll`, `CountByStatusAndPriority` continuam passthrough
    puro (herdado de CI-1, sem mudança).
- **Não faz:** nenhuma invalidação em escrita ainda (CI-3). Não decide
  TTLs/endereço de Redis reais — isso são `cistern.Option`s passadas de fora
  pelo composition root (CI-5); os testes deste CI constroem suas próprias
  `Option`s locais.
- **Testes:**
  - `internal/task/cached_repository_test.go`:
    - `TestCachedRepository_FindAll_SecondCallWithSameParamsSkipsNext` — só
      L1 (`cistern.WithL1(memory.New(...))`, sem L2/Bus), `next` fake com
      contador de chamadas, chama `FindAll` duas vezes com os mesmos
      parâmetros, afirma `next`'s contador == 1.
    - `TestCachedRepository_FindAll_DifferentFiltersAreDifferentCacheEntries` —
      duas chamadas com `statuses` diferentes, mesmo `userID`, afirma
      `next`'s contador == 2 (prova que a chave é composta, não só `userID`
      — fecha a garantia de `docs/DECISIONS.md:1638`).
    - `TestCachedRepository_FindAll_KeyOrderInsensitiveToFilterOrder` —
      `statuses=[done,pending]` vs `statuses=[pending,done]`, mesmo
      `userID`/paginação, afirma `next`'s contador == 1 (prova a
      normalização por ordenação).
    - `TestCachedRepository_CountAll_NeverCached` — chama `CountAll` várias
      vezes seguidas com os mesmos parâmetros, afirma `next.CountAll` é
      chamado a cada vez (fecha TG-2 da validação).
    - TTL: reaproveita o padrão `fakeClock`/`now func() time.Time` de
      `internal/user/token_cache_test.go` — se `cistern`'s `memory.Store`
      aceitar um relógio injetável, usar o dele; senão, testar TTL com um
      `WithL1TTL` curto (ex. 20ms) e uma espera real curta, documentando por
      quê (constraint da lib externa, não escolha deste projeto).
  - `internal/task/cached_repository_redis_test.go` (`//go:build
    integration`, `TEST_REDIS_ADDR` — skip se ausente):
    `TestCachedRepository_FindAll_RedisDown_FallsBackToNext` — configura
    `redisstore.New` apontando para um endereço que recusa conexão, afirma
    que `FindAll` ainda devolve os dados corretos via `next` (fail-open),
    sem erro (fecha TG-3 da validação).
- **Verificação:** `make check` (unitários) + `make test-integration`
  (com `TEST_REDIS_ADDR` setado, uma vez a infra de CI-6 existir)
- **Depende de:** CI-1

### CI-3 — Invalidação síncrona em `Create`/`Update`/`Delete`, fail-open na escrita

- **Arquivos:**
  - `internal/task/cached_repository.go` — `Create`/`Update`/`Delete`
    passam a invalidar; campo `OnInvalidationError func(error)`.
  - `internal/task/cached_repository_test.go` — testes de invalidação.
  - `internal/task/cached_repository_redis_test.go` — teste cross-replica
    via `Bus` real.
- **Faz:**
  - `Create(ctx, task) error`: chama `c.next.Create(ctx, task)`; se `nil`,
    chama `c.cache.InvalidateTag(ctx, cacheKeyTag(cacheKey{UserID:
    task.UserID})[0])` (só o tag de usuário, sem filtros/paginação — uma
    função auxiliar `userTag(userID string) string` evita montar um
    `cacheKey` incompleto só para extrair o tag); se `InvalidateTag` falhar,
    chama `c.OnInvalidationError(err)` (se não-nil) e **retorna sucesso
    mesmo assim** — mesmo padrão de `examples/taskapi/taskapi.go`'s
    `Create`.
  - `Update(ctx, task) error`: mesma forma, usando `task.UserID` (o dono já
    validado por `FindByID` antes de `Service.UpdateTask` chamar `Update` —
    `CachedRepository` não revalida ownership, só repassa).
  - `Delete(ctx, id, userID) error`: mesma forma, usando o `userID` recebido
    diretamente.
  - `OnInvalidationError` fica `nil`-safe: se não configurado, uma falha de
    invalidação é silenciosamente ignorada (o composition root em CI-5
    sempre configura um logger, mas o decorator não pode exigir isso para
    compilar/funcionar em teste).
- **Não faz:** não desfaz a escrita em `next` se a invalidação falhar; não
  faz retry da invalidação (fail-open é aceitar a janela de staleness até o
  TTL do L1, não tentar mascará-la).
- **Testes:**
  - `internal/task/cached_repository_test.go`:
    - `TestCachedRepository_Create_InvalidatesOwnersCachedLists` — popula o
      cache via `FindAll`, chama `Create` para o mesmo `userID`, chama
      `FindAll` de novo com os mesmos parâmetros, afirma que `next.FindAll`
      foi chamado de novo (cache invalidado, não só "ainda funciona").
    - `TestCachedRepository_Update_InvalidatesOwnersCachedLists` /
      `TestCachedRepository_Delete_InvalidatesOwnersCachedLists` — mesma
      forma.
    - `TestCachedRepository_Create_InvalidationFailure_WriteStillSucceeds` —
      um `Bus` fake cujo `Publish` sempre retorna erro (implementa
      `bus.Bus`), afirma que `Create` ainda devolve `nil` (sucesso) e que
      `OnInvalidationError` foi chamado exatamente uma vez com o erro.
    - `TestCachedRepository_Create_NextFails_NeverInvalidates` — `next.Create`
      retorna erro, afirma que `InvalidateTag`/`Bus.Publish` **não** foi
      chamado (não invalidar uma escrita que não aconteceu).
  - `internal/task/cached_repository_redis_test.go` (`//go:build
    integration`): `TestCachedRepository_CrossReplica_InvalidationPropagatesViaBus`
    — duas instâncias de `CachedRepository` sobre o mesmo Redis real
    (`redisstore.New`/`redisstore.NewBus` apontando pro mesmo endereço,
    simulando duas réplicas), popula o cache de uma via `FindAll`, escreve
    através da outra via `Create`, afirma que a primeira reflete a
    invalidação (próxima `FindAll` bate em `next` de novo) sem esperar o
    L1 TTL — fecha TG-1 da validação.
- **Verificação:** `make check` + `make test-integration`
- **Depende de:** CI-2

### CI-4 — Configuração Redis em `internal/config`

- **Arquivos:**
  - `internal/config/config.go` — novos campos e parsing em `Load()`.
  - `internal/config/config_test.go` — casos default/válido/inválido.
  - `.env.example` — novas variáveis documentadas.
  - `README.md` — tabela de Configuration (novas linhas) e seção
    Requirements (menção a Redis, mesmo padrão de "Docker e Docker Compose
    (optional)" já existente para Postgres).
- **Faz:** campos flat em `Config`, todos opcionais (zero value = cache
  desligado, espelhando `DatabaseURL`):
  - `RedisAddr string` — `REDIS_ADDR`, vazio por padrão. Vazio = sem cache
    (CI-5 não constrói o decorator).
  - `RedisPassword string` — `REDIS_PASSWORD`, vazio por padrão (nunca
    logado — mesma cautela de `AttachmentS3SecretKey`).
  - `RedisUseTLS bool` — `REDIS_USE_TLS`, `false` por padrão (mesmo nome que
    `ATTACHMENT_S3_USE_SSL` já usa para esse padrão).
  - `RedisCacheTTL time.Duration` — `REDIS_CACHE_TTL`, default `5m`
    (`WithTTL` do `cistern` — L2/TTL geral).
  - `RedisCacheL1TTL time.Duration` — `REDIS_CACHE_L1_TTL`, default `5s`
    (`WithL1TTL` — curto de propósito: é o teto de staleness entre réplicas
    quando o `Bus` perde um evento, `redisstore/README.md`'s própria
    orientação "keep it short (seconds)").
  - Todos parseados com os helpers `parseDuration`/`parseBool`/etc. já
    existentes em `config.go` — nenhum novo helper.
- **Não faz:** não monta `redis.Options`/`cistern.Option` aqui —
  `internal/config` continua sem saber que `cistern`/`redis` existem como
  tipos; isso é `cmd/api` (CI-5).
- **Testes:** `internal/config/config_test.go` — `TestLoad_RedisAddr_DefaultEmpty`,
  `TestLoad_RedisCacheTTL_DefaultFiveMinutes`,
  `TestLoad_RedisCacheL1TTL_DefaultFiveSeconds`,
  `TestLoad_RedisCacheTTL_InvalidDuration_ReturnsError` (mesmo padrão dos
  testes de duration já existentes, ex. `DBCallTimeout`).
- **Verificação:** `make check`
- **Depende de:** _nada_

### CI-5 — Wiring no composition root (`cmd/api/newServer`)

- **Arquivos:** `cmd/api/main.go`.
- **Faz:** em `newServer`, depois de `taskRepo` já montado (linha ~285-305
  hoje) e antes de `taskSvc := task.NewService(taskRepo)`: se
  `cfg.RedisAddr != ""`, constrói `redis.Options{Addr: cfg.RedisAddr,
  Password: cfg.RedisPassword, ContextTimeoutEnabled: true}` (+ `TLSConfig`
  se `cfg.RedisUseTLS`), `redis.NewClient(...)`, `redisstore.New(client,
  ...)`, `redisstore.NewBus(client)`, `memory.New(...)` para o L1, e chama
  `task.NewCachedRepository(taskRepo, cistern.WithL1(l1),
  cistern.WithL2(l2), cistern.WithBus(bus), cistern.WithTTL(cfg.RedisCacheTTL),
  cistern.WithL1TTL(cfg.RedisCacheL1TTL))`, atribuindo o resultado de volta a
  `taskRepo` antes de `task.NewService`. `OnInvalidationError` é ligado a
  `logger.Error("cache invalidation failed", "error", err)` (mesmo `logger`
  que todo o resto de `newServer` já usa). Se `cfg.RedisAddr == ""`,
  `taskRepo` segue sem decorator, exatamente como hoje.
- **Não faz:** não adiciona um health-check dedicado para Redis em
  `/health`/`/health/ready` — fica fora de escopo (fail-open já cobre uma
  falha de Redis sem derrubar a aplicação; adicionar uma probe é uma
  melhoria observacional separada, não exigida pela decisão).
- **Testes:** `cmd/api/main_test.go` (ou `boundary_test.go`, seguir o
  arquivo que já cobre `newServer`'s wiring condicional de outras
  dependências opcionais, ex. `AttachmentS3*`) —
  `TestNewServer_RedisAddrEmpty_TaskRepoUnwrapped` (usa reflection/type
  assertion ou um contador de chamadas indireto via comportamento observável
  — se não houver um jeito limpo de inspecionar o tipo concreto, testar via
  comportamento: com `RedisAddr` vazio, duas chamadas idênticas a `GET
  /v1/tasks` batem no banco duas vezes, o que já é o comportamento atual e
  não precisa de um teste novo além dos já existentes).
  `TestNewServer_RedisAddrSet_ButUnreachable_ServerStillStartsAndServes`
  (`//go:build integration`, opcional — confirma que um `REDIS_ADDR` mal
  configurado não impede o servidor de subir, coerente com fail-open).
- **Verificação:** `make check` + `make test-integration`
- **Depende de:** CI-3, CI-4

### CI-6 — Infra: `docker-compose.yml` + `k8s/25-redis.yaml` + `k8s/30-config.yaml`

- **Arquivos:**
  - `docker-compose.yml` — novo serviço `redis`.
  - `k8s/25-redis.yaml` (novo).
  - `k8s/30-config.yaml` — novas chaves `REDIS_*`.
  - `README.md` — seção Kubernetes ganha uma linha citando o novo manifesto
    (mesmo padrão da menção a `10-postgres.yaml`/`20-minio.yaml` já lá).
- **Faz:**
  - `docker-compose.yml`: serviço `redis`, imagem `redis:7-alpine` (ou a mais
    recente `redis:8-alpine` compatível — confirmar tag disponível no
    momento da implementação), `command` configurando `--maxmemory-policy
    allkeys-lru` e um usuário ACL dedicado via `--user cistern ... resetkeys
    ~cistern:* resetchannels &cistern:* -@all +get +set +del +mget +incr
    +pexpire +publish +subscribe` (adaptado da linha exata de
    `redisstore/README.md`), `healthcheck` via `redis-cli ping` (mesmo
    padrão de intervalo/timeout/retries dos outros serviços).
  - `k8s/25-redis.yaml`: `Deployment` + `Service`, `emptyDir` (cluster de
    validação descartável, mesmo padrão de `10-postgres.yaml`/
    `20-minio.yaml` — sem persistência real), mesma política
    `allkeys-lru`/ACL do compose.
  - `k8s/30-config.yaml`: adiciona `REDIS_ADDR` (DNS interno do novo
    Service), `REDIS_CACHE_TTL`, `REDIS_CACHE_L1_TTL`; `REDIS_PASSWORD` **não**
    entra aqui (vai num `Secret`, fora do escopo deste CI — ou reaproveita o
    padrão de secret já usado para `DATABASE_URL`/credenciais, a confirmar
    contra o manifesto de Secret existente ao implementar).
- **Não faz:** não persiste dados reais (mesma ressalva de disposable
  validation cluster que já vale para Postgres/MinIO em `k8s/`) — não é
  template para produção.
- **Testes:** `_nenhum — infraestrutura declarativa, não código_`; validado
  manualmente subindo `docker compose up -d redis` e `make test-integration`
  contra ele (parte da verificação deste item, não um teste automatizado
  novo).
- **Verificação:** `docker compose up -d redis` sobe e passa healthcheck;
  `make test-integration` com `TEST_REDIS_ADDR` apontando pro container
  passa (fecha a verificação real de CI-2/CI-3's testes de integração).
- **Depende de:** CI-4

### CI-7 — `docs/RUNBOOK-BACKUP-RESTORE.md`: seção de Redis

- **Arquivos:** `docs/RUNBOOK-BACKUP-RESTORE.md`.
- **Faz:** nova seção `### Redis (cistern)`, espelhando a estrutura das
  seções existentes (Postgres, Anexos): como o Redis deste projeto é
  **puramente um cache** (`allkeys-lru`, nada de fonte-de-verdade — `cistern`
  README: "Be a source of truth. Nothing may depend on the cache for
  correctness."), a seção documenta explicitamente que **não há backup a
  fazer** — perder a instância inteira é equivalente a um cold cache, não
  perda de dados — e cobre em vez disso o procedimento de recriação
  (variáveis de conexão, ACL, `maxmemory-policy`) caso a instância precise
  ser recriada do zero.
- **Não faz:** não trata Redis como se fosse dado durável (isso seria
  reintroduzir o erro que a correção de `docs/DECISIONS.md` já apontou).
- **Testes:** `_nenhum — documentação_`.
- **Verificação:** revisão de leitura.
- **Depende de:** CI-6

### CI-8 — `CLAUDE.md`: entrada de dependência

- **Arquivos:** `CLAUDE.md`.
- **Faz:** acrescenta `cistern` (core + `redisstore`) à lista de
  dependências diretas em "Don't add a dependency lightly", com a mesma
  forma das entradas existentes (o que faz, referência a
  `docs/DECISIONS.md`); reconfirma a contagem de módulos via `go list -deps
  -f '{{if not .Standard}}{{.Module}}{{end}}' ./cmd/api | sort -u` e atualiza
  o número citado.
- **Não faz:** não duplica o invariante de comportamento (já escrito em
  "Architecture — do not violate this" durante o `/decide` desta sessão) —
  só a entrada de inventário de dependências.
- **Testes:** `_nenhum — documentação_`.
- **Verificação:** revisão de leitura; `go list -deps` confere o número.
- **Depende de:** CI-1

### CI-9 — `docs/ARCHITECTURE.md`: seção de design

- **Arquivos:** `docs/ARCHITECTURE.md`.
- **Faz:** nova subseção `### Task list caching: cistern` (mesmo nível das
  outras em "Design Decisions" — Rate limiting, Link shortening), descrevendo
  a forma final (decorator sobre `Repository`, L1+L2+Bus, fail-open,
  `allkeys-lru`, chave composta) e linkando `docs/DECISIONS.md` § "Cache-aside
  para GET /v1/tasks" para o raciocínio completo — não duplica o raciocínio,
  só a forma.
- **Não faz:** não reabre nenhuma das duas menções em Future Improvements
  (rate limiting distribuído, `cairn.redisstore`) — continuam trabalho
  adjacente, não fechado por esta mudança.
- **Testes:** `_nenhum — documentação_`.
- **Verificação:** revisão de leitura.
- **Depende de:** CI-5

### CI-10 — `CHANGELOG.md`

- **Arquivos:** `CHANGELOG.md`.
- **Faz:** entrada normal (decisão do usuário nesta sessão — sem destaque
  de "mudança operacional") descrevendo: `GET /v1/tasks` agora é servido por
  cache-aside (`cistern`); rodar o projeto localmente via `docker-compose`
  agora inclui um serviço Redis.
- **Não faz:** não marca **BREAKING** — nenhum cliente de API quebra.
- **Testes:** `_nenhum — documentação_`.
- **Verificação:** revisão de leitura.
- **Depende de:** CI-5, CI-6, CI-9

## Mapa de dependências

```
CI-1 → CI-2 → CI-3 → CI-5 → CI-9 ┐
CI-1 → CI-8                      │
CI-4 → CI-5                      ├→ CI-10
CI-4 → CI-6 → CI-7               │
CI-6 ─────────────────────────────┘
```

## Entregáveis

- [x] `internal/task/cached_repository.go` + `cached_repository_test.go` +
      `cached_repository_redis_test.go`
- [x] `internal/config/config.go` + `config_test.go` — campos `Redis*`
      (mais `RedisUsername`, gap encontrado durante CI-6, não previsto na
      lista original de CI-4)
- [x] `cmd/api/main.go` — wiring condicional em `newServer`
- [x] `go.mod`/`go.sum` — `cistern` + `redisstore`
- [x] `docker-compose.yml` — serviço `redis`
- [x] `k8s/25-redis.yaml` (novo) + `k8s/30-config.yaml` atualizado
- [x] `.env.example` + tabela Configuration/Requirements/Testing/Kubernetes
      do `README.md`
- [x] `docs/RUNBOOK-BACKUP-RESTORE.md` — seção Redis
- [x] `CLAUDE.md` — entrada de dependência
- [x] `docs/ARCHITECTURE.md` — seção de design
- [x] `CHANGELOG.md` — entrada

## Riscos e como o plano os cobre

| Risco | Coberto por |
|---|---|
| Chave de cache ignora filtros e devolve página errada | CI-2, `TestCachedRepository_FindAll_DifferentFiltersAreDifferentCacheEntries` |
| `CountAll`/`X-Total-Count` fica cacheado por engano, servindo total desatualizado | CI-2, `TestCachedRepository_CountAll_NeverCached` |
| Falha de Redis na invalidação derruba uma escrita (`CreateTask`/etc.) | CI-3, `TestCachedRepository_Create_InvalidationFailure_WriteStillSucceeds` |
| Falha de Redis na leitura derruba `GET /v1/tasks` | CI-2, `TestCachedRepository_FindAll_RedisDown_FallsBackToNext` (integração) |
| Invalidação não propaga entre réplicas de fato (a garantia central da decisão) | CI-3, `TestCachedRepository_CrossReplica_InvalidationPropagatesViaBus` (integração, Redis real) |
| Redis provisionado com o contrato errado (`noeviction`, compartilhado com cairn/moat) | CI-6, comando explícito `allkeys-lru`/ACL dedicada em `docker-compose.yml`/`k8s/25-redis.yaml` |
| `cistern`/`redisstore` trazem cgo e quebram o build `scratch` | CI-1, verificado antes desta sessão (`go-redis/v9`, confirmado pure Go) — `make check`/build da imagem em CI-1 confirma de novo |
| Documentação (README/RUNBOOK/CLAUDE.md/ARCHITECTURE.md) fica desatualizada | CI-4, CI-6, CI-7, CI-8, CI-9 — cada uma no mesmo CI que introduz a mudança que descreve |
