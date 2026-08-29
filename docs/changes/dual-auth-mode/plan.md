---
slug: dual-auth-mode
stage: plan
tier: full
items: 8
sources_mtime:
  docs/changes/dual-auth-mode/context.md: 2026-08-29T00:00:00Z
  docs/changes/dual-auth-mode/validation.md: 2026-08-29T00:00:00Z
---

# dual-auth-mode — Plano

**Nota sobre numeração:** `#112`–`#118` são os números reais das issues
`12.1`–`12.7` no GitHub (criadas via `create-fase12-13-frontend.sh`,
confirmadas via `gh issue list`). `CI-8` não corresponde a nenhuma delas —
sua issue própria é **#130**.

## Objetivo

Depois desta mudança: a API aceita `Authorization: Bearer` (inalterado) ou
um cookie `httpOnly` de sessão; toda rota mutadora acessada sem
`Authorization` (cookie autenticado, ou `login`/`register` sem sessão)
exige um token CSRF válido obtido de `GET /v1/auth/csrf-token`; nenhum
cliente `curl`/serviço documentado no README muda de comportamento;
`GET /v1/auth/me` passa a informar `attachments_enabled`.

## Restrições herdadas

- CSRF nunca se aplica a uma requisição com `Authorization: Bearer` —
  quebrar isso quebra todo cliente `curl`/serviço do README (regra central
  da issue #112, não negociável).
- `internal/config` não importa nada fora da stdlib — `CSRF_SECRET` é
  `string`, convertido para `secret.Value` só em `cmd/api`.
- `internal/middleware` não pode ganhar conhecimento de domínio (o que é
  um "usuário", um "token de sessão") — isso continua em `internal/user`.
- `cmd/api` é o único lugar que monta tipos cross-package — o
  `csrf.Protector` é construído em `newServer`, como `ratelimit`/`secureheaders`.
- Toda config nova falha no startup, nunca em request-time — `CSRF_SECRET`
  ausente ou < 32 bytes (`csrf.MinSecretLen`) impede o processo de subir.
- Nunca logar um `Config` inteiro; `CSRF_SECRET` entra na mesma lista de
  "reportado por nome, nunca por valor" que `AttachmentS3SecretKey` já usa.
- Ordem de middleware existente (`RequestID → Logging → secureheaders →
  CORS → Recovery → [globalLimiter]`) não é reordenada — o novo middleware
  de CSRF entra nela, não a substitui.
- O corpo de `POST /auth/login`/`logout`/`logout-all` não muda — só
  ganham `Set-Cookie`.

## Itens

### CI-1 — Registrar a decisão em DECISIONS.md e a ressalva em CLAUDE.md

- **Arquivos:**
  - `docs/DECISIONS.md` — nova seção "Autenticação: modo duplo (cookie
    httpOnly + Bearer), CSRF condicionado à origem da credencial",
    inserida logo após a seção existente "Autenticação: token via header,
    não cookie de sessão" (linha 10). A seção existente ganha uma frase
    final apontando para a nova — o texto original **não é reescrito**.
  - `CLAUDE.md` § "Things not to do without being asked" — a linha "Don't
    switch session auth to JWT, or add a second auth mechanism..." ganha
    uma ressalva: "— exceto o modo duplo cookie+Bearer decidido em
    `docs/DECISIONS.md` § '...'; ambos autenticam o mesmo token opaco, são
    dois transportes, não dois mecanismos."
- **Faz:** registra por que o cookie deixa de ser universalmente rejeitado
  (só o era pelo risco de CSRF, que passa a ser mitigado), a regra "CSRF
  se aplica quando a credencial não veio de `Authorization`" (unificada —
  ver achado em `validation.md`), e por que login/register também são
  protegidos (decisão do usuário: risco de login CSRF não é aceito).
- **Não faz:** não decide nada de implementação — é só o registro que os
  outros itens implementam.
- **Testes:** _nenhum — não altera comportamento_.
- **Verificação:** leitura humana (revisão de PR).
- **Depende de:** _nada_.

### CI-2 — Config: CSRF_SECRET e COOKIE_INSECURE

- **Arquivos:**
  - `internal/config/config.go` — campo `CSRFSecret string` (obrigatório,
    sem default, validado com `len(bytes) >= 32` — mesma constante que
    `csrf.MinSecretLen` documenta, duplicada como `.claude/rules` já
    aceita para não acoplar `config` a `moat/csrf`); campo
    `CookieInsecure bool` (`parseBool`, default `false`).
  - `internal/config/config_test.go` — casos válido/ausente/curto demais
    para `CSRFSecret`; default/true para `CookieInsecure`.
  - `.env.example` — `CSRF_SECRET` (sem valor, comentário explicando que é
    gerado com `moat/csrf.GenerateSecret` ou equivalente, nunca commitado)
    e `COOKIE_INSECURE=false`.
  - `README.md` — tabela de configuração ganha as duas linhas.
