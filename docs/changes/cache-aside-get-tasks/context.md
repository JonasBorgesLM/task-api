---
slug: cache-aside-get-tasks
stage: context
tier: full
sources_mtime:
  docs/DECISIONS.md: 2026-09-24T13:05:25-03:00
  docs/ARCHITECTURE.md: 2026-09-24T13:05:59-03:00
  CLAUDE.md: 2026-09-24T13:05:44-03:00
  docs/openapi.yaml: 2026-09-16T10:45:32-03:00
---

# cache-aside-get-tasks — Contexto

## Pedido

Implementar cache-aside para `GET /v1/tasks` (`task.Repository.FindAll`) usando
`cistern`, configurado com L1 em processo + L2 `redisstore` (Redis) + `Bus`
para invalidação entre réplicas, conforme a decisão registrada em
`docs/DECISIONS.md` § "Cache-aside para GET /v1/tasks: cistern com
L1+redisstore e Bus entre réplicas" — reconstruído depois que essa decisão foi
corrigida nesta mesma sessão (via `/decide`): o texto original superestimava a
garantia de consistência do `Bus` e não especificava o contrato operacional
correto do Redis. A escolha de L1+redisstore+Bus continua a mesma; o que
mudou é a justificativa e o contrato operacional.

## Escopo

**Dentro:**
- Um decorator de `task.Repository` (`cachingRepository`/`CachedRepository` —
  ver "Já é um item diferido?" abaixo para o nome exato que a própria lib
  `cistern` já usa em seu exemplo de referência) que serve `FindAll` a partir
  do cache (L1 em processo, depois L2 `redisstore`) e invalida via `Bus` em
  `Create`/`Update`/`Delete` — sem alterar `Service`/`Handler`.
- Invalidação síncrona na chamada de escrita, mas **uma falha de publicação
  nunca falha a escrita** — é reportada via hook (`OnInvalidationError`,
  ligado em `cmd/api`), e o pior caso degrada para servir uma entrada L1
  stale até o TTL dela expirar. Isto é comportamento documentado do próprio
  `cistern` (fail-open), não uma frouxidão da implementação.
- Wiring do cliente Redis + instância `cistern` em `cmd/api/newServer`
  (composição raiz), com os campos de configuração correspondentes como
  campos flat em `internal/config.Config`.
- Redis como infraestrutura real deste projeto pela primeira vez, **dedicado
  ao `cistern`**: `docker-compose.yml`, um novo manifesto `k8s/*-redis.yaml`,
  atualização de `k8s/30-config.yaml`, extensão de
  `docs/RUNBOOK-BACKUP-RESTORE.md`. Configurado `maxmemory-policy
  allkeys-lru` (nunca `noeviction` — esse é o contrato do `cairn.Store`,
  para dados duráveis, não de um cache) — **nunca compartilhado** com o rate
  limiter do `moat` nem com `cairn.Store`, mesmo com bancos lógicos
  distintos (`maxmemory-policy` vale pra instância inteira).
- Nova dependência `cistern` (+ `redisstore`, que usa `github.com/redis/go-redis/v9`
  — cliente pure Go, sem cgo) em `go.mod`.
- Preservar o contrato de `ETag`/`X-Total-Count` de `GET /v1/tasks` sem
  alteração observável.
- Chave de cache que contempla a assinatura real de `FindAll` —
  `(userID, statuses, priorities, limit, offset)` — não só `userID` (ver
  "Decisões registradas", D5, abaixo: já existe uma decisão registrada dizendo
  que não há índice dedicado para `status`/`priority`, o que torna a chave de
  cache o único lugar que hoje reduz o custo real desses filtros).

**Fora** (decisões próprias, não destravadas por esta mudança):
- Trocar o rate limiter (`moat/ratelimit`) de in-process para um store
  distribuído/Redis — precisaria da própria instância Redis, nunca a do
  `cistern`.
- Trocar o backend do encurtador de links (`cairn`) de `memstore` para
  `redisstore` — mesma razão: precisa de instância própria, `cistern` proíbe
  compartilhamento.
