---
slug: frontend-redesign
stage: context
tier: full
sources_mtime:
  docs/DECISIONS.md: 2026-09-02T00:20:34Z
  docs/ARCHITECTURE.md: 2026-09-02T00:20:34Z
  CLAUDE.md: 2026-09-02T00:20:34Z
  docs/openapi.yaml: 2026-09-02T00:20:34Z
---

# frontend-redesign — Contexto

## Pedido

O usuário testou a aplicação pessoalmente (stack `docker compose` real, dados
seedados via `cmd/seed`) e considerou a interface "bem simples" demais. Pediu
um design mais atual, focado em usabilidade, no espírito de um Jira mais
simples — não um clone literal, mas a sensação de ferramenta de produtividade
madura: hierarquia visual clara, densidade de informação equilibrada, e mais
recursos de navegação/filtragem do que existem hoje. Pediu explicitamente para
registrar os pontos de melhoria como uma nova fase (Fase 14), com issues no
GitHub, no mesmo padrão da Fase 13.

Este documento **não implementa nada** — é o primeiro estágio do pipeline
(`/change-context`) que a Fase 13 já usou, aplicado agora a esta mudança.

## Escopo

**Dentro:**
- Redesenho visual das telas existentes (`/register`, `/login`, lista de
  tasks, modais de criar/editar/excluir, seção de anexos) dentro da SPA já
  existente — sem trocar framework, sem SSR, sem sair do monorepo `web/`
  (essas já são decisões registradas, ver seção abaixo).
- Extensão do sistema de design tokens (`web/src/design/tokens.css`) com o
  que faltar (cores de status/prioridade, por exemplo) — não substituição.
- Um componente de app-shell/header/navegação, hoje inexistente
  (`AuthenticatedHome` em `App.tsx` é só um parágrafo + dois botões de
  logout + a lista).
- Avaliar, como decisão explícita desta fase (não assumida em silêncio), se
  filtro/busca/ordenação de tasks entra neste momento — item já registrado
  como deliberadamente adiado em `docs/ARCHITECTURE.md` § Future
  Improvements ("Task filtering and search"), não uma ideia nova.

**Fora:**
- Qualquer mudança de contrato que não seja a extensão de filtro/busca acima
  decidida explicitamente — nenhuma outra rota nova, nenhum campo novo em
  `Task` (labels, assignees, due dates) sem decisão própria.
- Reescrever os testes de CI-3–CI-11 do zero — a auditoria de acessibilidade
  (CI-10) e a operabilidade só-por-teclado (CI-7/CI-10/CI-11) são invariantes
  que o redesenho tem que preservar, não repensar.
- Board/kanban com drag-and-drop — mencionado como possível "sensação de
  Jira", mas fora de escopo desta fase a menos que vire um CI explícito no
  plano (ver "Perguntas em aberto").

## Superfície de código

| Arquivo | Papel na mudança |
|---|---|
| `web/src/design/tokens.css` | Sistema de tokens (cor/tipografia/spacing/radius) — todo token novo (cor de status, por exemplo) entra aqui, testado por contraste como os existentes |
| `web/src/design/tokens.test.ts` | Padrão de teste de contraste a seguir para qualquer token de cor novo |
| `web/src/App.tsx` | `AuthenticatedHome` — hoje só texto + botões; é onde um app-shell/header entraria |
| `web/src/features/tasks/TaskList.tsx` | Tela principal — maior parte dos achados da auditoria visual se concentra aqui |
| `web/src/features/tasks/TaskItem.tsx` | Uma linha de task — grupos de botão (status/ações/anexos) sem separação visual, badges sem cor |
| `web/src/features/tasks/TaskList.module.css`, `TaskItem.module.css` | CSS modules a restilizar |
| `web/src/features/auth/{LoginPage,RegisterPage}.tsx` | Telas sem card/branding, `<h1>` sem estilo |
| `web/src/components/{Button,TextField,Select,Checkbox,Modal,Toast,Skeleton}.tsx` | Os sete primitives existentes (CI-5) — só componentes reutilizáveis hoje; Select nativo hoje destoa visualmente de TextField |
| `docs/openapi.yaml` `GET /v1/tasks` (linha ~488) | Contrato atual: só `limit`/`offset`, ordenado por `created_at` — sem `status`/`priority`/`q`. Precisa mudar **somente se** a decisão de filtro/busca entrar nesta fase |