- **Faz:** as duas variáveis passam a existir, com falha no `Load` quando
  `CSRF_SECRET` está ausente ou é curto demais.
- **Não faz:** não constrói o `csrf.Protector` em si (isso é `cmd/api`,
  composition root — CI-5).
- **Testes:** `internal/config/config_test.go` —
  `TestLoad_CSRFSecret_Missing_ReturnsError`,
  `TestLoad_CSRFSecret_TooShort_ReturnsError`,
  `TestLoad_CSRFSecret_Valid`, `TestLoad_CookieInsecure_DefaultsFalse`,
  `TestLoad_CookieInsecure_True`.
- **Verificação:** `make test`.
- **Depende de:** _nada_.

### CI-3 — Middleware de sessão resolve cookie ou header, com precedência compartilhada

- **Arquivos:**
  - `internal/user/middleware.go` — `bearerToken` é substituído (ou
    complementado) por `credentialSource(r *http.Request) (source, token
    string)`: se `Authorization: Bearer <t>` presente → `("bearer", t)`;
    senão, se o cookie de sessão (nome `session_token`, constante nova)
    presente → `("cookie", t)`; senão → `("", "")`. `RequireAuth` passa a
    usar essa função; o resto do fluxo (`ValidateToken`, 401 vs. 503) não
    muda.
  - `internal/user/middleware_test.go` — precedência nos dois sentidos
    (header presente + cookie presente → header vence, testado
    explicitamente), autenticação só por cookie funciona, autenticação só
    por header continua idêntica (regressão), cookie malformado/vazio
    tratado como ausente (não gera 500).
- **Faz:** dá ao resto da cadeia (CI-6) uma função só, reaproveitada, para
  responder "esta requisição apresenta um `Authorization`?" sem depender
  de `RequireAuth` já ter rodado.
- **Não faz:** não decide nada de CSRF aqui — `credentialSource` só resolve
  qual credencial validar, CI-6 é quem decide se CSRF se aplica.
- **Testes:** ver acima.
- **Verificação:** `make test`.
- **Depende de:** _nada_.

### CI-4 — Login/logout/logout-all emitem e expiram o cookie de sessão

- **Arquivos:**
  - `internal/user/handler.go` — `login` seta `Set-Cookie: session_token=
    <token>; HttpOnly; Secure (a menos que cfg.CookieInsecure);
    SameSite=Lax; Path=/; Max-Age=<AUTH_SESSION_TTL em segundos>` na
    mesma resposta que já devolve o corpo JSON, sem alterá-lo. `logout` e
    `logoutAll` setam o mesmo cookie com `Max-Age=0` (expira), mesmos
    demais atributos — sem isso o browser não remove o cookie.
  - `internal/user/handler_test.go` — corpo de `login` inalterado
    (regressão explícita, comparando com o teste existente); os quatro
    atributos do cookie presentes; `logout`/`logoutAll` expiram o cookie
    mantendo a invalidação de sessão já testada.
- **Faz:** o cookie passa a existir de ponta a ponta (emitido, aceito por
  CI-3, expirado no logout).
- **Não faz:** não chama `csrf.Protector.Rotate` — isso é CI-6 (rotação de
  CSRF é uma preocupação do CSRF, não da emissão do cookie de sessão em
  si; são dois cookies diferentes).
- **Testes:** ver acima.
- **Verificação:** `make test`.
- **Depende de:** CI-2 (`CookieInsecure`), CI-3 (nome da constante do
  cookie, compartilhada).

### CI-5 — `csrf.Protector` construído em cmd/api; `GET /v1/auth/csrf-token`

- **Arquivos:**
  - `cmd/api/main.go` (`newServer`) — constrói `csrf.Protector` via
    `csrf.New(secret.New([]byte(cfg.CSRFSecret)), opts...)`, com
    `csrf.WithInsecureCookie()` só quando `cfg.CookieInsecure`. Falha de
    `csrf.New` (secret curto) já foi prevenida em CI-2, mas o erro ainda
    precisa ser tratado (mesma forma que outros erros de construção em
    `newServer`).
  - `internal/user/handler.go` — novo método `csrfToken`, público (sem
    `requireAuth`), que devolve `{"csrf_token": "<valor>"}` — o valor vem
    de `csrf.Token(r)` **depois** que a requisição passou pelo
    `Protector.Middleware` (ver CI-6; esta rota precisa estar coberta por
    ele para o token existir).
  - `internal/user/handler.go` (`RegisterRoutes`) — `GET /auth/csrf-token`
    registrado como rota pública, ao lado de `register`/`login`.
  - `docs/openapi.yaml` — novo path `/v1/auth/csrf-token`, `200` com
    `CsrfTokenResponse` (`csrf_token: string`), sem auth.