- Qualquer mudança ao corpo/schema de `GET /v1/tasks`, ou ao gap pré-existente
  de documentação sobre `Cache-Control` nesse endpoint (ver "Contrato
  afetado" abaixo).

## Superfície de código

| Arquivo | Papel na mudança |
|---|---|
| `internal/task/repository.go:50` | Interface `Repository` que o decorator de cache precisa envolver por inteiro |
| `internal/task/postgres_breaker.go:31,47` | Precedente direto de decorator (`NewBreakerRepository(next Repository, ...) Repository`) — mesma forma a replicar |
| `internal/task/service.go:134,208,332,365,386,420` | `CreateTask`, `ListTasks`, `UpdateTask`, `DeleteTask`, `TransitionStatus`, `CompleteTask` — os quatro pontos de invalidação; `CompleteTask` invalida "de graça" por ser um wrapper fino sobre `TransitionStatus` |
| `internal/task/handler.go:151-195,280,292` | `listTasks`, `pageETag`/`taskETag` — contrato de ETag/X-Total-Count que o cache não pode alterar |
| `internal/user/token_cache.go` | Precedente de cache L1 in-process — padrão de teste a replicar para a camada L1; **rejeitado como desenho final** (aqui a consistência é requisito, não uma janela de staleness tolerada) |
| `internal/config/config.go:167,456` | `Config` struct e `Load()` — novos campos Redis como campos flat, stdlib-only |
| `cmd/api/main.go:255,285-305,807` | `newServer`, wiring de `taskRepo`/`taskSvc`, `openDatabase` — único lugar que monta o cliente Redis e decide se o decorator de cache está ativo |
| `docker-compose.yml` | Serviços atuais: `postgres`, `minio`, `minio-bucket`, `api`, `web`, `swagger-ui` — nenhum Redis hoje |
| `k8s/10-postgres.yaml`, `k8s/20-minio.yaml` | Padrão a seguir para o novo manifesto Redis (cluster de validação descartável, `emptyDir`) |
| `k8s/30-config.yaml`, `k8s/40-api.yaml` | ConfigMap e Deployment (`replicas: 1`) que precisam dos novos campos de conexão Redis |
| `docs/RUNBOOK-BACKUP-RESTORE.md` | Precisa de uma seção nova de backup/restore de Redis |
| `go.mod` | Nova dependência direta `cistern` (+ `redisstore`, transitiva `go-redis/v9`) |

## Decisões registradas que isto toca

- **`docs/DECISIONS.md:3026` § "Cache-aside para GET /v1/tasks"** (a decisão
  principal, com seu parágrafo de correção de 2026-09-24 já incorporado):
  L1+`redisstore`+`Bus`; invalidação síncrona nas quatro escritas, mas uma
  falha de publicação **nunca** falha a escrita (fail-open, como o próprio
  `cistern` documenta); `ETag` preservado; Redis provisionado junto, com
  `maxmemory-policy allkeys-lru` (nunca `noeviction`), ACL dedicada, TLS fora
  de dev, **nunca compartilhado** com `moat`/`cairn`. O "Por quê" agora
  justifica a escolha por pagar a infraestrutura uma vez só (o que já
  destrava "Distributed rate limiting" em `docs/ARCHITECTURE.md`), não por
  uma consistência categórica que o `cistern` de fato não entrega.
- **`docs/DECISIONS.md:458` § "ETag de GET /v1/tasks"** — o hash continua
  vindo de `(id, version)` das linhas efetivamente devolvidas; a camada de
  cache nunca vira segunda fonte desse hash; nenhuma comparação fraca (`W/`).
- **`docs/DECISIONS.md:1560` § "Cache de ValidateToken"** — precedente
  **explicitamente rejeitado** como desenho final para este cache (TTL local
  puro não seria suficiente), mas o padrão de teste (`now func() time.Time`
  injetável, `fakeClock`) continua o modelo a seguir para a camada L1.
- **`docs/DECISIONS.md:276` § "Topologia de deploy"** — produção roda 1
  réplica hoje; D1 inverte a lógica de "esperar múltiplas réplicas para
  justificar Redis" citando esta seção. Qualquer código novo (cache-aside
  incluso) precisa continuar correto durante a janela de rollout em que dois
  pods coexistem brevemente, não só em regime estável de 1 réplica.
- **`docs/DECISIONS.md:1638` § "Índices para status/priority em tasks"**
  (achado novo nesta reconstrução) — nenhum índice dedicado para
  `status`/`priority` foi criado (medição mostrou custo irrelevante,
  0,15–26ms); `FindAll` com esses filtros usa o índice existente
  `idx_tasks_user_id_created_at_id`. **Restringe diretamente a chave de
  cache:** a camada de cache-aside é o único lugar que hoje reduz o custo
  real de filtrar por `status`/`priority` — uma chave que ignore essas
  variações devolveria a página errada para um filtro diferente. A chave
  precisa contemplar `(userID, statuses, priorities, limit, offset)`.
- **`docs/DECISIONS.md:2394` § "Backend do cairn.Store"** — define
  `maxmemory-policy noeviction` como contrato **do `cairn`**, para dados
  duráveis. D1 (corrigida) é explícita: esse contrato **não se aplica** ao
  Redis do `cistern`, que exige o oposto (`allkeys-lru`).

