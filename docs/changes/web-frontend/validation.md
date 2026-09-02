---
slug: web-frontend
stage: validation
status: clean
open_count: 0
issues:
  - id: AM-4
    status: resolved
    blocking: false
    summary: "Estratégia de paginação da listagem de tasks não decidida (GET /tasks sem contagem total)"
    resolved_by: "não perguntada nas quatro pendências originais — decisão tomada agora por recomendação: infinite scroll com a técnica do item extra (pedir limit+1, descartar o extra, usar a sobra como sinal de 'há mais'). Evita pedir contagem ao backend (mudança de contrato maior) e evita paginação numerada mentirosa. Reversível — sinalizado como escolha padrão, não confirmação explícita do usuário."
  - id: AM-5
    status: resolved
    blocking: false
    summary: "Tratamento do 409 ambíguo de PATCH /status — a própria issue #122 pede decisão explícita, não silenciosa"
    resolved_by: "não perguntada nas quatro pendências originais — decisão tomada agora: match de string no corpo do erro (frágil, mas describe funciona hoje e o texto das duas mensagens já é estável o suficiente para distinguir — ver docs/openapi.yaml:743-750) MAIS uma issue de débito técnico aberta pedindo um código de erro legível por máquina no envelope, registrada como pendência conhecida (mesmo padrão que o CHANGELOG.md já usa para débitos como o do #107 antes de ser corrigido), não deixada para trás em silêncio."
  - id: AM-1
    status: resolved
    blocking: true
    summary: "Esquema de versionamento do frontend não decidido (analogia com crier não se aplica)"
    resolved_by: "usuário decidiu: linha compartilhada com o repo, sem tag própria para web/. Reconsiderar só se surgir necessidade real de lançar o frontend sem cortar release de API — não antecipar isso agora."
  - id: AM-2
    status: resolved
    blocking: true
    summary: "Feature-detection de anexos habilitados exige mudança de contrato — não é decisão só do frontend"
    resolved_by: "usuário decidiu: attachments_enabled (boolean) em GET /v1/auth/me — vira CI-8 de dual-auth-mode/plan.md, issue própria #130 (fora da numeração #112-#118 original)."
  - id: AM-3
    status: resolved
    blocking: false
    summary: "Ferramenta de geração de tipos TS a partir do openapi.yaml não escolhida"
    resolved_by: "não respondido explicitamente pelo usuário; adotada a recomendação já registrada em context.md (openapi-typescript — zero runtime, só tipos) por ser de baixo risco e reversível. Sinalizado como escolha padrão, não confirmação explícita."
  - id: TG-1
    status: resolved
    blocking: false
    summary: "Nenhuma infraestrutura de teste (unit/E2E) existe ainda — esperado nesta fase"
    resolved_by: "vira escopo de CI-2 (scaffold + Vitest) e do item de E2E (Playwright como devDependency real) em plan.md."
  - id: DD-1
    status: resolved
    blocking: false
    summary: "docs/DECISIONS.md, README.md, CLAUDE.md, ARCHITECTURE.md ainda não têm as decisões de frontend registradas"
    resolved_by: "primeiro CI do plano (CI-1), agora que AM-1 (o único bloqueio real para escrever a seção) está decidido."
sources_mtime:
  docs/changes/web-frontend/context.md: 2026-08-29T00:00:00Z
  docs/DECISIONS.md: 2026-08-29T13:00:23Z
  CLAUDE.md: 2026-08-29T12:35:31Z
---

# web-frontend — Validação

## Veredito

**status: clean** — 0 questões abertas. Das sete questões, `AM-1`/`AM-2`
fecharam por decisão explícita do usuário; `AM-3`, `AM-4` e `AM-5` fecharam
por recomendação padrão (reversível, sinalizada como tal — não confirmação
explícita); `TG-1`/`DD-1` foram absorvidos pela estrutura do plano.

**Continua condicional, não pelo veredito, mas pela ordem:** `plan.md`
pode ser escrito agora (é isso que este documento habilita), mas nenhum
`CI` de código real começa antes de `dual-auth-mode` estar implementado e
mergeado — o cookie httpOnly é o mecanismo de sessão que todo o resto
assume, e `attachments_enabled` (CI-8 de lá) é uma dependência direta do
CI de anexos aqui. Isso é ordem de execução, já combinada, não uma
pendência de validação.

## Questões resolvidas

Ver frontmatter — todas as sete, com `resolved_by`. Cinco vieram da rodada
anterior; `AM-4` e `AM-5` foram encontradas só ao montar `plan.md` (a
própria issue #122 do rascunho já pedia explicitamente "não escolher em
silêncio" para o `409` — decisão tomada aqui em vez de deixada implícita
no plano).

## Checagens sem achados (reconfirmadas)

- Conflito de decisão (`DC-N`) — nenhum específico a esta mudança (o único
  `DC` do par de mudanças, o de `CLAUDE.md`, pertence a `dual-auth-mode` e
  já está resolvido lá).
- Paridade `Repository`/`BlobStore` — n/a.

## Nota sobre numeração de issues

Mesma confirmação de `docs/changes/dual-auth-mode/validation.md`: as 11
issues desta fase (`#119`–`#129`) existem de verdade no GitHub, criadas via
`create-fase12-13-frontend.sh` e confirmadas via `gh issue list --label
fase-13`. `plan.md` já usa os números reais.