- **Faz:** dá ao frontend uma forma de obter o token antes de qualquer
  login, exatamente a decisão do usuário para AM-1.
- **Não faz:** não decide onde na cadeia o `Protector.Middleware` entra —
  isso é CI-6, do qual esta rota depende para funcionar de fato (o handler
  em si só lê `csrf.Token(r)`, não gera nada sozinho).
- **Testes:** `internal/user/handler_test.go` —
  `TestCSRFToken_Handler_ReturnsToken` (contra um fake que simula o
  middleware já ter rodado, ou teste de integração completo — preferir o
  de integração dado que a garantia real depende da cadeia inteira, ver
  CI-6).
- **Verificação:** `make test`.
- **Depende de:** CI-2.

### CI-6 — Middleware CSRF global, condicional por presença de `Authorization`

- **Arquivos:**
  - `internal/middleware/csrf.go` (novo) — `CSRF(p *csrf.Protector)
    Middleware`: para método seguro (`GET`/`HEAD`/`OPTIONS`), sempre
    delega a `p.Middleware(next)` (garante cookie+token, cobre a rota da
    CI-5 e qualquer `GET` autenticado por cookie). Para método mutador:
    se `Authorization` presente → `next.ServeHTTP` direto (pula CSRF —
    caminho Bearer); senão → delega a `p.Middleware(next)` (aplica
    Origin+token — cobre cookie autenticado **e** `login`/`register` sem
    sessão, com a mesma checagem, por decisão do usuário em AM-2). Esta
    função não sabe o que é "sessão" nem "usuário" — só olha o header
    `Authorization`, mantendo `internal/middleware` sem conhecimento de
    domínio.
  - `cmd/api/main.go` (`newServer`) — `middleware.CSRF(csrfProtector)`
    entra na cadeia global (`rootHandler`), depois de `CORS` (precisa do
    `Origin` já tratado por CORS não ser um pré-requisito real, mas a
    ordem lógica de "coisas de browser" fica junto) e antes de
    `Recovery`. `Rotate` é chamado dentro de `user.Handler.login`
    (portanto depende de CI-4 e de o `Protector` estar acessível ali —
    passado por parâmetro, como `userSvc` já é).
  - `internal/user/handler.go` (`login`) — chama `p.Rotate(w, r)` logo
    após autenticar com sucesso, antes de escrever a resposta (a ordem
    importa — `Rotate` só funciona antes do primeiro `Write`/`WriteHeader`,
    conforme o próprio `moat/csrf` documenta).
  - `internal/middleware/cors.go` — `corsAllowedHeaders` ganha
    `csrf.DefaultHeaderName` (`X-CSRF-Token`) na lista.
- **Faz:** fecha a regra central da #112: CSRF nunca se aplica a
  `Authorization: Bearer`, sempre se aplica a método mutador sem ele —
  incluindo `login`/`register`, por decisão do usuário.
- **Não faz:** não adiciona um segundo secret ou uma segunda tabela de
  sessão — reaproveita o `Protector` inteiro da `moat/csrf`, nada
  reimplementado.
- **Testes:** teste de integração HTTP real (`cmd/api`, junto de
  `TestIntegration_CORS_*`) — cookie sem token → `403`; cookie com token
  válido → passa; `Authorization: Bearer` sem token CSRF → passa
  (regressão explícita dos `curl` do README); `login` sem token CSRF →
  `403`; `login` com o token de `GET /auth/csrf-token` → passa; token
  emitido antes do login é rejeitado depois do login (prova de que
  `Rotate` rodou — o token pós-login é diferente do pré-login).
- **Verificação:** `make check` (a cadeia global mudou, vale o gate
  inteiro) + o teste de integração acima.
- **Depende de:** CI-3, CI-4, CI-5.

### CI-7 — CORS com credenciais

- **Arquivos:**
  - `internal/middleware/cors.go` (`CORS`) — quando a origem é permitida,
    emite também `Access-Control-Allow-Credentials: true`. Nunca junto de
    `*` (este código já não usa wildcard — vira um teste de regressão,
    não uma mudança de comportamento adicional).
  - `internal/middleware/cors_test.go` — `Access-Control-Allow-Credentials:
    true` presente só para origem permitida; ausente para origem não
    permitida; nunca coexiste com `Access-Control-Allow-Origin: *`.
- **Faz:** o navegador aceita enviar/receber o cookie em requisição
  cross-origin para uma origem já permitida.
- **Não faz:** não adiciona a origem do frontend de desenvolvimento a
  `CORS_ALLOWED_ORIGINS` — isso é configuração de ambiente
  (`docker-compose.yml`/`.env`), tratado quando `web/` existir de fato
  (Fase 13), não código deste `CI`.