**Veredito do decisions-reader: não contradiz.** A tarefa descrita — Redis
novo, `cistern` no `go.mod`, invalidação síncrona sem falhar a escrita, ETag
preservado, `allkeys-lru` nunca `noeviction`, instância nunca compartilhada —
é exatamente o que D1 (com a correção) prescreve. O único risco de
contradição seria implementar a invalidação como bloqueante/forte (o texto
pré-correção sugeria isso) ou copiar o contrato `noeviction` do `cairn.Store`
— ambos já sinalizados como erro pela própria correção.

## Invariantes aplicáveis

**Bloqueantes — violar isto é bug, não estilo:**
- `Service`/`Handler` em `internal/task` continuam completamente inconscientes
  de Redis/`cistern` — nenhum import direto neles.
- Toda escrita que afeta a listagem invalida via `Bus` **de forma síncrona,
  dentro da própria chamada** — não pode depender só do TTL.
- Uma falha ao publicar a invalidação **nunca** é retornada como falha de
  escrita — só reportada via `OnInvalidationError` (fiado em `cmd/api`, fora
  de `Service`/`Handler`); pior caso é servir uma entrada L1 stale até o TTL
  dela expirar. Não "consertar" isso fazendo a falha de invalidação falhar a
  escrita — reverteria o fail-open deliberado do `cistern`.
- `pageETag` continua hash de `(id, version)` das linhas efetivamente
  retornadas; `X-Total-Count` continua vindo de um `Repository.CountAll`
  real, nunca cacheado.
- A instância Redis roda `maxmemory-policy allkeys-lru` (nunca `noeviction`)
  e não é compartilhada com o rate limiter do `moat` nem com `cairn.Store`.
- O cache-aside é um decorator sobre a **interface** `task.Repository`
  (montado em `cmd/api/newServer`), nunca embutido só em
  `postgresRepository`.
- `internal/config` continua importando só a stdlib; campos Redis são flat,
  parseados pelos helpers `parse*` já existentes; o cliente Redis real e a
  instância `cistern` são montados em `cmd/api`, nunca guardados em `Config`.
- `Repository.FindAll` continua dono de ordenação, paginação e filtros — a
  camada de cache chaveia/invalida em torno das chamadas existentes.
- `Handler.handleServiceError` continua o único lugar que mapeia erro→status
  HTTP; um erro de Redis não vira um novo branch nem é engolido em silêncio.

**Precisam mudar junto:**
- `docker-compose.yml`, `k8s/30-config.yaml`, novo `k8s/*-redis.yaml` — serviço
  Redis dedicado, `allkeys-lru`.
- `internal/config/config.go` + `config_test.go` + `.env.example` + tabela de
  Configuration do `README.md` — as "quatro sincronizadas" de
  `config-env.md`, para cada variável Redis nova.
- `go.mod` e o parágrafo de contagem de dependências/pure-Go do `CLAUDE.md`.
- `docs/ARCHITECTURE.md` — seção de design real descrevendo o que foi
  construído (as duas menções em Future Improvements já foram corrigidas
  nesta sessão, mas só referenciam a decisão — não substituem uma seção de
  design própria).

**Proibido sem perguntar antes:**
- Fazer uma falha de invalidação falhar a escrita.
- Compartilhar a instância Redis do cache com `moat`/`cairn`.
- Rodar essa instância com `noeviction`.
- Adicionar `cistern`/cliente Redis sem confirmar pure Go sem cgo.
- Trocar o rate limiter ou o backend do `cairn` para Redis de carona.

**Testes:** fakes, nunca lib de mock; teste que precise de Redis real vai em
`//go:build integration`; invalidação sensível a concorrência precisa de
teste concorrente real sob `-race`.

## Contrato afetado

Nenhuma mudança de schema, parâmetro ou código de status em `GET /v1/tasks` —
`ETag`, `If-None-Match`/`304`, `X-Total-Count` continuam exatamente como
documentados hoje.

**Gap pré-existente, não causado por esta mudança:** a descrição em prosa do
`304` (docs/openapi.yaml:889-897) promete um header `Cache-Control` que o
bloco `headers:` estruturado de nenhuma das duas respostas (`200`/`304`)
realmente declara. Fora do escopo desta mudança corrigir isso.

## Superfície de testes

**internal/task** (pacote principal desta mudança):
- Novo `internal/task/caching_repository.go` + `caching_repository_test.go`
  (sem build tag), espelhando a forma de `postgres_breaker.go`/
  `postgres_breaker_test.go` — decorator testado isoladamente contra um
  `Repository` fake mínimo como `next`.
