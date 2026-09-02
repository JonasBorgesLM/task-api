---
slug: dual-auth-mode
stage: validation
status: clean
open_count: 0
issues:
  - id: DC-1
    status: resolved
    blocking: true
    summary: "CLAUDE.md proíbe 'adicionar um segundo mecanismo de auth' sem discutir antes"
    resolved_by: "usuário confirmou: mesmo token opaco, dois transportes, não um segundo mecanismo. CLAUDE.md ganha uma ressalva na linha existente apontando para a nova seção de DECISIONS.md, sem reescrever o texto original — vira CI-1."
  - id: AM-1
    status: resolved
    blocking: true
    summary: "Mecanismo de entrega do token CSRF ao frontend não decidido"
    resolved_by: "usuário decidiu: endpoint dedicado GET /v1/auth/csrf-token, {\"csrf_token\": \"...\"} via csrf.Token(r), chamável sem sessão. Frontend guarda em memória, nunca em storage persistente — vira CI-5."
  - id: AM-2
    status: resolved
    blocking: true
    summary: "CSRF em login/register (login-CSRF) — dentro ou fora de escopo?"
    resolved_by: "usuário decidiu: proteger, não aceitar o risco. login/register passam a exigir o mesmo token CSRF (pré-sessão, da CI-5) que rotas autenticadas por cookie exigem — vira CI-6. Isso simplifica o gate: a regra deixa de ser 'cookie presente' e passa a ser 'Authorization ausente', que cobre login/register e cookie autenticado com a mesma checagem — ver plan.md § CI-6."
  - id: AM-3
    status: resolved
    blocking: false
    summary: "Toggle de cookie inseguro em dev — nome de variável e escopo não decididos"
    resolved_by: "não respondido explicitamente pelo usuário; adotada a recomendação já registrada em context.md por ser de baixo risco e reversível: COOKIE_INSECURE (default false), aplicado aos dois cookies (sessão e CSRF) juntos. Sinalizado aqui como escolha padrão, não como confirmação explícita — reversível em CI-2 sem re-planejar."
  - id: TG-1
    status: resolved
    blocking: false
    summary: "Testes CSRF/cookie/precedência ainda não existem — ver Superfície de testes"
    resolved_by: "já mapeados em context.md § Superfície de testes; cada um vira a linha Testes: do CI correspondente em plan.md."
  - id: DD-1
    status: resolved
    blocking: false
    summary: "CLAUDE.md, ARCHITECTURE.md, .env.example, k8s manifests não atualizados ainda"
    resolved_by: "cada artefato vira seu próprio CI (ou parte de um) em plan.md, na posição que as regras de ordenação do change-plan exigem (openapi.yaml junto do handler, CHANGELOG.md por último)."
sources_mtime:
  docs/changes/dual-auth-mode/context.md: 2026-08-29T00:00:00Z
  docs/DECISIONS.md: 2026-08-29T13:00:23Z
  CLAUDE.md: 2026-08-29T12:35:31Z
---

# dual-auth-mode — Validação

## Veredito

**status: clean** — 0 questões abertas. As seis questões da rodada anterior
foram todas fechadas por decisão do usuário (DC-1, AM-1, AM-2) ou por
resolução mecânica (AM-3 como escolha padrão reversível; TG-1/DD-1
absorvidos pela estrutura do plano).

## Achado que a decisão do usuário simplificou (registrado aqui, não em context.md, porque só existe depois da resolução de AM-2)

A decisão de proteger login/register com CSRF elimina a necessidade do
gate depender de "a credencial resolvida veio do cookie" (o problema de
ordering que `context.md` já apontava). A regra unificada passa a ser:

> **`Authorization: Bearer` presente → pula CSRF (caminho serviço/script).
> Ausente → aplica CSRF em todo método mutador, autenticado ou não** (cobre
> cookie autenticado, login e register com a mesma checagem).

Isso é estritamente mais simples do que o desenho original de #114/#115: a
checagem passa a ser sintática (qual header está presente), não depende de
nenhuma resolução de sessão prévia, e pode ser um único middleware global,
montado uma vez, sem tratamento especial por rota.

## Questões resolvidas

Ver frontmatter — todas as seis, com `resolved_by`.

## Checagens sem achados (reconfirmadas)

- Paridade de `Repository`/`BlobStore` — n/a.
- Camada de domínio (`internal/task`, `internal/attachment`) — nenhum
  arquivo desses pacotes muda.

## Escopo novo, fora da numeração original (#112–#118)

A decisão sobre `attachments_enabled` (ver
`docs/changes/web-frontend/validation.md` AM-2) exige uma mudança pequena
em `GET /v1/auth/me`, que não corresponde a nenhuma das issues #112–#118
originais. Está no plano como **CI-8**, e agora tem issue própria: **#130**
("[backend] Expor attachments_enabled em GET /v1/auth/me", labels
`fase-12,fase-13`).

## Nota sobre numeração de issues

Todas as 19 issues (`#112`–`#130`) foram criadas de verdade no GitHub —
`create-fase12-13-frontend.sh` (labels + `#112`–`#129`) e
`open-attachments-enabled-issue.sh` (`#130`) rodados e confirmados via
`gh issue list --label fase-12`/`--label fase-13` (o `gh` CLI trata
`--label a,b` como E, não OU — a checagem precisa de duas chamadas
separadas, união, para não subcontar). Números reais já substituíram a
numeração de rascunho em todo este documento e em `plan.md`.