## Decisões registradas que isto toca

### D1 — Frontend: Vite (SPA), monorepo em `web/`, mesma linha de versão do repo (`docs/DECISIONS.md:1119`)
- **Decidido:** SPA pura (Vite + React + TS), monorepo, versionamento compartilhado, cookie httpOnly nunca `localStorage`, `react-router-dom` com URLs reais.
- **Por quê:** um único cliente, um único recurso central; BFF/SSR resolveriam um problema que não existe ainda.
- **Restringe:** o redesenho não pode introduzir SSR, um framework diferente, um repositório separado, ou qualquer persistência de credencial fora do cookie httpOnly + token CSRF em memória.
- **Trade-off:** _não registrado_ (decisão sem alternativa rejeitada explicitada além do BFF).

## Veredito

- **Contradiz alguma decisão?** não — nenhuma decisão registrada impede um redesenho visual, nem a extensão do sistema de tokens, nem a adição de um componente de navegação.

## Invariantes aplicáveis

Não há `.claude/rules/*.md` específico para `web/` (todas as regras hoje são
Go-backend: `go-layering`, `go-domain-errors`, `go-http-handlers`,
`go-repository-parity`, `go-tests`, `sql-migrations`, `config-env`,
`api-contract`, `attachment-storage`, `k8s-deploy`). Os invariantes que se
aplicam vêm de `docs/changes/web-frontend/plan.md` em si (o mesmo tipo de
"regra recém-criada" que `CLAUDE.md` documenta para o backend):

### Bloqueantes — violar isto é bug, não estilo
- [ ] Nenhum componente visual chama `fetch` diretamente — só `src/api/` (verificado por grep no CI de `web/`)
- [ ] `localStorage`/`sessionStorage` nunca guardam credencial — cookie httpOnly (backend) + token CSRF em memória (frontend)
- [ ] Todo `.module.css` novo ou alterado usa só `var(--token-*)` — sem cor/spacing literal (verificado por `assertOnlyTokens`, `web/src/test-utils/assertOnlyTokens.ts`)
- [ ] Os quatro estados de UI explícitos (loading/empty/error/sucesso) continuam explícitos em qualquer tela redesenhada — não um spinner genérico substituindo os quatro
- [ ] Todo fluxo continua completável só por teclado, com foco visível (`:focus-visible`) — invariante testado de verdade em CI-10/CI-11, não presumido

### Precisam mudar junto
- [ ] `web/src/design/tokens.test.ts` — todo token de cor novo (status/prioridade) precisa de um teste de contraste WCAG AA próprio, mesmo padrão dos existentes
- [ ] `CHANGELOG.md` — uma entrada nova quando esta fase for lançada (mesmo padrão da entrada de frontend já criada para 1.2.0)
- [ ] `docs/openapi.yaml` + `internal/task/handler.go`/`service.go` — **somente se** filtro/busca de `GET /tasks` entrar nesta fase

### Proibido sem perguntar antes
- [ ] Mudar o contrato de `GET /tasks` (novos query params) sem decisão explícita registrada — mesmo espírito de `CLAUDE.md` § "Things not to do without being asked", aplicado aqui porque filtro/busca é item já adiado deliberadamente, não uma escolha óbvia
- [ ] Adicionar campos novos ao schema `Task` (labels, assignees, due dates) para "parecer mais com Jira" sem que isso seja uma decisão própria, registrada — risco real dado o pedido do usuário citar Jira como referência

## Contrato afetado

`GET /v1/tasks` (`docs/openapi.yaml:488` em diante) — **condicionalmente**.
Hoje aceita só `limit`/`offset`, retorna sempre ordenado por `created_at`
ascendente. Se esta fase decidir implementar filtro/busca (ver "Perguntas em
aberto"), o contrato ganha novos parâmetros de query (`status`, `priority`,
possivelmente `q` para busca por título) — mudança aditiva (novos parâmetros
opcionais), não deveria quebrar nada existente, mas ainda é mudança de
contrato real: precisa de entrada em `docs/openapi.yaml`, handler/service no
Go, e é trabalho de backend, não só frontend.

