---
slug: dual-auth-mode
stage: context
tier: full
sources_mtime:
  docs/DECISIONS.md: 2026-08-29T13:00:23Z
  docs/ARCHITECTURE.md: 2026-08-29T12:35:31Z
  CLAUDE.md: 2026-08-29T12:35:31Z
  docs/openapi.yaml: 2026-08-29T13:16:05Z
---

# dual-auth-mode — Contexto

## Pedido

Fase 12: a API passa a aceitar credencial por cookie `httpOnly` (caminho
navegador, para o frontend da Fase 13) *além* de `Authorization: Bearer`
(caminho script/serviço, inalterado), na mesma sessão. CSRF (`moat/csrf`) se
aplica somente quando a credencial resolvida veio do cookie. Login também
seta o cookie; logout/logout-all o expiram; CORS ganha
`Access-Control-Allow-Credentials`. Aditivo — `v1.2.0`, sem quebrar contrato
existente. Issues reais: `#112`–`#118`, criadas no GitHub (ver "Perguntas em
aberto" item 1).

## Escopo

**Dentro:**
- Cookie de sessão (`HttpOnly`, `Secure`, `SameSite=Lax`) emitido por
  `POST /v1/auth/login`, expirado por `logout`/`logout-all`.
- Resolução de credencial por cookie OU `Authorization: Bearer` no
  middleware de sessão, com uma regra de precedência explícita quando ambos
  chegam juntos.
- CSRF (`moat/csrf`, já vendorizado) aplicado a métodos mutadores **somente**
  quando a credencial usada veio do cookie.
- `CORS_ALLOWED_ORIGINS` + `Access-Control-Allow-Credentials: true` para a
  origem do frontend.
- `docs/openapi.yaml` com o segundo `securityScheme`, `CHANGELOG.md` `v1.2.0`.

**Fora:**
- Qualquer mudança na *forma* do token em si (continua opaco, `sha256`
  hasheado — nenhuma migração para JWT).
- Remover o caminho Bearer ou torná-lo menos privilegiado — seria breaking
  (`v2.0.0`), fora de escopo aqui.
- CSRF em `POST /auth/login`/`POST /auth/register` (ver "Perguntas em
  aberto" — a proteção clássica de CSRF pressupõe uma sessão já
  autenticada; login CSRF é um risco relacionado mas distinto, não coberto
  pelo rascunho original).

## Superfície de código

| Arquivo | Papel na mudança |
|---|---|
| `internal/user/middleware.go` (`RequireAuth`, `bearerToken`) | precisa resolver cookie *ou* header; hoje só lê `Authorization` |
| `internal/middleware/auth_context.go` | precisa de um novo par `ContextWith*`/`*FromContext` para a origem da credencial (ou, ver achado abaixo, pode não precisar) |
| `internal/middleware/cors.go` (`CORS`) | precisa emitir `Access-Control-Allow-Credentials: true` e incluir o header CSRF em `corsAllowedHeaders` |
| `cmd/api/main.go` (`newServer`, linha ~487 em diante) | monta a cadeia de middleware global; é o composition root — o `csrf.Protector` é construído aqui, como `ratelimit`/`secureheaders` já são |
| `internal/user/handler.go` (`login`, `logout`, `logoutAll`) | precisa setar/expirar o cookie na resposta, sem mudar o corpo JSON |
| `internal/config/config.go` | novo campo `string` (não `secret.Value` — ver invariante) para `CSRF_SECRET`; `internal/config` não importa nada fora da stdlib |
| `github.com/JonasBorgesLM/moat/csrf` (dependência já no `go.sum`? não — não é importada hoje em nenhum `.go` deste repo, apenas vendorizada como pacote-irmão do `moat` no monorepo de módulos locais) | fornece `Protector`, `Middleware`, `Token`, `Rotate` — mecanismo já decidido pela biblioteca, não a inventar |

## Decisões registradas que isto toca

- `docs/DECISIONS.md:10` § "Autenticação: token via header, não cookie de
  sessão" — **revisão parcial, não reversão total.** O texto atual diz "CSRF
  não se aplica a este modelo de auth — não implementar `csrf` do moat aqui"
  e trata cookie como "a alternativa rejeitada". Isso deixa de ser universal:
  passa a valer só para o caminho Bearer. O próprio texto já previa isso —
  "se um frontend futuro guardar o token... decisão do projeto de frontend
  quando existir" (linha 23-25) — só que a decisão real (cookie httpOnly,
  não `localStorage`) é melhor que a hipótese que o texto cogitava.
- `CLAUDE.md` § "Things not to do without being asked" — **conflito literal
  a resolver, não ignorar.** "Don't switch session auth to JWT, **or add a
  second auth mechanism**, without discussing it first." Isto é
  tecnicamente uma segunda *forma de transporte* da mesma credencial opaca,
  não um segundo *mecanismo* de autenticação (token continua sendo o mesmo
  artefato, mesma tabela `sessions`, mesmo `ValidateToken`) — mas o texto diz
  "mechanism" sem essa distinção. Tratado como `DC-1` em `validation.md`.

