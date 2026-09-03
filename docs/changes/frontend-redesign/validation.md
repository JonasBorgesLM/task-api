---
slug: frontend-redesign
stage: validation
status: clean
open_count: 0
issues:
  - id: AM-1
    status: resolved
    blocking: true
    summary: "Filtro/busca de GET /tasks entra nesta fase (backend+frontend) ou fica só client-side?"
    resolved_by: "REVERTIDA depois de CI-1–CI-13 implementados: usuário pediu filtros de verdade ('gostaria de ter filtros também para organizar as tasks'), depois de testar a versão client-side-only na prática. Decisão original ('não nesta fase') fica só como histórico abaixo. Nova decisão: filtro por status/priority entra como CI-14 (backend, internal/task) + CI-15 (frontend) — escopo estruturado (query params status/priority em GET /v1/tasks), não busca livre por título, que continua fora (AD-1 fecha parcialmente: a metade filtro fecha, a metade busca por título permanece adiada)."
  - id: AM-2
    status: resolved
    blocking: true
    summary: "Board/kanban agrupado por status é meta desta fase ou de uma fase seguinte?"
    resolved_by: "usuário decidiu: não nesta fase. Foco fica em hierarquia visual, cor, densidade e app-shell na lista já existente — board vira uma fase futura, não parte desta."
  - id: AM-3
    status: resolved
    blocking: false
    summary: "Paleta de cores de status/prioridade — usuário tem referência ou fica com quem implementa?"
    resolved_by: "recomendação padrão, reversível: paleta proposta no plano (CI de tokens), testada por contraste WCAG AA como os tokens existentes — não confirmação explícita do usuário."
  - id: AM-4
    status: resolved
    blocking: false
    summary: "Escopo do app-shell/header novo — só navegação, ou também busca global/avatar/tema?"
    resolved_by: "recomendação padrão, reversível: v1 mínima (nome do app, indicação de view atual, menu do usuário com logout) — busca global/avatar/tema ficam fora até haver pedido concreto, mesmo espírito de 'não decisão por antecipação' de CLAUDE.md."
  - id: AD-1
    status: open
    blocking: false
    summary: "Filtro/busca de tasks já é item deliberadamente adiado em ARCHITECTURE.md § Future Improvements"
sources_mtime:
  docs/changes/frontend-redesign/context.md: 2026-09-02T00:35:00Z
  docs/DECISIONS.md: 2026-09-02T00:20:34Z
  CLAUDE.md: 2026-09-02T00:20:34Z
---

# frontend-redesign — Validação

## Veredito

**status: clean** — 0 questões abertas. `AM-2` fechou por decisão explícita
do usuário ("não" a board/kanban nesta fase); `AM-3`/`AM-4` fecharam por
recomendação padrão (reversível, sinalizada como tal). `AM-1` fechou
originalmente como "não" e foi **reaberta e refechada** depois de
`CI-1`–`CI-13` implementados e testados na prática — usuário pediu filtro de
verdade, que entra como `CI-14`/`CI-15` (ver seção própria abaixo). Próximo:
`/change-plan frontend-redesign` (replanejamento incremental — os `CI`s já
implementados/planejados não mudam).

## Checagens sem achados

- Conflito de decisão (`DC-N`) — nenhum. Nenhuma decisão registrada em
  `docs/DECISIONS.md` proíbe redesenho visual, extensão de tokens, ou um
  componente de navegação novo (ver `context.md`'s D1).
- Conflito de invariante (`IV-N`) — nenhum. Sem violação de camadas, sem
  dependência nova, sem branch de PostgreSQL vazando para `Service`/handler —
  nada disso está em jogo ainda nesta etapa de planejamento.
- Cobertura de contrato (`CG-N`) — `CI-14` estende `docs/openapi.yaml`'s
  `GET /v1/tasks` (dois `parameters` opcionais, `400` com mais uma causa) na
  mesma mudança que o handler, por `.claude/rules/api-contract.md` —
  planejado no `plan.md`, sem achado pendente aqui.
- Paridade de `Repository`/`BlobStore` (`PG-N`) — n/a, nenhuma mudança de
  `Repository` decidida ainda.
- Cobertura de teste (`TG-N`) — coberta em `context.md` § "Superfície de
  testes" para ambas as leituras de AM-1; nada faltando a apontar aqui.
- Drift de documentação (`DD-N`) — nenhum achado novo além do que
  `context.md` já condiciona corretamente a AM-1 (`docs/openapi.yaml`,
  remoção do bullet em `docs/ARCHITECTURE.md`).

## Questões resolvidas

### AM-1 — Filtro/busca de GET /tasks (reaberta e refechada)
- **Decisão original:** não nesta fase. Ficaria só organização/agrupamento
  client-side do que já é carregado — sem novo parâmetro de contrato.
- **Reaberta:** depois de `CI-1`–`CI-13` implementados e testados na
  prática, usuário pediu filtro de verdade: "gostaria de ter filtros
  também para organizar as tasks".
- **Fechada de novo por:** decisão explícita do usuário — filtro por
  `status`/`priority` entra como `CI-14` (backend) + `CI-15` (frontend).
  Escopo estruturado (dois query params), não busca livre por título —
  isso mantém `AD-1` parcialmente aberto (ver abaixo), em vez de resolver
  as duas coisas de uma vez sem terem sido pedidas juntas.

### AM-3 — Paleta de cores de status/prioridade
- **Fechada por:** recomendação padrão, reversível — proposta concreta entra
  no plano como um `CI` de tokens, testada por contraste WCAG AA (mesmo
  padrão de `tokens.test.ts`), não confirmação explícita do usuário.

### AM-4 — Escopo do app-shell/header
- **Fechada por:** recomendação padrão, reversível — v1 mínima (nome do app,
  indicação de view atual, menu do usuário com logout); busca
  global/avatar/tema ficam fora até um pedido concreto existir, mesmo
  espírito de "sem abstração por antecipação" de `CLAUDE.md`.

## Item já deliberadamente adiado

### AD-1 — Filtro e busca de tasks
`docs/ARCHITECTURE.md` § Future Improvements: _"Task filtering and search —
filter `GET /tasks` by `status`/`priority`, or search by title (the natural
point to also add matching indexes)."_ **Fecha parcialmente** com
`CI-14`/`CI-15` desta fase: a metade "filter by status/priority" é
implementada; a metade "search by title" permanece adiada — ninguém pediu
busca por texto ainda, e adicioná-la sem pedido seria antecipação
especulativa (`CLAUDE.md` § "No speculative abstraction"). O bullet em
`docs/ARCHITECTURE.md` deve ser reescrito para refletir só a metade que
continua pendente, não removido inteiro.