- Teste de concorrência/race no mesmo arquivo, seguindo
  `TestBreakerRepository_Update_ConcurrentCallsAreRaceFree`
  (postgres_breaker_test.go:139).
- Teste de TTL/clock injetável no mesmo arquivo, copiando a forma de
  `internal/user/token_cache_test.go`'s `fakeClock`/`newTestCache(ttl)`.
- Teste explícito de que uma falha de invalidação **não** falha a escrita
  (mock/fake de `Bus` retornando erro em `Publish`) e que `CountAll` nunca é
  interceptado pelo cache.
- Teste de integração (`//go:build integration`, Redis real) para a
  invalidação cross-replica de fato via `Bus`, e para o fallback fail-open de
  leitura com Redis indisponível.
- **Não** em `service_test.go`/`handler_test.go`: o decorator fica abaixo de
  `Service`. Os testes de ETag/`X-Total-Count` existentes não deveriam
  precisar mudar.

## Artefatos que precisam mudar junto

- [x] `docs/openapi.yaml` — n/a, nenhuma mudança de contrato.
- [ ] `README.md` — sim: dependências de runtime e seções
      Requirements/Configuration/Testing/Kubernetes precisam citar Redis.
- [x] `CLAUDE.md` — já atualizado e corrigido nesta sessão via `/decide`
      (invariante da camada de cache, com o comportamento fail-open e o
      contrato `allkeys-lru`/instância dedicada); falta a entrada na lista de
      dependências quando `cistern` for de fato adicionado ao `go.mod`.
- [x] `docs/ARCHITECTURE.md` — as duas menções a Redis em Future Improvements
      já foram corrigidas nesta sessão; falta a seção de design descrevendo o
      que foi construído, a escrever quando implementado.
- [ ] `.env.example` — sim: novas variáveis `REDIS_*`/`CISTERN_*`.
- [ ] `CHANGELOG.md` — sim, quando implementado.
- [x] migração `NNNN_*.{up,down}.sql` + `migrate_test.go` — n/a.
- [ ] `docs/RUNBOOK-BACKUP-RESTORE.md` — sim, seção de backup/restore de
      Redis.
- [ ] `docker-compose.yml` + `k8s/*.yaml` — sim, serviço/manifesto Redis
      dedicado (`allkeys-lru`), `k8s/30-config.yaml` com os novos campos.

## Já é um item diferido?

**Sim, indiretamente.** As duas menções a Redis em `docs/ARCHITECTURE.md`'s
Future Improvements ("Distributed rate limiting", linha 308; "cairn.redisstore
as the link-shortening backend", linha 315) foram corrigidas nesta sessão:
ambas agora declaram consistentemente que (a) esta decisão é o que provisiona
Redis no projeto; (b) essa instância roda `allkeys-lru`; (c) o próprio
`cistern` **proíbe** compartilhá-la com `moat`/`cairn` — não é mais
apresentado como uma opção em aberto; (d) qualquer adoção futura de qualquer
um dos dois bullets precisa da própria instância Redis, separada. Nenhum dos
dois bullets é fechado por esta mudança — continuam sendo trabalho futuro,
próprio, adjacente.

Nenhum bullet de Future Improvements antecipava um cache para
`GET /v1/tasks`/`task.Repository.FindAll` em si — isso não é um item
diferido sendo fechado, é trabalho novo.

## Perguntas em aberto

- Nomes exatos das variáveis de ambiente Redis (`REDIS_ADDR`?
  `CISTERN_REDIS_*`?) — a decidir no `/change-plan`.
- Nome/numeração do novo manifesto k8s (`k8s/15-redis.yaml` vs
  `k8s/25-redis.yaml`) — cosmético, a decidir no plano.
- `CHANGELOG.md`: formato da entrada, já que não quebra nenhum cliente de API
  mas adiciona um requisito de infra novo para quem roda o projeto localmente.
- A forma exata do tipo de chave/tag do `cistern.Cache[K,V]` para
  `(userID, statuses, priorities, limit, offset)` — o exemplo de referência
  do próprio `cistern` (`examples/taskapi/taskapi.go`) cacheia só por
  `owner`, sem paginação/filtro; `docs/DECISIONS.md:1638` (D5 acima) já
  estabelece que a chave *precisa* contemplar o filtro completo, mas a forma
  concreta (struct + função de normalização, dado que `Service.ListTasks`
  já valida/deduplica os filtros antes de chegar em `Repository`) fica para
  o `/change-plan`.