## Invariantes aplicáveis

- `internal/middleware` não pode ganhar conhecimento de domínio (CLAUDE.md §
  "Cross-domain coupling"). O `csrf.Protector` em si é genérico (não sabe o
  que é um usuário) — pode viver em `internal/middleware` ou ser só
  construído/montado em `cmd/api`. O que **não pode** ir em
  `internal/middleware`: qualquer lógica que saiba o que é um "token de
  sessão" — isso já vive em `user.RequireAuth`, e a extensão para cookie
  deve continuar lá.
- `cmd/api` é o composition root e o único lugar que monta tipos
  cross-package (CLAUDE.md § "Composition root"). O `csrf.Protector` é
  construído em `newServer`, como `ratelimit.New`/`secureheaders.Middleware`
  já são.
- `internal/config` não importa nada fora da stdlib (`.claude/rules/config-env.md`).
  `CSRF_SECRET` entra como `string` simples, igual a `AttachmentS3SecretKey`
  — a conversão para `secret.Value` (o tipo que `csrf.New` exige) acontece em
  `cmd/api`, não em `config`.
- Toda config nova falha no *startup*, nunca em request-time
  (`config-env.md` § "Fail at startup"). `CSRF_SECRET` ausente/curto (< 32
  bytes, `csrf.MinSecretLen`) deve impedir o processo de subir, não degradar
  silenciosamente.
- Nunca logar um `Config` inteiro; `CSRF_SECRET` entra na mesma lista de
  "reportado por nome, nunca por valor" que `AttachmentS3SecretKey` já usa
  (`config.go:660-672`).
- Ordem de middleware documentada em `.claude/rules/go-http-handlers.md`:
  `RequestID → Logging → secureheaders → CORS → Recovery → [globalLimiter]`.
  CSRF precisa de um lugar nessa cadeia — ver achado abaixo, não é trivial.
- `moat/csrf`'s próprio doc comment: "Place a body-size limit before this
  middleware" — a cadeia atual já tem o limite de corpo dentro de cada
  handler (`http.MaxBytesReader`), não como middleware global; isso precisa
  ser conferido especificamente para onde o CSRF for montado.

## Achado que não estava no rascunho: como conciliar "CSRF só no cookie" com o desenho do `moat/csrf`

`csrf.Protector.Middleware` não tem um modo "verificar só se X" — é tudo ou
nada: em método seguro, garante cookie+token; em método mutador, **sempre**
valida Origin+token. Não há uma função pública de verificação separada da
`Middleware` para compor condicionalmente por fora.

O rascunho (#114) presumia resolver isso lendo, depois do `RequireAuth`, um
valor de contexto com "de onde veio a credencial resolvida" — mas isso cria
uma dependência de ordem estranha: `RequireAuth` roda **por rota** (não é
global), então um middleware CSRF que precisasse desse contexto também
teria que ser por rota, um por handler mutador — muito mais cirurgião do que
o resto da cadeia global (`CORS`, `secureheaders` etc.).

**Achado:** não é preciso esperar a autenticação *resolver* para saber a
origem — só é preciso saber qual credencial a requisição **apresenta**,
sintaticamente, antes de validar: `Authorization` presente → caminho Bearer
(pula CSRF); senão, cookie de sessão presente → caminho cookie (aplica
CSRF). Essa é exatamente a mesma pergunta que a precedência de #114 (header
vence, se ambos presentes) já precisa responder — dá para fatorar um único
helper, ex. `credentialSource(r) (source, token string)`, usado tanto por
`RequireAuth` (para saber qual token validar) quanto por um middleware CSRF
que pode então ser **global**, montado na cadeia como os demais, sem
depender de contexto pós-autenticação. Resolve a tensão de ordering sem
inventar um segundo mecanismo — reaproveita a mesma decisão de precedência
em dois lugares.

Isso muda o desenho de #114/#115 do rascunho original: a "origem da
credencial no contexto" deixa de ser estritamente necessária para o CSRF
(ainda pode valer a pena registrá-la, só que para log/observabilidade, não
como dependência do gate de CSRF).

## Contrato afetado

- `POST /v1/auth/login` (`docs/openapi.yaml:150`) — resposta ganha
  `Set-Cookie`, corpo inalterado. Novo header de resposta a documentar.
- `POST /v1/auth/logout` (`:224`), `POST /v1/auth/logout-all` (`:257`) —
  ganham `Set-Cookie` (expirando o cookie).
- Toda rota mutadora (`POST`/`PUT`/`PATCH`/`DELETE` sob `/v1/tasks`,
  `/v1/files`) ganha uma resposta `403` nova, condicional (só ocorre para
  quem autenticou por cookie).
- `components.securitySchemes` (`:1229`) ganha um segundo esquema
  (`cookieAuth` ou nome equivalente) — hoje só existe `bearerAuth`.
- `security:` de nível de operação nas rotas mutadoras precisa listar os
  dois esquemas como alternativas (`- bearerAuth: []` / `- cookieAuth: []`),
  não uma combinação obrigatória.

