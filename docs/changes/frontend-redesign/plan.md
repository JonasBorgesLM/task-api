---
slug: frontend-redesign
stage: plan
tier: full
items: 9
sources_mtime:
  docs/changes/frontend-redesign/context.md: 2026-09-02T00:35:00Z
  docs/changes/frontend-redesign/validation.md: 2026-09-02T00:45:00Z
---

# frontend-redesign — Plano

## Objetivo

Depois desta mudança: a mesma SPA (mesmas rotas, mesmo contrato de API, zero
mudança em `docs/openapi.yaml`) lê como uma ferramenta de produtividade
madura em vez de uma página de teste — status e prioridade são reconhecíveis
por cor à distância, a ação destrutiva "Delete" não compete visualmente com
as ações comuns, existe um cabeçalho persistente que dá identidade e
wayfinding ao app, os formulários de autenticação têm um cartão real em vez
de inputs soltos, e a auditoria de acessibilidade (axe-core) e a suíte E2E de
CI-10/CI-11 continuam 100% passando contra as telas redesenhadas — não é uma
reescrita, é um segundo passe visual sobre o que CI-3–CI-9 da Fase 13 já
produziram.

Explicitamente fora desta fase (decisão do usuário, `validation.md` AM-1/
AM-2): filtro/busca real de `GET /tasks` e uma visualização em board/kanban.
Ambos ficam candidatos a uma fase futura.

## Restrições herdadas

- Nenhuma mudança em `docs/openapi.yaml` — todo `CI` desta fase é
  estritamente frontend; um `CI` que precisasse tocar o contrato estaria
  fora de escopo (ver `validation.md`).
- `localStorage`/`sessionStorage` nunca guardam credencial — invariante do
  frontend inteiro, não deste redesenho especificamente, mas nenhum `CI`
  aqui tem motivo para tocar `api/client.ts`.
- Todo `.module.css` novo ou alterado usa só `var(--token-*)` — verificado
  por `assertOnlyTokens` (`web/src/test-utils/assertOnlyTokens.ts`), mesmo
  padrão de todo componente desde CI-5 da Fase 13.
- Os quatro estados de UI explícitos (loading/empty/error/sucesso)
  continuam explícitos em toda tela tocada — restilizar não é substituir por
  um spinner genérico.
- Todo fluxo continua completável só por teclado, com foco visível
  (`:focus-visible`) — CI-9 desta fase reverifica isso por execução real
  (mesmo padrão de CI-10/CI-11 da Fase 13), não por inspeção.
- Todo token de cor novo passa pelo mesmo teste de contraste WCAG AA que os
  tokens existentes de `tokens.css` já têm (`tokens.test.ts`).

## Itens

### CI-1 — Tokens de cor semântica: status e prioridade

- **Arquivos:**
  - `web/src/design/tokens.css` — sete categorias semânticas novas:
    `--color-status-pending`, `--color-status-in-progress`,
    `--color-status-done`, `--color-status-cancelled`,
    `--color-priority-low`, `--color-priority-medium`,
    `--color-priority-high`, cada uma como um par `-bg`/`-text` (catorze
    custom properties no total) para permitir uma pílula colorida com texto
    legível, não só uma borda colorida.
  - `web/src/design/tokens.test.ts` — um teste de contraste por par
    `-bg`/`-text`, mesmo padrão dos pares existentes (`≥ 4.5:1`).
- **Faz:** define valores medidos (não estimados) para cada par, seguindo a
  mesma disciplina de `--color-border-interactive` (CI-5 da Fase 13): medir
  de verdade, documentar o resultado no comentário do token.
- **Não faz:** não aplica os tokens a nenhum componente ainda — isso é
  `CI-6`. Não cria uma paleta "de marca" nova além do que já existe em
  `tokens.css` (acento único `--color-accent`, resto neutro — mesma
  disciplina notada na conversa sobre o template de referência).
- **Testes:** `web/src/design/tokens.test.ts` — sete testes novos (um por
  par), assertando contraste `≥ 4.5:1`.
- **Verificação:** `npm test` (dentro de `web/`).
- **Depende de:** _nada_.

### CI-2 — Largura de conteúdo consistente

- **Arquivos:**
  - `web/src/design/tokens.css` — um token novo, `--content-max-width`
    (ex.: `72rem`), medido contra o layout real das telas hoje (auditoria
    encontrou uma coluna estreita e não-intencional, alinhada à esquerda,
    em vez de um container deliberado).
  - `web/src/App.css` (ou um novo `web/src/components/PageContainer.module.css`
    + `PageContainer.tsx`, o que for mais simples de reutilizar entre telas
    autenticadas e não-autenticadas) — a regra de largura+centralização em
    um único lugar.
