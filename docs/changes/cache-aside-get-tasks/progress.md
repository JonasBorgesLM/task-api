---
slug: cache-aside-get-tasks
stage: progress
status: completed
---

# cache-aside-get-tasks — Progresso

**Itens:** 10/10 concluídos

### CI-1 — Dependência `cistern` + esqueleto do decorator (passthrough)
- **Status:** concluído
- **Verificação:** `make check` — ok (rodado em conjunto com CI-2, ver observação)
- **Arquivos:** `go.mod`, `go.sum`, `internal/task/cached_repository.go`, `internal/task/cached_repository_test.go`
- **Observações:**
  - **Desvio do plano, registrado:** CI-1 e CI-2 foram verificados juntos, não separadamente. `go mod tidy` (parte de `make check`) marca uma dependência como "should be direct"/candidata a remoção enquanto nada a importa de verdade — CI-1 sozinho (esqueleto sem cache) deixaria `cistern` tecnicamente "untidy" até CI-2 de fato usar `cistern.Cache`. Isso é uma restrição do próprio Go, não uma mudança de escopo: implementei os dois antes de rodar `make check` pela primeira vez.
  - `NewCachedRepository`'s assinatura mudou de `func(next Repository) *CachedRepository` (CI-1) para `func(next Repository, opts ...cistern.Option) (*CachedRepository, error)` (CI-2) exatamente como o próprio plano já antecipava ("assinatura final ganha o parâmetro de cache em CI-2") — não é um desvio, é o plano se cumprindo.

### CI-2 — Cache-aside em `FindAll` (L1+L2, chave composta, TTL)
- **Status:** concluído
- **Verificação:** `make check` — ok. `make test-integration` ainda não roda (Redis só existe a partir do CI-6) — o teste que genuinely precisa de Redis real fica para CI-3 (ver abaixo); o teste de fail-open não precisa.
- **Arquivos:** `internal/task/cached_repository.go`, `internal/task/cached_repository_test.go`, `README.md`
- **Observações:**
  - **Desvio do plano, registrado:** o plano previa o teste de fail-open (`TEST_REDIS_ADDR`, `//go:build integration`) em `cached_repository_redis_test.go`. Na prática, esse teste não precisa de Redis real — ele testa exatamente o comportamento quando Redis está inacessível (aponta para uma porta fechada, `127.0.0.1:1`). Ficou em `cached_repository_test.go`, sem tag, rodando na suíte padrão — mais forte que o planejado (não depende de infra nenhuma) e não perde cobertura. `cached_repository_redis_test.go` (com a tag `integration`) fica reservado para CI-3's teste de invalidação cross-replica, que genuinely precisa de dois processos falando com o mesmo Redis.
  - `cacheKey` não pôde ser literalmente `struct { UserID string; Statuses []Status; Priorities []Priority; Limit, Offset int }` como a escolha do usuário (AM-8) descreveu: `cistern.New[K, V]` exige `K comparable`, e slices não satisfazem `comparable`. Resolvido normalizando `statuses`/`priorities` para `string` (ordenada, joined) já dentro do struct — mesma ideia (struct tipado + função de normalização), só que a normalização produz campos comparáveis em vez de carregar as slices originais. Restrição da linguagem, não uma escolha de design diferente da decidida.
  - Teste de TTL usa espera real (`time.Sleep`), não relógio injetável: `memory.Option` do `cistern` só tem `WithMaxBytes`/`WithMaxEntries`/`WithOnEvict` — sem ponto de injeção de tempo, ao contrário do `tokenCache` deste projeto. Documentado no comentário do teste.
  - README.md: adicionada `TEST_REDIS_ADDR` à tabela de dependências externas de teste (seção Testing), como o plano previa para este CI.

