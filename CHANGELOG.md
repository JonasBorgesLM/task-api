# Changelog

Formato baseado em [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
versionamento seguindo [Semantic Versioning](https://semver.org/lang/pt-BR/).

Este é o primeiro release versionado do projeto — não há tags anteriores.
`v1.0.0` marca o ponto em que a API passa a ter contrato estável (`/v1`).

## [1.1.0] — a definir na tag

Lote das issues #96 a #106 (fora #95, já em v1.0.0). Nenhuma mudança
deste release quebra contrato existente da forma como o prefixo `/v1`
quebrou no release anterior: toda adição é opt-in, usa default generoso,
ou corrige um caso que já era bug (ver `docs/DECISIONS.md` para o
raciocínio de cada decisão).

### Adicionado
- `DELETE /v1/files/{key}` — remove um anexo individual. Metadado e blob
  saem na mesma requisição, não só o metadado com o blob deixado para o
  coletor de órfãos (ver `docs/DECISIONS.md`).
- Quota de armazenamento de anexos por usuário
  (`ATTACHMENT_MAX_BYTES_PER_USER`, 500 MiB por padrão). Checada antes do
  upload; um usuário já no limite recebe `400` sem que o arquivo seja
  lido.
- Teto de sessões simultâneas por usuário (`AUTH_MAX_SESSIONS_PER_USER`,
  10 por padrão) — login além do limite evict a sessão mais antiga em vez
  de recusar. `POST /v1/auth/logout-all` — encerra todas as sessões da
  conta de uma vez, incluindo a que fez a chamada, para quem suspeita de
  um token vazado.
- Espelhamento opcional de logs para um coletor OTLP/HTTP (SigNoz, um
  OTel Collector, etc.), via `CRIER_OTLP_ENDPOINT` — desligado por
  padrão, e nunca substitui o log em stdout. `GET /debug/vars` ganha
  `crier_buffer_depth` e `crier_records_dropped` quando habilitado.
  Detalhes em `docs/DECISIONS.md`.
- `user_id` no log de acesso de toda requisição autenticada, e
  `version`/`commit` do build em `GET /debug/vars` — stampados via
  `-ldflags` a partir de `git describe`/`git rev-parse` por `make build`
  e `make docker-build`.
- Suporte a TLS de saída verificado: a imagem `scratch` passa a incluir
  o bundle de CA do sistema, habilitando `DATABASE_URL` com
  `sslmode=verify-full` e `ATTACHMENT_S3_USE_SSL=true` contra um
  endpoint S3 real — nenhum dos dois funcionava antes desta versão.
- `startupProbe` no manifest de Kubernetes (`k8s/40-api.yaml`), cobrindo
  o tempo de uma migration lenta sem exigir um `Job` separado.
- CI: build e smoke test real da imagem Docker a cada push (`docker
  build` mais requisições HTTP reais contra o container), e
  `go mod tidy -diff` travando um `go.mod`/`go.sum` desalinhado.

### Alterado
- `Vary: Origin` agora é enviado em toda resposta quando CORS está
  habilitado, não só nas de origem aceita — evita que um cache
  compartilhado sirva, para uma origem que deveria recebê-lo, uma
  resposta cacheada sem `Access-Control-Allow-Origin`.
- `docker-compose.yml` configura as variáveis `ATTACHMENT_S3_*` por
  padrão — os endpoints de anexo, antes silenciosamente desligados
  nesse ambiente, agora funcionam com `docker compose up`.

### Corrigido
- Um `taskID`/`storageKey` malformado agora responde `404` contra o
  backend PostgreSQL, igual ao backend em memória — antes chegava ao
  cast `::uuid` do banco e virava `500`, logado como falha inesperada.
- `attachment.Service.Delete` fechava a última lacuna dessa mesma
  classe de bug: era o único dos quatro métodos do pacote que não
  chamava `isValidID` antes de operar, então um `storage_key`
  malformado em `DELETE /v1/files/{key}` ainda alcançava o cast
  `::uuid` do banco (issue #107).
- `middleware.Recovery` não escreve mais uma segunda resposta HTTP
  quando o handler já tinha começado a responder antes do panic — evita
  corpo duplicado/inválido e o aviso "superfluous response.WriteHeader"
  nos logs do runtime.

### Segurança
- Auditoria completa de todo call site de log do projeto: nenhum
  interpola valor sensível (DSN, credencial de S3, token) na mensagem.
  A garantia — que já dependia de `pgx`/`minio-go` redigirem
  credenciais no próprio texto de erro — agora está travada por dois
  testes de regressão, não só por leitura de código. Ver
  `docs/DECISIONS.md`.

### Migração necessária
1. Se `DATABASE_URL` aponta para PostgreSQL, a migration `0008` (índice
   para a evicção de sessões) é aplicada automaticamente na subida
   (`DB_AUTO_MIGRATE=true`, o padrão) — nenhuma ação manual, a menos que
   `DB_AUTO_MIGRATE=false` esteja em uso, caso em que rode
   `make migrate-up` (ou `cmd/migrate`) antes do deploy.
2. Nenhuma outra migração de cliente é necessária — toda adição deste
   release é opt-in (`CRIER_OTLP_ENDPOINT` desligado por padrão) ou usa
   default generoso que não afeta uso normal (quota de 500 MiB, teto de
   10 sessões).

---

## [1.0.0] — a definir na tag

### Adicionado
- Anexos em tasks: upload, download, dois backends de storage
  intercambiáveis (filesystem local ou S3-compatível via `minio-go`),
  coletor de blobs órfãos.
- Rate limiting (via `moat`) agora cobre toda a superfície da API, não
  só `/auth/*`. Um cliente que fazia rajadas contra rotas de task pode
  passar a ver `429`. Configure `TRUSTED_PROXIES` se a API roda atrás de
  proxy reverso — sem isso, todos os clientes dividem um único balde de
  limite.
- Headers de segurança (`secureheaders`, via `moat`) em todas as
  respostas, inclusive de erro.
- Sanitização de campos de texto livre (título/descrição) antes de
  persistir.
- `govulncheck` fail-closed no gate de CI — merge bloqueado se uma
  vulnerabilidade acionável for encontrada.
- Deploy validado sem downtime em Kubernetes (rolling update, probes,
  drain configurável via `HTTP_PRE_SHUTDOWN_DELAY`).

### Alterado
- **BREAKING:** todas as rotas passam a exigir o prefixo `/v1`. Um
  cliente chamando os caminhos antigos sem prefixo recebe `404`. Rotas
  operacionais (`/health`, `/health/ready`, `/debug/vars`) permanecem
  sem prefixo, deliberadamente — ver `docs/DECISIONS.md`.
- Go atualizado para 1.26.6, fechando 4 vulnerabilidades da stdlib que o
  código alcançava.

### Não muda
- Autenticação por token via header já era obrigatória desde release
  anterior (PR #39) — não é mudança deste release.

### Migração necessária
1. Aplicar a migração `0007` (schema de anexos).
2. Atualizar clientes para usar o prefixo `/v1`.
3. Definir `ATTACHMENT_STORAGE_DIR` ou as variáveis `ATTACHMENT_S3_*`
   conforme o backend de storage escolhido — sem isso, os endpoints de
   anexo não são registrados.
4. Se a API roda atrás de proxy reverso, configurar `TRUSTED_PROXIES`.

### Dívidas técnicas conhecidas, não bloqueantes
- Manifests de Kubernetes usam `Secret` nativo (base64, não criptografia)
  para credenciais do S3 — mitigado por aviso explícito no manifest;
  solução real depende da definição do cluster de produção (issue #62).
