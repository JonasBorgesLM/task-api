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
    resolved_by: "usuário decidiu: não nesta fase. Fica só organização/agrupamento client-side do que já é carregado — sem novo parâmetro de contrato. AD-1 (item do ARCHITECTURE.md) permanece adiado, não fechado por esta fase."
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

**status: clean** — 0 questões abertas. `AM-1`/`AM-2` fecharam por decisão
explícita do usuário (ambas "não" — fase fica puramente frontend, sem board);
`AM-3`/`AM-4` fecharam por recomendação padrão (reversível, sinalizada como
tal). Próximo: `/change-plan frontend-redesign`.

## Checagens sem achados

- Conflito de decisão (`DC-N`) — nenhum. Nenhuma decisão registrada em
  `docs/DECISIONS.md` proíbe redesenho visual, extensão de tokens, ou um
  componente de navegação novo (ver `context.md`'s D1).
- Conflito de invariante (`IV-N`) — nenhum. Sem violação de camadas, sem
  dependência nova, sem branch de PostgreSQL vazando para `Service`/handler —
  nada disso está em jogo ainda nesta etapa de planejamento.
- Cobertura de contrato (`CG-N`) — condicional a AM-1; não avaliável até a
  resposta (ver Questões abertas).
- Paridade de `Repository`/`BlobStore` (`PG-N`) — n/a, nenhuma mudança de
  `Repository` decidida ainda.
- Cobertura de teste (`TG-N`) — coberta em `context.md` § "Superfície de
  testes" para ambas as leituras de AM-1; nada faltando a apontar aqui.
- Drift de documentação (`DD-N`) — nenhum achado novo além do que
  `context.md` já condiciona corretamente a AM-1 (`docs/openapi.yaml`,
  remoção do bullet em `docs/ARCHITECTURE.md`).

## Questões resolvidas

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
point to also add matching indexes)."_ Advisório, não afeta o veredito — mas
o plano final precisa dizer explicitamente se esta fase fecha esse item
(removendo o bullet) ou permanece adjacente a ele sem fechá-lo, dependendo da
resposta a AM-1.