- **Testes:** ver acima.
- **Verificação:** `make test`.
- **Depende de:** _nada_ (o header CSRF já existe como constante do
  `moat/csrf`, não precisa esperar CI-6 para ser referenciado).

### CI-8 — `attachments_enabled` em `GET /v1/auth/me`

*(fora da numeração #112–#118 original — decisão nova, ver `validation.md`
§ "Escopo novo". Issue própria: **#130**.)*

- **Arquivos:**
  - `internal/user/handler.go` — `NewHandler` ganha um parâmetro
    `attachmentsEnabled bool`; `me` inclui `"attachments_enabled":
    <valor>` no corpo já existente.
  - `cmd/api/main.go` — `user.NewHandler(userSvc, logger,
    attachmentsEnabled(cfg))` (a função `attachmentsEnabled(cfg)` já
    existe em `cmd/api/main.go:645`, hoje só usada para decidir se as
    rotas de anexo são registradas — reaproveitada, não duplicada).
  - `docs/openapi.yaml` — `User`/`MeResponse` (verificar no `openapi.yaml`
    se `/auth/me` usa o schema `User` puro ou um schema próprio — se for
    `User` puro, precisa de um schema novo só para esta resposta, já que
    `User` também descreve o corpo de `register`, que não deve ganhar o
    campo) ganha `attachments_enabled: boolean`.
- **Faz:** dá ao frontend (Fase 13, issue #127) o sinal que hoje não
  existe em lugar nenhum do contrato.
- **Não faz:** não muda `/health`/`/health/ready` (ficam sem esse detalhe
  de propósito — são rotas não-autenticadas, não deveriam vazar detalhe de
  deploy, conforme já registrado em `web-frontend/context.md`).
- **Testes:** `internal/user/handler_test.go` —
  `TestMe_Handler_IncludesAttachmentsEnabled` (`true`/`false` conforme o
  parâmetro construído).
- **Verificação:** `make test`.
- **Depende de:** _nada_ (independente do resto — pode ser feito em
  paralelo).

## Mapa de dependências

```
CI-1 (independente)
CI-2 → CI-4
CI-2 → CI-5 → CI-6
CI-3 → CI-4
CI-3 → CI-6
CI-4 → CI-6
CI-7 (independente)
CI-8 (independente)
```

## Entregáveis

- [ ] `docs/DECISIONS.md` — nova seção + ressalva na existente
- [ ] `CLAUDE.md` — ressalva na linha de "second auth mechanism"
- [ ] `internal/config` — `CSRF_SECRET`, `COOKIE_INSECURE`
- [ ] `internal/user/middleware.go` — `credentialSource`, `RequireAuth` estendido
- [ ] `internal/user/handler.go` — cookie em login/logout/logout-all, `csrfToken`, `Rotate`, `attachments_enabled` em `me`
- [ ] `internal/middleware/csrf.go` — middleware condicional novo
- [ ] `internal/middleware/cors.go` — `Allow-Credentials`, header CSRF
- [ ] `cmd/api/main.go` — `csrf.Protector` construído e wireado
- [ ] `docs/openapi.yaml` — dois `securityScheme`s, `/auth/csrf-token`, `403` novo, `attachments_enabled`
- [ ] `.env.example` + `README.md` — duas variáveis novas
- [ ] `CHANGELOG.md` — entrada `[1.2.0]`
- [x] Issue de `CI-8`: **#130** (criada, fora da numeração #112–#118)

## Riscos e como o plano os cobre

| Risco | Coberto por |
|---|---|
| CSRF acidentalmente exigido de um cliente Bearer (quebra README) | Teste de integração explícito em CI-6, citando os `curl` do README |
| CSRF acidentalmente pulado para um cookie autenticado (reabre a vulnerabilidade original) | Mesmo teste, direção oposta |
| `Rotate` chamado depois de `WriteHeader` (silenciosamente não roda) | `moat/csrf` já detecta isso e retorna `ErrHeadersAlreadySent` — CI-6 checa o erro, não ignora |
| `CSRF_SECRET` divergente entre réplicas (tokens de uma instância rejeitados por outra) | Fora do alcance do código — nota operacional em `.env.example`/README: gerar uma vez, usar em todas as réplicas, nunca por processo |
| Cookie `Secure` quebra dev local sem avisar por quê | `COOKIE_INSECURE` documentado e testado (CI-2); sem ele, o sintoma (`Set-Cookie` ignorado pelo browser em `http://`) fica óbvio no DevTools, não silencioso no servidor |
| `attachments_enabled` fica dessincronizado de `attachmentsEnabled(cfg)` real | Mesma função reaproveitada (CI-8), não duplicada |