### CI-3 — Invalidação síncrona em `Create`/`Update`/`Delete`, fail-open na escrita
- **Status:** concluído
- **Verificação:** `make check` — ok. `make test-integration` — ok, rodado de verdade contra Postgres real (já de pé) e um container Redis temporário (`redis:7-alpine`, removido depois — infraestrutura oficial ainda é CI-6). Falhas em `internal/attachment`/`cmd/api` nessa rodada são pré-existentes e não relacionadas: MinIO não estava de pé (`make storage-up` não rodado), `connection refused` na porta 9000 — nenhum arquivo de `internal/attachment` foi tocado por este trabalho.
- **Arquivos:** `internal/task/cached_repository.go`, `internal/task/cached_repository_test.go`, `internal/task/cached_repository_redis_test.go` (novo), `Makefile`
- **Observações:**
  - **Achado real durante a verificação, corrigido:** a primeira execução do teste cross-replica falhou (`nextA.FindAll called 0 times before the write, want 1`) — não por bug no decorator, mas porque o teste reusava `testUserID` como chave, e o Redis temporário manteve a entrada da rodada anterior (TTL de 1 min sobrevive entre execuções do `go test`). Corrigido gerando um `userID` único por execução (`cross-replica-test-<timestamp>`), em vez do `testUserID` compartilhado — confirmado estável rodando 3x seguidas (`-count=3`) sem flakiness.
  - Renomeei o teste cross-replica para `TestRedis_CachedRepository_CrossReplica_InvalidationPropagatesViaBus`, seguindo a convenção `TestPostgres_*` de `go-tests.md` (prefixo pelo backend que o teste precisa, por legibilidade — o build tag é o que de fato seleciona, não o nome).
  - **Gap do plano, corrigido:** o plano não mencionava o `Makefile` em nenhum CI. `make test-integration`/`test-integration-race` não passavam `TEST_REDIS_ADDR` ao `go test` — sem isso, o teste cross-replica sempre faria skip, mesmo com Redis de pé. Adicionada a variável (`TEST_REDIS_ADDR ?= localhost:6379`, mesmo padrão de `TEST_S3_ENDPOINT`) e passada nos dois targets, no mesmo espírito do resto do CI.
  - `InvalidateTag` do `cistern` de fato retorna o erro do `Bus.Publish` para quem chamou (confirmado pelo teste `TestCachedRepository_Create_InvalidationFailure_WriteStillSucceeds`) — `invalidateOwner` traduz isso para `OnInvalidationError`, nunca para o retorno de `Create`/`Update`/`Delete`, como o plano exigia.

### CI-4 — Configuração Redis em `internal/config`
- **Status:** concluído
- **Verificação:** `make check` — ok
- **Arquivos:** `internal/config/config.go`, `internal/config/config_test.go`, `.env.example`, `README.md`
- **Observações:**
  - Campos flat: `RedisAddr`, `RedisPassword`, `RedisUseTLS`, `RedisCacheTTL` (default `5m`), `RedisCacheL1TTL` (default `5s`) — todos opcionais, `RedisAddr` vazio desliga o cache inteiro, espelhando `DatabaseURL`.
  - **Adição além do que o plano especificou, dentro do espírito de "fail at startup" de `config-env.md`:** `Load()` agora rejeita `REDIS_CACHE_L1_TTL > REDIS_CACHE_TTL` — o mesmo invariante que `cistern.WithL1TTL` (RF-08) já impõe, só que checado aqui para falhar no startup com uma mensagem clara em vez de `cmd/api` descobrir isso mais tarde quando `cistern.New` recusar construir o cache. Coberto por `TestLoad_InvalidRedisCacheL1TTL_ExceedsCacheTTL`.
  - README.md: linha "quatro dependências de runtime" (Requirements) corrigida para citar `cistern`/`redisstore` como a quinta, condicional a `REDIS_ADDR`; tabela de Configuration ganhou as quatro linhas novas.

