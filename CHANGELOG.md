# Changelog

Formato baseado em [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
versionamento seguindo [Semantic Versioning](https://semver.org/lang/pt-BR/).

Este é o primeiro release versionado do projeto — não há tags anteriores.
`v1.0.0` marca o ponto em que a API passa a ter contrato estável (`/v1`).

## [1.4.0] — a definir na tag

Fase 14, restante (CI-10 a CI-12) — PRs #190, #191, #192. **Minor, não
major, e sem mudança de contrato nenhuma desta vez:** verificado contra
o histórico real (`git diff v1.3.0..develop -- docs/openapi.yaml
internal/ cmd/` vazio) — nada em `docs/openapi.yaml`, `internal/` ou
`cmd/` mudou desde `v1.3.0`. Este lote inteiro é frontend: tema escuro
(paleta, alternância, aplicação) e o redesenho do controle de status da
task como menu de ícone. Nenhum endpoint, campo, código de status ou
comportamento de resposta muda para um cliente da API.

### Alterado
- `web/` ganha tema escuro: segue a preferência do sistema
  operacional por padrão, com um controle (Sistema/Claro/Escuro) que
  permite escolher explicitamente e persiste a escolha entre sessões.
  Paleta própria para o modo escuro (não é o modo claro escurecido),
  auditada em WCAG AA nas duas variantes.
- `web/`'s `TaskStatusControls` — os até três botões de texto sempre
  visíveis ("Move to X") viram um único gatilho de ícone que abre um
  menu com as transições legais da task (mesmo padrão de pull-down
  menu do macOS/iOS); uma transição ilegal fica ausente do menu, não
  mais visível-e-desabilitada. A tabela de transições legais em si não
  muda — só a apresentação.

## [1.3.0] — a definir na tag

Fase 14 (redesenho visual do frontend + filtro de tasks) — PRs #165,
#166, #167, #168, #171, #174, #177, #178, #180, #181, #183, #185, #187.
**Minor, não major:** a única mudança de contrato é aditiva (dois query
params novos, opcionais, em `GET /v1/tasks`) — nenhuma rota, campo ou
código de status existente muda de comportamento, e um cliente que não
envia os dois parâmetros novos vê exatamente o que via antes. O restante
do lote é redesenho visual do frontend (fora do contrato de API) e
correções de auditoria (E2E, acessibilidade) — ver `CHANGELOG.md`'s
seção "Alterado" desta entrada para o que efetivamente é observável por
um cliente, e `docs/changes/frontend-redesign/` para o resto.

### Adicionado
- `GET /v1/tasks` aceita dois query params novos, opcionais: `status` e
  `priority`, filtrando a lista por esses campos — combinam com `AND`
  quando os dois estão presentes (ex.: `?status=pending&priority=high`
  retorna só tasks que são as duas coisas ao mesmo tempo). Omitir os
  dois continua retornando exatamente o que retornava antes; um valor
  que não é um dos enums conhecidos de `status`/`priority` retorna
  `400`, mesmo formato de erro já usado para `limit`/`offset` inválidos
  (issue #175).
- `web/` ganha os controles de filtro correspondentes na lista de tasks
  (dois `<select>`, "Todos" = sem filtro) — issue #176.

### Alterado
- `web/` — segundo passe visual sobre a SPA introduzida em v1.2.0: app
  shell persistente, telas de login/registro com painel informativo,
  hierarquia de cor/densidade na lista de tasks, tema com efeitos
  visuais (glow de foco, elevação em hover, entre outros). Nenhuma
  mudança de contrato de API — é puramente visual/estrutural do
  frontend, sem novo endpoint, campo ou comportamento que um cliente da
  API observe. Detalhe completo em `docs/changes/frontend-redesign/`.
- `docs/openapi.yaml`'s descrição de `GET /v1/tasks` reescrita para
  documentar a interação entre filtro e paginação (o filtro é aplicado
  antes da janela `limit`/`offset`, não depois) — só clareza de
  documentação, o comportamento em si (ordenação, janela) não mudou.

## [1.2.0] — a definir na tag

Lote das issues #112 a #118 e #130 (Fase 12 — modo duplo de autenticação),
mais #119 a #129 e #153 (Fase 13 — frontend). **Minor, não major:**
nenhuma mudança deste release quebra o contrato
existente. Um cliente que só usa `Authorization: Bearer` continua
funcionando exatamente como antes, sem alteração de rota, código de
status, ou formato de resposta — o cookie é um segundo *transporte* para
a mesma sessão opaca, não um segundo mecanismo de autenticação (ver
`docs/DECISIONS.md` § "Autenticação: modo duplo"). A única mudança de
texto de erro (`missing or malformed Authorization header` →
`missing or invalid session credential`, quando nenhuma credencial é
enviada) nunca esteve fixada como exemplo no contrato — só o caso
"token presente mas inválido" estava, e esse texto não mudou.

### Adicionado
- Segundo modo de autenticação: cookie httpOnly (`session_token`), além
  do Bearer já existente. `POST /auth/login` passa a também gravar esse
  cookie (`Secure` por padrão, `SameSite=Lax`, carregando o mesmo token
  opaco do corpo da resposta); `RequireAuth` aceita Bearer ou cookie,
  com Bearer vencendo quando ambos estão presentes.
  `POST /auth/logout`/`logout-all` passam a também limpar o cookie.
- `GET /v1/auth/csrf-token` — emite o token CSRF que toda requisição
  mutadora (`POST`/`PUT`/`PATCH`/`DELETE`) sem `Authorization` deve
  enviar em `X-CSRF-Token`. Isso inclui `POST /auth/login` e
  `POST /auth/register`, fechando deliberadamente o gap de "login CSRF"
  — ver `docs/DECISIONS.md`.
- `attachments_enabled` (boolean) na resposta de `GET /v1/auth/me` —
  sinaliza se as rotas de anexo existem neste deployment, sem depender
  de inferir isso de um `404` que também pode significar outra coisa
  (issue #130).
- `Access-Control-Allow-Credentials: true` em toda resposta CORS de
  origem permitida — necessário para o cookie de sessão funcionar contra
  um frontend hospedado em outra origem.
- `CSRF_SECRET` (obrigatório, mínimo 32 bytes — o processo recusa
  iniciar sem ele) e `COOKIE_INSECURE` (default `false`; só para
  desenvolvimento local sobre HTTP puro) — ver README.md.
- `docs/openapi.yaml` ganha o segundo `securityScheme` (`cookieAuth`),
  a resposta `403` (`Forbidden`) em toda rota mutadora, e documentação
  do fluxo de CSRF de ponta a ponta.
- `web/` — a primeira SPA do projeto (Vite + React + TypeScript, Fase 13,
  issues #119–#129): registro, login/logout, CRUD de task, transições de
  status e anexos (upload com progresso, preview, download, exclusão),
  autenticada pelo cookie httpOnly + CSRF acima. Quatro estados de UI
  explícitos (loading/empty/error/sucesso) em cada tela, navegável só por
  teclado, auditoria de acessibilidade automatizada sem violação (CI-10)
  e E2E real contra `docker compose up` — incluindo o teste que mais
  importa, um `503` de banco fora do ar não desloga a sessão (CI-11).
  Versionada e lançada junto com o resto do repo, sem tag própria — ver
  `docs/DECISIONS.md` § "Frontend: Vite (SPA)...".

### Alterado
- Toda resposta a um método seguro (`GET`/`HEAD`/`OPTIONS`/`TRACE`)
  passa a incluir um `Set-Cookie` do cookie CSRF (`HttpOnly`, nunca
  exposto a JavaScript) — aditivo; um cliente que ignora cookies, como
  todo cliente Bearer-only até hoje, não é afetado.

### Segurança
- CSRF (`moat/csrf`, double-submit cookie assinado com HMAC) aplicado
  globalmente a toda requisição mutadora que não carrega
  `Authorization` — cobre tanto o cookie de sessão quanto
  `login`/`register` sem sessão nenhuma ainda, o que fecha um gap que a
  maioria das implementações de CSRF deixa aberto.

### Dívidas técnicas conhecidas, não bloqueantes
- A resposta `403` de CSRF não usa o envelope `{"error": "..."}` do
  resto da API — é texto puro (`Forbidden`) escrito pelo handler padrão
  de `moat/csrf`. Ver `docs/ARCHITECTURE.md` § Future Improvements.
- `PATCH /tasks/{id}/status`'s `409` (transição ilegal vs. conflito de
  concorrência) não tem um código de erro legível por máquina — o
  frontend distingue os dois casos por match de string na mensagem
  (`web/src/api/errors.ts`), frágil a uma mudança futura do texto.
  Registrado deliberadamente, não corrigido em silêncio — ver issue
  #153 e `docs/changes/web-frontend/validation.md`'s `AM-5`.

### Migração necessária
1. Definir `CSRF_SECRET` (≥32 bytes aleatórios) antes de subir esta
   versão — `cmd/api` recusa iniciar sem ele. `COOKIE_INSECURE` só é
   necessário em desenvolvimento local sobre HTTP puro; nunca em
   produção.
2. Nenhuma migração de schema. Nenhuma ação é necessária para um
   cliente que só usa `Authorization: Bearer` — o modo cookie é
   estritamente aditivo.

---

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