- **Faz:** toda tela (login, register, lista de tasks) passa a usar a mesma
  largura de conteúdo centralizada, em vez da coluna estreita e desalinhada
  encontrada na auditoria visual.
- **Não faz:** não decide o app-shell (`CI-4`) nem o cartão de autenticação
  (`CI-5`) — só a largura/centralização que ambos vão reutilizar.
- **Testes:** _nenhum teste de comportamento — mudança de layout puro,
  coberta indiretamente pelos testes visuais/tokens de `CI-1` e pelo
  `assertOnlyTokens` do CSS module tocado._
- **Verificação:** `npm run build` + inspeção visual real (`vite preview`),
  mesmo padrão de "visto rodando" já exigido no resto do projeto.
- **Depende de:** _nada_.

### CI-3 — `Select`: paridade visual com `TextField`

- **Arquivos:** `web/src/components/Select.tsx`,
  `web/src/components/Select.module.css`, `web/src/components/Select.test.tsx`.
- **Faz:** o `<select>` nativo hoje renderiza com fonte/borda/altura do
  sistema operacional, destoando visivelmente do `TextField` logo acima dele
  em `TaskForm` (achado da auditoria visual). Restilizado para ter a mesma
  altura, borda (`--color-border-interactive`, mesma correção de contraste
  de CI-5 da Fase 13) e tipografia de `TextField.module.css`.
- **Não faz:** não substitui o `<select>` nativo por um combobox customizado
  — continua HTML semântico primeiro (mesma regra de CI-5 da Fase 13),
  só a casca visual muda.
- **Testes:** `web/src/components/Select.test.tsx` — os testes existentes
  continuam passando (nenhuma mudança de comportamento/acessibilidade);
  `assertOnlyTokens` no CSS module cobre a ausência de valor literal.
- **Verificação:** `npm test`.
- **Depende de:** _nada_.

### CI-4 — App shell: cabeçalho e navegação persistente

- **Arquivos:**
  - `web/src/components/AppShell.tsx` (novo) + `AppShell.module.css` +
    `AppShell.test.tsx` — nome do app, indicação de view atual, e um menu do
    usuário (email + "Log out" + "Sign out of all devices", movidos para cá).
  - `web/src/App.tsx` — `AuthenticatedHome` passa a renderizar `<AppShell>`
    em vez do parágrafo + dois botões soltos que a auditoria visual
    encontrou.