### CI-5 — Wiring no composition root (`cmd/api/newServer`)
- **Status:** concluído
- **Verificação:** `make check` — ok. Além disso, verificação manual de ponta a ponta: subi o binário real (`go run ./cmd/api`) contra Postgres real e um Redis temporário (`redis:7-alpine`, removido depois), confirmei `/health` respondendo e nenhum erro de wiring nos logs de inicialização (`database migrations applied` → `server started`, sem nada sobre Redis/cache no meio).
- **Arquivos:** `cmd/api/main.go`, `cmd/api/cached_task_repository_test.go` (novo), `internal/task/cached_repository.go`
- **Observações:**
  - **Gap real do plano, corrigido:** o plano não previa nenhuma função de limpeza para o cliente Redis/cache — `buildCachedTaskRepository` como planejado só devolvia `(task.Repository, error)`. Isso vazaria a conexão Redis e a goroutine de subscrição do `Bus` a cada restart do processo (grave em particular na própria suíte de testes de `cmd/api`, que constrói vários servidores). Corrigido: `CachedRepository` ganhou um `Close() error` (fecha `cache.Close()`, que por sua vez encerra a subscrição do `Bus`); `buildCachedTaskRepository` passou a devolver `(task.Repository, func() error, error)`, mesmo formato de `buildBlobStore`; a função de limpeza é composta em `closeAll` junto com `closeBlobs`/`closeDB`.
  - **Correção durante a implementação:** a primeira versão fazia `client.Options().TLSConfig = ...` *depois* de construir o cliente — a documentação do próprio `go-redis` avisa que isso é comportamento indefinido (`Options()` devolve a struct viva usada internamente, não uma cópia). Corrigido para montar `redis.Options` completo, incluindo `TLSConfig`, antes de `redis.NewClient`.
  - `buildCachedTaskRepository` não faz nenhum `Ping`/checagem de conectividade contra Redis no startup, ao contrário de `openDatabase`'s `PingContext` para `DATABASE_URL` — decisão deliberada, documentada no comentário da função: Redis é o cache que o `cistern` constrói para falhar aberto; travar o startup nele reverteria exatamente essa garantia. `cistern.New` ainda falha o startup para um erro de configuração real (ex.: L1TTL > TTL, já barrado antes em `config.Load`).
  - Testes de CI-5 vão direto contra `buildCachedTaskRepository` (não contra `newServer`/HTTP), mais preciso que o "testar via comportamento observável" que o plano cogitava como alternativa: `RedisAddr` vazio devolve `next` inalterado (checado por identidade de interface); `RedisAddr` setado devolve um `*task.CachedRepository` — inclusive contra um endereço que ninguém escuta (`127.0.0.1:1`), confirmando que a construção nunca falha por causa de conectividade, só a leitura (fail-open, já provado em CI-2).
  - Não adicionei health-check dedicado para Redis em `/health`/`/health/ready`, como o plano já previa como fora de escopo.