Se a decisão for **não** filtrar/buscar nesta fase (só reorganizar
visualmente o que já existe, por exemplo com agrupamento client-side por
status), _nenhuma mudança de contrato_.

## Superfície de testes

- `web/src/design/tokens.test.ts` — onde um teste de contraste para um token
  de cor novo entra; segue o padrão `describe`/`it` por par texto/fundo já
  usado pelos tokens existentes.
- `web/src/test-utils/assertOnlyTokens.ts` — reutilizado (não recriado) por
  todo `.module.css` novo ou alterado, mesmo padrão de todo componente desde
  CI-5.
- `web/src/components/*.test.tsx`, `web/src/features/**/*.test.tsx` — testes
  unitários existentes que **não podem regredir**: qualquer restilização de
  `TaskItem`/`TaskList`/`LoginPage`/`RegisterPage` roda a suíte existente
  desses arquivos, mesmo padrão de "ver rodando" já exigido no resto do
  projeto.
- `web/e2e/*.spec.ts` — a suíte E2E real (CI-11) cobre os fluxos completos
  contra `docker compose`; um redesenho que muda `getByRole`/`getByLabel`
  acessíveis de qualquer elemento quebra esses specs — sinal cedo de uma
  restilização que também mudou semântica, não só aparência.
- Se filtro/busca entrar: `internal/task/handler_test.go` e
  `internal/task/service_test.go` (padrão table-driven já usado no pacote,
  ver `fakeRepository`) mais o par unit/integration de
  `postgres_repository_test.go` (`//go:build integration`) para os novos
  parâmetros de query.

## Artefatos que precisam mudar junto

- [ ] `docs/openapi.yaml` — só se filtro/busca entrar nesta fase
- [ ] `README.md` — n/a (frontend já mencionado desde a Fase 13)
- [ ] `CLAUDE.md` — n/a, a menos que um invariante novo surja do plano (por
      exemplo, uma regra sobre onde tokens de cor semântica por status
      vivem)
- [ ] `docs/ARCHITECTURE.md` — remover "Task filtering and search" de Future
      Improvements **se e quando** for implementado nesta fase
- [ ] `.env.example` — n/a
- [ ] `CHANGELOG.md` — sim, entrada nova no release em que esta fase for
      lançada
- [ ] migração `NNNN_*.{up,down}.sql` + `migrate_test.go` — n/a (nenhum
      campo novo de schema previsto; filtro por `status`/`priority` usa
      colunas que já existem)

## Já é um item diferido?

**Sim, parcialmente.** `docs/ARCHITECTURE.md` § Future Improvements já
registra: _"Task filtering and search — filter `GET /tasks` by
`status`/`priority`, or search by title (the natural point to also add
matching indexes)."_ Isso cobre exatamente o pedaço de "recursos de
navegação/filtragem" do pedido do usuário — não é uma ideia nova, é decidir
se **esta** fase é o momento de finalmente fazer, ou se o redesenho fica
só com o que é possível derivar client-side da lista já existente
(agrupamento visual por status, por exemplo, sem filtro/busca de verdade no
servidor). O resto do pedido (hierarquia visual, cor, app-shell, densidade)
não tem item equivalente registrado — é território novo desta fase.

## Perguntas em aberto

- Filtro/busca/ordenação de `GET /tasks` entra nesta fase (muda o contrato,
  é trabalho de backend + frontend), ou fica fora e o redesenho se limita ao
  que é derivável client-side da lista já carregada?
- Um board/kanban agrupado por status (sensação mais forte de "Jira") é
  meta desta fase, ou fica para uma fase seguinte depois que a base visual
  (cor, hierarquia, app-shell) estiver pronta?
- Cores de status/prioridade novas em `tokens.css`: o usuário tem uma
  paleta/referência de marca em mente, ou a escolha fica com quem
  implementar (sujeita ao mesmo teste de contraste WCAG AA de CI-3)?
- O app-shell/header novo carrega algo além de navegação (busca global,
  avatar/iniciais do usuário, seletor de tema)? Undecided, afeta o escopo de
  componente novo necessário.