- **Faz:** dá ao app uma identidade visual persistente e wayfinding — hoje
  a tela autenticada "lê como página de teste, não como produto" (achado
  #1 da auditoria, o de maior impacto). Usa `<header>`/`<nav>` semânticos
  (landmarks reais, não `<div>`s genéricos) e o token `--content-max-width`
  de `CI-2`.
- **Não faz:** busca global, avatar, seletor de tema — resolvido em
  `validation.md` (AM-4) como fora do escopo desta v1, sem pedido concreto
  que os justifique ainda.
- **Testes:** `web/src/components/AppShell.test.tsx` (novo) — renderiza
  nome do app e email do usuário; clicar "Log out" chama a função passada;
  landmarks `header`/`navigation` presentes via `getByRole`.
  `web/src/App.test.tsx` — atualizado onde a estrutura de
  `AuthenticatedHome` mudou (os botões de logout continuam alcançáveis,
  agora dentro do shell).
- **Verificação:** `npm test`.
- **Depende de:** CI-2.

### CI-5 — Login/Register: cartão e identidade visual

- **Arquivos:** `web/src/features/auth/LoginPage.tsx`,
  `RegisterPage.tsx`, `AuthLayout.module.css` (novo, compartilhado pelas
  duas) — mais os testes existentes de ambas.
- **Faz:** as duas telas ganham um cartão real (fundo, borda, sombra,
  padding — mesma linguagem visual de `Modal.module.css`), largura
  consistente (`CI-2`), nome do app acima do formulário, e a hierarquia
  tipográfica do `<h1>` usa `--font-size-2xl`/`--font-weight-semibold` em
  vez do padrão do navegador (achado #9 da auditoria: hoje é o maior
  elemento da tela, ~64px, sem peso de fonte aplicado).
- **Não faz:** não muda nenhuma regra de validação (`registerSchema`/
  `loginSchema` em `RegisterPage.tsx`/`LoginPage.tsx` ficam intactas) — só
  a casca visual ao redor do formulário existente.
- **Testes:** `LoginPage.test.tsx`, `RegisterPage.test.tsx` — os testes
  existentes continuam passando sem alteração de asserção (mudança é
  puramente visual, não muda `role`/`label` de nada); `assertOnlyTokens`
  no `AuthLayout.module.css` novo.
- **Verificação:** `npm test`.
- **Depende de:** CI-2.

### CI-6 — `TaskItem`: badges coloridos, ações agrupadas, delete comedido

- **Arquivos:** `web/src/features/tasks/TaskItem.tsx`,
  `TaskItem.module.css`, `TaskItem.test.tsx`.
- **Faz:** três correções da auditoria visual, todas no mesmo componente:
  1. Os badges de status/prioridade usam os pares de token de `CI-1` (pílula
     colorida, texto legível) em vez de contorno branco com texto preto —
     achado #2 da auditoria, o de maior alavancagem para a "sensação
     escaneável" pedida.
  2. O botão "Delete" da linha (o que abre o modal de confirmação) passa de
     `variant="danger"` para `variant="secondary"` — o vermelho sólido fica
     reservado para o botão de confirmação real dentro do modal (que a
     auditoria já considerou "apropriadamente sério"), não repetido em toda
     linha da lista (achado #3: "15+ botões vermelhos na mesma tela").
  3. Uma separação visual (mesmo tratamento de borda que a seção de anexos
     já usa, `border-top` + `padding-top`) entre o grupo de controles de
     status, o grupo Edit/Delete, e a seção de anexos — hoje os três leem
     como uma única linha de botões sem relação (achado #8).
  4. `description` deixa de estar centralizado (estava destoando do resto,
     alinhado à esquerda — achado #10).
- **Não faz:** não muda a lógica de `TaskStatusControls` (quais transições
  são legais continua vindo do mesmo lugar) — só a apresentação visual dos
  grupos ao redor dela.
- **Testes:** `TaskItem.test.tsx` — atualiza a asserção que hoje espera
  `variant="danger"` no botão de linha (se existir) para `secondary`,
  mantém a asserção do botão de confirmação como `danger`; nenhum teste
  existente de comportamento (edição, exclusão, transição) muda de
  resultado. `assertOnlyTokens` cobre os tokens novos de `CI-1` usados
  aqui.
- **Verificação:** `npm test`.
- **Depende de:** CI-1, CI-3.

### CI-7 — `TaskList`: cabeçalho de página e densidade

- **Arquivos:** `web/src/features/tasks/TaskList.tsx`,
  `TaskList.module.css`, `TaskList.test.tsx`.
- **Faz:** um cabeçalho de página próprio (título "Tasks", contagem visível,
  "New task" reposicionado com intenção — hoje é "o único elemento com cor
  de destaque na tela", o que continua verdade, mas ganha companhia
  estrutural de um título de página em vez de flutuar sozinho) abaixo do
  `AppShell` de `CI-4`. Espaçamento vertical de cada linha reduzido agora
  que cor/peso (`CI-6`) fazem parte do trabalho de escaneabilidade que hoje
  só o espaço em branco fazia (achado #12).
- **Não faz:** não agrupa por status nem introduz uma view de board —
  decisão explícita do usuário (`validation.md` AM-2) de deixar isso fora
  desta fase.
- **Testes:** `TaskList.test.tsx` — os quatro estados (loading/empty/error/
  sucesso) continuam passando sem alteração de asserção; `assertOnlyTokens`
  no CSS module.
- **Verificação:** `npm test`.
- **Depende de:** CI-4, CI-6.

### CI-8 — Anexos: estado vazio discreto

- **Arquivos:** `web/src/features/attachments/AttachmentList.tsx`,
  `AttachmentList.module.css`, `AttachmentList.test.tsx`.
- **Faz:** quando uma task não tem nenhum anexo, a linha "No attachments
  yet." mais o botão "Upload file" sempre visível hoje se repetem em toda
  task da lista, mesmo vazias (achado #6 — "ruído repetido em 15+ linhas").
  Reduz o peso visual do estado vazio (tipografia menor/tom mais discreto
  via `--color-text-secondary`, já existente) sem esconder ou desabilitar
  a funcionalidade — `Upload file` continua alcançável por teclado e
  visível, só deixa de competir visualmente com o conteúdo real da task.
- **Não faz:** não esconde o controle de upload nem o torna inacessível por
  teclado — isso violaria o invariante de operabilidade só-por-teclado
  herdado de CI-10/CI-11 da Fase 13. Não muda a lógica de
  `attachments_enabled` (continua decidindo se a seção existe; esta mudança
  só afeta a aparência de uma seção que já existe).
- **Testes:** `AttachmentList.test.tsx` — os quatro estados continuam
  passando; nenhum teste novo de comportamento (mudança é só de peso
  visual). `assertOnlyTokens` no CSS module.
- **Verificação:** `npm test`.
- **Depende de:** CI-6.

### CI-9 — Acessibilidade e E2E reverificados contra o redesenho

- **Arquivos:** nenhum arquivo novo fixo — depende do que a reverificação
  encontrar (mesmo formato de CI-10 da Fase 13).
- **Faz:** repete, de verdade, a auditoria de acessibilidade automatizada
  (axe-core, três larguras, mesmo escopo de telas de CI-10 da Fase 13) e a
  suíte E2E completa (`npx playwright test` contra `docker compose up`,
  CI-11 da Fase 13) contra as telas redesenhadas — cor/contraste/hierarquia
  são exatamente o tipo de mudança que poderia introduzir uma regressão que
  a passagem limpa original de CI-10 não previa. Corrige qualquer achado
  real antes de fechar a fase.
- **Não faz:** não é um redesign — é verificação e ajuste do que
  `CI-1`–`CI-8` desta fase produziram, mesmo espírito de CI-10 da Fase 13.
- **Testes:** o próprio resultado da auditoria e da suíte E2E, registrado
  no PR (execução real, resultado colado — não é um `npm test` no sentido
  usual).
- **Verificação:** execução real do axe-core + `npx playwright test` contra
  `docker compose up` + navegação manual só por teclado — não leitura de
  código.
- **Depende de:** CI-1, CI-2, CI-3, CI-4, CI-5, CI-6, CI-7, CI-8.

## Mapa de dependências

```
CI-1 ──────────────┐
CI-2 ──┬── CI-4 ──┐ │
       └── CI-5   │ │
CI-3 ──────────────┼─ CI-6 ── CI-7
                    │         │
                    │         CI-8
                    │          │
                    └──────────┴── CI-9
```

## Entregáveis

- [ ] `web/src/design/tokens.css` + `tokens.test.ts` — tokens de status/
      prioridade e largura de conteúdo
- [ ] `web/src/components/Select.{tsx,module.css}` — restilizado
- [ ] `web/src/components/AppShell.{tsx,module.css,test.tsx}` — novo
- [ ] `web/src/features/auth/{LoginPage,RegisterPage}.tsx` +
      `AuthLayout.module.css` — restilizados
- [ ] `web/src/features/tasks/{TaskItem,TaskList}.{tsx,module.css}` —
      restilizados
- [ ] `web/src/features/attachments/AttachmentList.{tsx,module.css}` —
      restilizado
- [ ] Auditoria de a11y + E2E reverificadas, registradas no PR de `CI-9`
- [ ] `CHANGELOG.md` — entrada nova quando esta fase for lançada
- [ ] `docs/ARCHITECTURE.md` — **sem alteração** (filtro/busca continua
      adiado, board não vira item novo por não ter sido decidido nesta fase)

## Riscos e como o plano os cobre

| Risco | Coberto por |
|---|---|
| Um token de cor novo (status/prioridade) falha WCAG AA e só é percebido depois de aplicado em `CI-6` | `CI-1` testa contraste antes de qualquer componente consumir o token |
| Restilizar `TaskItem`/`TaskList` quebra um `getByRole`/`getByLabel` que os specs de E2E (Fase 13, CI-11) dependem | `CI-9` roda a suíte E2E completa de verdade contra o redesenho, não só os testes unitários locais de cada `CI` |
| Reduzir o peso visual do estado vazio de anexos (`CI-8`) sem querer torna "Upload file" inacessível por teclado | `CI-8` mantém o controle visível e alcançável; `CI-9` reverifica o fluxo de teclado ponta a ponta |
| App shell novo (`CI-4`) esconde ou duplica a lógica de `logout`/`logout-all` que `useAuth` já expõe | `CI-4` só move a apresentação para dentro do shell — `useAuth()` continua a única fonte da lógica, testado em `AppShell.test.tsx` |
| Escopo crescer de volta para filtro/busca ou board no meio da implementação, revertendo a decisão já tomada em `validation.md` | Plano não inclui nenhum `CI` de contrato; qualquer pedido nessa direção durante a implementação volta para `/decide` ou uma fase nova, não uma expansão silenciosa deste plano |