### CI-6 — Infra: `docker-compose.yml` + `k8s/25-redis.yaml` + `k8s/30-config.yaml`
- **Status:** concluído
- **Verificação:** `make check` — ok. `docker compose up -d redis` real (imagem `redis:7-alpine`), confirmado saudável, `maxmemory-policy allkeys-lru` ativo, usuário `cistern` autentica e fica restrito ao próprio keyspace/canal (`NOPERM` fora de `cistern:*`, `NOPERM` em `flushall`). `make test-integration` com esse Redis real — 100% verde em `internal/task`, incluindo o teste cross-replica. **Também subi um cluster `kind` descartável** (não fazia parte da verificação exigida pelo plano, mas `k8s-deploy.md` pede "prove it, don't read it") só para aplicar `k8s/25-redis.yaml` isoladamente e confirmar o mesmo comportamento de ACL dentro de um Deployment/Secret reais — idêntico ao docker-compose. Cluster destruído depois.
- **Arquivos:** `docker-compose.yml`, `k8s/25-redis.yaml` (novo), `k8s/30-config.yaml`, `internal/config/config.go`, `internal/config/config_test.go`, `.env.example`, `README.md`, `cmd/api/main.go`
- **Observações:**
  - **Gap real, corrigido, encontrado ao escrever o ACL do `docker-compose.yml`:** o usuário `cistern` que o ACL cria tem usuário *e* senha próprios — mas `buildCachedTaskRepository` (CI-5) só passava `Password` para o `redis.Options`, nunca `Username`. Sem isso, o cliente autenticaria como `default`, não como `cistern`, e a conexão real teria falhado (não pego pelos testes de CI-5, que não testam contra um Redis com ACL de verdade). Corrigido adicionando `RedisUsername` a `internal/config.Config` (campo novo, não previsto no plano original de CI-4) e passando `Username: cfg.RedisUsername` em `cmd/api/main.go`. Sincronizado nas "quatro partes" de `config-env.md`: `config.go`, `config_test.go`, `.env.example`, tabela do README — inclusive corrigindo um comentário em `.env.example` que dizia (incorretamente, antes desta correção) que o cluster k8s/docker-compose deste projeto não usa senha no Redis.
  - `docker-compose.yml`'s serviço `redis` usa `command:` (lista de argumentos), não `args:`, para o `--user cistern on >senha resetkeys ~cistern:* ...` — mesma regra exata documentada em `redisstore/README.md`. Usuário `default` fica sem senha só para o healthcheck (`redis-cli ping`); a API sempre autentica como `cistern`.
  - `k8s/25-redis.yaml` usa um wrapper de shell (`command: ["/bin/sh", "-c"]`, `$REDIS_PASSWORD` expandido pelo shell) em vez do mecanismo `$(VAR)` de substituição do próprio Kubernetes em `command`/`args` — mais fácil de verificar corretude por leitura, e efetivamente verificado rodando de verdade num cluster `kind`.
  - Redis não entra em `docs/RUNBOOK-BACKUP-RESTORE.md` como algo a persistir — é puramente um cache (`cistern`'s próprio README: "nothing may depend on the cache for correctness"), perder a instância é um cache frio, nunca perda de dado. Isso fica explícito no CI-7.

### CI-7 — `docs/RUNBOOK-BACKUP-RESTORE.md`: seção de Redis
- **Status:** concluído
- **Verificação:** `make check` — ok (mudança só de documentação)
- **Arquivos:** `docs/RUNBOOK-BACKUP-RESTORE.md`
- **Observações:**
  - Nova seção "Redis (cistern) — sem backup, e por quê", exatamente como o plano descreveu: explica que o Redis é puramente cache (nunca fonte de verdade), não tem passo de backup, e documenta o procedimento de *provisionamento* (não restauração) caso a instância precise ser recriada — `allkeys-lru`, ACL `cistern`, nunca compartilhada.
  - Nota de escopo no topo do arquivo (parágrafo "Fora de escopo") atualizada para apontar pra essa seção nova, mantendo a mesma convenção que já existia ali para `k8s/`.

### CI-8 — `CLAUDE.md`: entrada de dependência
- **Status:** concluído
- **Verificação:** `make check` — ok (mudança só de documentação)
- **Arquivos:** `CLAUDE.md`
- **Observações:**
  - Acrescentado `github.com/JonasBorgesLM/cistern` (+ `cistern/redisstore`) à lista de dependências diretas em "Don't add a dependency lightly", mesma forma das entradas existentes.
  - Contagem de módulos reconferida via `go list -deps -f '{{if not .Standard}}{{.Module}}{{end}}' ./cmd/api | sort -u`: 31 → 35 (bate exatamente com os 4 módulos novos: `cistern`, `cistern/redisstore`, `github.com/redis/go-redis/v9`, `go.uber.org/atomic`).

### CI-9 — `docs/ARCHITECTURE.md`: seção de design
- **Status:** concluído
- **Verificação:** `make check` — ok (mudança só de documentação)
- **Arquivos:** `docs/ARCHITECTURE.md`
- **Observações:**
  - Nova subseção "### Task list caching: cistern (opt-in, REDIS_ADDR)", inserida ao final de "Design Decisions" (antes de "## Future Improvements"), mesmo nível de "Rate limiting"/"Link shortening" — descreve a forma final construída (decorator sobre Repository, chave composta, invalidação síncrona fail-open, contrato operacional do Redis, e a nota de que nada ali é backupeado) e linka `docs/DECISIONS.md` para o raciocínio completo, sem duplicá-lo.
  - Não adicionei `cached_repository.go` à árvore de Project Structure: essa árvore já é abreviada por convenção pré-existente (nem `postgres_breaker.go`, o precedente direto do decorator, aparece lá) — segui a mesma convenção em vez de introduzir uma inconsistência nova.
  - Não reabri nenhuma das duas menções em Future Improvements (rate limiting distribuído, cairn.redisstore) — continuam como estavam, corrigidas na sessão do `/decide`.

### CI-10 — `CHANGELOG.md`
- **Status:** concluído
- **Verificação:** `make check` — ok
- **Arquivos:** `CHANGELOG.md`
- **Observações:**
  - Nova seção `## [1.7.0] — a definir na tag`, acima de `[1.6.1]` (já
    tagueada — `v1.6.1` existe em `git tag`, confirmado antes de escrever
    aqui). Segue exatamente a convenção que o próprio projeto já usa (ver
    commit `3265bf7`, "a definir na tag" como placeholder até o release
    real). Entrada normal, sem destaque de mudança operacional — decisão do
    usuário (AM-5 da validação).
  - Classificado como **minor** (1.7.0, não patch): nova capacidade opt-in,
    zero mudança de contrato.
  - **Não** toquei `docs/openapi.yaml`'s `info.version` (ainda "1.6.0",
    pré-existentemente desatualizado em relação à tag `v1.6.1` — CLAUDE.md
    já registra esse tipo de drift como um problema conhecido). Bump de
    versão é passo de release, não deste CI; fora de escopo tocar um drift
    que já existia antes desta mudança.