## Superfície de testes

- `internal/user`: `middleware_test.go` (ou novo arquivo) — cookie
  aceito, header aceito, precedência quando ambos presentes (testada nos
  dois sentidos), cookie inválido/expirado tratado igual a header
  inválido/expirado (401, nunca 500). Reusa o padrão de fake
  `sessionValidator` já usado por `TestRequireAuth_*`.
- `internal/user`: `handler_test.go` — `login` seta cookie com os quatro
  atributos e não muda o corpo; `logout`/`logoutAll` expiram o cookie
  (`Max-Age=0` ou `Expires` no passado) mantendo a invalidação de sessão já
  testada.
- Pacote novo ou `cmd/api`: teste de integração HTTP fim-a-fim provando
  CSRF: cookie sem token → 403; cookie com token → passa; Bearer sem token →
  passa (regressão explícita dos exemplos de `curl` do README). Candidato
  natural: `cmd/api/server_integration_test.go` (já tem
  `TestIntegration_CORS_*`, mesmo padrão de teste HTTP real contra
  `newServer`).
- `internal/middleware/cors_test.go` — `Access-Control-Allow-Credentials:
  true` presente quando a origem é permitida, ausente quando não é; nunca
  junto de `Access-Control-Allow-Origin: *` (que este código já não emite,
  mas vale um teste que trava isso).
- `internal/config/config_test.go` — `CSRF_SECRET` ausente ou curto demais
  falha `Load` (mesmo padrão de `TestLoad_Invalid*`).

## Artefatos que precisam mudar junto

- [x] `docs/openapi.yaml` — segundo `securityScheme`, `403` novo nas rotas
      mutadoras, `Set-Cookie` documentado em login/logout
- [ ] `README.md` — provavelmente não muda (os exemplos de `curl` continuam
      válidos como estão); avaliar se vale uma nota "o frontend usa cookie,
      veja `web/README.md`"
- [x] `CLAUDE.md` — a linha "don't add a second auth mechanism" precisa de
      uma ressalva apontando para a nova seção de `docs/DECISIONS.md`
- [x] `docs/ARCHITECTURE.md` — § "Authentication: opaque bearer session
      tokens, not JWT" ganha um parágrafo sobre o segundo transporte
- [ ] `.env.example` — `CSRF_SECRET` (obrigatório, sem default — como
      `ATTACHMENT_S3_SECRET_KEY` quando S3 está em uso, só que este é
      **sempre** obrigatório, não condicional) e o toggle de cookie
      inseguro em dev
- [x] `CHANGELOG.md` — entrada `[1.2.0]`
- [ ] migração de banco — **não**: nenhum schema muda, o cookie carrega o
      mesmo token que já é validado contra `sessions.token_hash`
- [ ] `k8s/30-config.yaml` / Secret — `CSRF_SECRET` é sensível, não pertence
      ao `ConfigMap` que hoje carrega `CRIER_OTLP_ENDPOINT` etc.; precisa ir
      no mesmo lugar que credenciais de banco/S3 já vão (avaliar no plano)

## Já é um item diferido?

Não. `docs/ARCHITECTURE.md`'s "Future Improvements" não lista cookie auth,
CSRF ou modo duplo — a única entrada próxima é "BFF layer", que é uma coisa
diferente (um serviço intermediário), não citada nem necessária para isto.

## Perguntas em aberto

1. ~~As issues #112–#118 não existem no GitHub.~~ **Resolvido:** criadas
   via `create-fase12-13-frontend.sh`, confirmadas via `gh issue list
   --label fase-12`/`--label fase-13` (19 issues, `#112`–`#130`, esta
   última via `open-attachments-enabled-issue.sh` — ver `validation.md`
   § "Nota sobre numeração de issues"). Numeração real já usada em todo
   este documento e nos demais artefatos.
2. CSRF em `POST /auth/login`/`register` (proteção contra "login CSRF") —
   deliberadamente fora de escopo pelo rascunho original; registrar a
   decisão de deixar de fora (e por quê) ou trazer para dentro?
3. Mecanismo exato de entrega do token CSRF ao frontend — a `Protector` não
   expõe o valor lido do cookie para JS (o cookie é `HttpOnly` por design).
   Candidatos: (a) endpoint dedicado que chama `csrf.Token(r)` e devolve no
   corpo; (b) devolver o token no corpo de `GET /v1/auth/me` (já
   autenticado, já teria passado pela `Middleware` se ela for global); (c)
   um header de resposta em toda resposta GET. Precisa de uma decisão.
4. Toggle de cookie inseguro em desenvolvimento — nome da variável de
   ambiente, e se ela também relaxa `SameSite` (não deveria — só `Secure`).
5. `Protector.Rotate` deve ser chamado em login (o próprio pacote recomenda:
   "call this at every privilege change") — isso é uma chamada adicional em
   `user.Handler.login`, ortogonal à emissão do cookie de sessão em si (são
   dois cookies diferentes: o de sessão e o `__Host-moat.csrf`). O rascunho
   não menciona `Rotate` — precisa entrar em #115 ou #113?