## Achados do /pre-pr (fora dos 10 CIs originais)

Dois gaps reais encontrados só na revisão do `/pre-pr`, corrigidos antes do
gate final:

- **`.github/workflows/ci.yml` não provisionava Redis nenhum.** Sem isso, o
  teste cross-replica (`internal/task/cached_repository_redis_test.go`)
  faria skip silenciosamente pra sempre em CI — exatamente o risco que o
  próprio comentário do workflow já nomeia para Postgres ("filtering by
  name... silently skipped any integration test not named TestPostgres_*").
  Corrigido adicionando `redis` a `services:` (mesma forma de `postgres` —
  ao contrário de MinIO, que precisa de um `command` que `services:` não
  suporta, o teste de integração conecta como usuário `default`
  não-autenticado do Redis, então não precisa do ACL/`--maxmemory-policy`
  que só a instância real de produção exige) e `TEST_REDIS_ADDR` ao `env:`
  do job. Validado com `actionlint` (limpo) e `python3 -c "import yaml"`.
  O smoke test da imagem Docker não precisou de nenhuma mudança — já roda
  sem `DATABASE_URL`/serviços externos por design, e `REDIS_ADDR` ausente
  segue exatamente esse mesmo padrão.
- **Faltava uma regra em `.claude/rules/` para a área de cache**, item
  explícito do checklist do `/pre-pr`. Criada `.claude/rules/task-caching.md`
  (`paths: internal/task/cached_repository*.go`, `cmd/api/main.go`),
  destilando os invariantes já registrados em `CLAUDE.md`/`docs/DECISIONS.md`
  para disparar automaticamente na próxima edição desses arquivos — mesmo
  papel que `attachment-storage.md` já cumpre para `internal/attachment`.
