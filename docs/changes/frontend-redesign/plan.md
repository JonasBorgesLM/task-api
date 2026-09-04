---
slug: frontend-redesign
stage: plan
tier: full
items: 15
sources_mtime:
  docs/changes/frontend-redesign/context.md: 2026-09-02T00:35:00Z
  docs/changes/frontend-redesign/validation.md: 2026-09-03T00:00:00Z
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

Explicitamente fora desta fase (decisão do usuário, `validation.md` AM-2):
uma visualização em board/kanban — candidata a uma fase futura.

`AM-1` (filtro/busca de `GET /tasks`) foi reaberta depois de `CI-1`–`CI-13`
implementados e testados na prática — ver `validation.md`'s seção "AM-1 —
Filtro/busca de GET /tasks (reaberta e refechada)". Filtro estruturado por
`status`/`priority` entra nesta fase como `CI-14` (backend) + `CI-15`
(frontend); busca livre por título continua fora (`AD-1` fecha só
parcialmente).

## Restrições herdadas

- `CI-1`–`CI-13` são estritamente
  frontend, sem mudança em `docs/openapi.yaml`. `CI-14` é a única exceção
  deliberada, decidida depois do plano original (ver `validation.md`'s AM-1
  reaberta): estende `GET /v1/tasks` com dois query params novos,
  `status`/`priority`, seguindo `.claude/rules/api-contract.md` — o contrato
  muda na mesma mudança que o handler, nunca depois.
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

### CI-5 — Login/Register: layout split-screen com painel de marketing

- **Arquivos:** `web/src/features/auth/LoginPage.tsx`,
  `RegisterPage.tsx`, `AuthLayout.tsx` (novo — antes só se previa o CSS
  module, o layout de duas colunas justifica um componente de verdade),
  `AuthLayout.module.css` (novo, compartilhado pelas duas) — mais os
  testes existentes de ambas.
- **Faz:** layout de duas colunas em telas largas (empilha em uma coluna
  só abaixo de `--breakpoint-md`, o formulário sempre primeiro no DOM —
  nunca depende de layout visual para ordem de leitura/tab): a coluna do
  formulário ganha um cartão real (fundo, borda, sombra, padding — mesma
  linguagem visual de `Modal.module.css`), nome do app acima, hierarquia
  tipográfica real no `<h1>` (`--font-size-2xl`/`--font-weight-semibold`
  em vez do padrão do navegador — achado #9 da auditoria: hoje é o maior
  elemento da tela, ~64px, sem peso de fonte aplicado). A segunda coluna é
  um painel de marketing descrevendo o produto — nome, proposta de valor
  curta, três ou quatro destaques reais (CRUD de task com transição de
  status, anexos com preview/download, sessão dupla cookie+Bearer segura)
  — usando só os tokens já existentes (acento único, sem gradiente/sombra
  decorativa, mesma disciplina de "issue #121"), não uma cópia do
  template de referência que motivou o pedido (ver
  `docs/changes/frontend-redesign/validation.md`'s `AM-5`).
- **Não faz:** não muda nenhuma regra de validação (`registerSchema`/
  `loginSchema` em `RegisterPage.tsx`/`LoginPage.tsx` ficam intactas) — só
  a casca visual ao redor do formulário existente. O painel de marketing é
  puramente apresentacional — `aria-hidden` ou decorativo o bastante para
  não competir com a ordem de tabulação do formulário real.
- **Testes:** `LoginPage.test.tsx`, `RegisterPage.test.tsx` — os testes
  existentes continuam passando sem alteração de asserção (mudança é
  puramente visual, não muda `role`/`label` de nada); `assertOnlyTokens`
  no `AuthLayout.module.css` novo; um teste novo confirma que o painel de
  marketing não aparece antes do formulário na ordem de tabulação.
- **Verificação:** `npm test` + `vite preview` (visto rodando em desktop e
  mobile — o empilhamento em coluna única é comportamento real, não só
  CSS lido).
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

### CI-10 — Tema escuro: paleta e tokens

- **Arquivos:** `web/src/design/tokens.css`, `web/src/design/tokens.test.ts`.
- **Faz:** um segundo conjunto de valores para todo token de cor já
  existente (neutros, acento, status/prioridade de `CI-1`), sob
  `@media (prefers-color-scheme: dark)` como padrão automático **e**
  `:root[data-theme="dark"]`/`:root[data-theme="light"]` para uma escolha
  explícita do usuário vencer a preferência do sistema — mesmo mecanismo
  aditivo que o comentário original de `tokens.css` já previa ("CSS custom
  properties make adding one later ... additive, not a rewrite"). Cada par
  texto/fundo do tema escuro passa pelo mesmo teste de contraste WCAG AA
  que o tema claro já tem — não é o mesmo número reaproveitado, é medido
  de novo para os valores escuros. **Não é o template de referência**: sem
  o laranja dele, sem gradiente, sem sombra decorativa — mesma disciplina
  de "issue #121" que já rege o tema claro, aplicada a uma segunda
  paleta.
- **Não faz:** não decide como o usuário troca de tema (isso é `CI-11`) e
  não aplica nada a nenhum componente ainda — só define os valores.
- **Testes:** `tokens.test.ts` — um teste de contraste por par do tema
  escuro, mesmo padrão dos pares claros existentes.
- **Verificação:** `npm test`.
- **Depende de:** CI-1.

### CI-11 — Tema escuro: alternância, aplicação e auditoria

- **Arquivos:** `web/src/components/ThemeToggle.{tsx,module.css,test.tsx}`
  (novo — vive no menu do usuário de `AppShell`), um hook/contexto pequeno
  para persistir a escolha explícita (`localStorage`, preferência de UI,
  não credencial — não conflita com a restrição de `docs/DECISIONS.md`
  sobre nunca guardar sessão lá).
- **Faz:** alternância real entre claro/escuro/"seguir o sistema", com o
  sistema como padrão até uma escolha explícita existir. Como todo
  componente desde `CI-5`+ já consome tokens em vez de cor literal
  (verificado por `assertOnlyTokens` em cada um), a expectativa é que
  **nenhum componente existente precise de mudança de código** — só os
  novos valores de `CI-10` entrando em vigor. Se algum precisar, é sinal
  de um token literal escapando do sistema, corrigido aqui.
  Reverifica de verdade (não por inspeção) a auditoria de acessibilidade
  automatizada e uma navegação manual só por teclado, desta vez com o
  tema escuro ativo — mesmo padrão de "visto rodando" de `CI-9`.
- **Não faz:** não adiciona uma terceira opção de tema além de
  claro/escuro/sistema. Não é um redesign do tema escuro além do que
  `CI-10` já define — é a alternância e a verificação de que o resto do
  app realmente segue os tokens sem precisar de ajuste manual.
- **Testes:** `ThemeToggle.test.tsx` — alterna o atributo `data-theme`,
  persiste em `localStorage`, respeita `prefers-color-scheme` como padrão
  na ausência de escolha explícita. Resultado da auditoria/navegação
  registrado no PR, mesmo formato de `CI-9`.
- **Verificação:** `npm test` + execução real (axe-core + teclado) com o
  tema escuro ativo, resultado colado no PR.
- **Depende de:** CI-10, CI-9 (o redesenho claro precisa estar
  auditado e estável antes de verificar a versão escura dele).

### CI-12 — `TaskStatusControls`: menu de ações por ícone (padrão Apple)

- **Arquivos:** `web/src/components/Menu.{tsx,module.css,test.tsx}` (novo
  primitive — nenhum componente de menu/dropdown existe hoje),
  `web/src/features/tasks/TaskStatusControls.{tsx,module.css,test.tsx}`.
- **Faz:** troca os até três botões de texto sempre visíveis ("Move to
  In progress", "Move to Done", "Move to Cancelled") por um único
  botão-gatilho (ícone, sem texto solto competindo com Edit/Delete) que
  abre um menu com as transições legais — mesmo padrão de pull-down menu
  usado em apps macOS/iOS: uma ação por vez, escondida até ser pedida, em
  vez de toda opção sempre visível. `Menu.tsx` é HTML semântico + ARIA
  real (`role="menu"`/`"menuitem"`), não um `<select>` disfarçado: abre
  com Enter/Espaço, navega com as setas, fecha com Escape ou clique fora,
  devolve o foco ao gatilho ao fechar. Cada item do menu usa um ícone SVG
  inline pequeno (sem biblioteca de ícones nova — `fill="currentColor"`
  herda a cor do token que já estiver em uso, mesmo espírito de "sem
  dependência nova sem necessidade real" que rege o lado Go).
- **Não faz:** não muda quais transições são legais (mesma tabela de
  `TaskStatusControls.tsx` já existente) nem o tratamento do 409
  ambíguo (`AM-5` da Fase 13 já resolvido, issue #153) — só a
  interação/apresentação.
- **Testes:** `Menu.test.tsx` (novo primitive — abre/fecha, navegação por
  teclado, clique fora, Escape, foco devolvido ao gatilho);
  `TaskStatusControls.test.tsx` atualizado — abre o menu, clica uma
  transição, chama `onSuccess`; transições ilegais continuam ausentes do
  menu (mesma tabela `LEGAL_TRANSITIONS`, verificada agora contra os
  itens do menu em vez dos botões).
- **Verificação:** `npm test` + `vite preview` (visto rodando, navegação
  só por teclado incluída).
- **Depende de:** CI-6.

### CI-13 — Efeitos visuais: blur, elevação, transição, glow, gradiente

- **Arquivos:** `web/src/design/tokens.css` (dois tokens novos —
  `--color-surface-translucent`, `--color-focus-glow`),
  `web/src/components/AppShell.module.css`,
  `web/src/features/tasks/TaskItem.module.css`,
  `web/src/components/{Button,TextField,Select,Checkbox}.module.css`.
- **Faz:** cinco efeitos novos (um sexto, shimmer no `Skeleton`, já
  existia desde CI-5 da Fase 13 — conferido no código antes de propor,
  não reproposto aqui), todos atrás de
  `@media (prefers-reduced-motion: no-preference)` onde envolvem
  movimento, todos usando token em vez de valor literal:
  1. **Header com desfoque** — `AppShell`'s `.header` ganha
     `position: sticky` + `backdrop-filter: blur(...)` sobre
     `--color-surface-translucent` (novo token, o mesmo `--color-surface`
     com alfa) em vez de opaco — o conteúdo passa por baixo ao rolar a
     lista.
  2. **Elevação em hover** — linhas de `TaskItem` ganham `--shadow-md` (já
     existente, só não usado em hover) ao passar o mouse/foco
     (`:focus-within` também, para paridade com navegação por teclado).
  3. **Transição suave de cor nos badges** — `background-color`/`color`
     dos badges de status/prioridade (`CI-6`) transicionam em vez de
     trocar abrupto quando uma task muda de status. Inerte até `CI-6`
     aplicar cor de verdade aos badges — a regra de transição não exige
     que a cor já varie para existir.
  4. **Glow sutil no foco** — estende o `:focus-visible` já existente de
     `Button`/`TextField`/`Select`/`Checkbox` com um `box-shadow` suave
     (`--color-focus-glow`, novo token — o mesmo `--color-accent` a baixa
     opacidade), além do anel que já existe (decoração sobre o requisito
     de WCAG 2.4.11, nunca substituindo o outline que o satisfaz).
  5. **Gradiente de dois tons no botão primário** — única sugestão que
     tensiona com "sem gradiente sem motivo" (issue 121): justificada
     aqui como profundidade no único botão de ação primária da tela, não
     decoração — dois tons do mesmo `--color-accent`/`--color-accent-hover`
     (ambos já existentes, nenhuma cor nova), nunca uma cor diferente.
     Hover/active usam `filter: brightness()` sobre o mesmo gradiente em
     vez de trocar para uma cor sólida — o gradiente nunca desaparece
     numa interação.
- **Não faz:** não aplica nenhum efeito a `variant="secondary"`/
  `variant="danger"` de `Button` (o gradiente é só do primário) nem
  introduz uma biblioteca de animação — tudo é CSS puro, já é o padrão do
  resto do projeto.
- **Testes:** nenhum teste de comportamento novo (mudança é
  puramente visual/decorativa, nenhum `role`/estado muda) —
  `assertOnlyTokens` em cada `.module.css` tocado cobre a ausência de
  valor literal. Nenhum teste novo em `tokens.test.ts`: os dois tokens
  novos são decorativos (a mesma isenção que `--shadow-*` já tem), não
  pares texto/fundo com obrigação de contraste.
- **Verificação:** `npm test` + `vite preview` (visto rodando — blur ao
  rolar, hover, foco, gradiente, todos efeitos que só existem de verdade
  num navegador real, verificados via `getComputedStyle` real, não só
  lidos no CSS).
- **Depende de:** CI-4 (header a desfocar precisa existir), CI-6
  (badges a colorir precisam existir para o efeito 3 ser visível, mesmo
  que a regra em si seja inerte antes disso).

### CI-14 — Backend: filtro de `status`/`priority` em `GET /v1/tasks`

- **Arquivos:**
  - `internal/task/repository.go` — `Repository.FindAll` ganha dois
    parâmetros novos, `status Status` e `priority Priority`, ambos
    posicionais depois de `limit, offset`. Valor zero (`""`) em qualquer um
    dos dois significa "sem filtro nesse campo" — o mesmo sentinela que
    `CreateTaskRequest.Priority`/`UpdateTaskRequest.Priority` já usam para
    "não informado" (`docs/openapi.yaml`'s `CreateTaskRequest`/
    `UpdateTaskRequest`), não um valor novo inventado para este `CI`.
  - `internal/task/memory_repository.go` — `FindAll` aplica os dois filtros
    (comparação direta de campo) **antes** de `paginateTasks`, não depois —
    filtrar depois da janela de paginação mudaria quantos resultados uma
    página com filtro retorna, o que divergiria de como um `WHERE` do
    Postgres se comporta.
  - `internal/task/postgres_repository.go` — `FindAll` adiciona `AND status
    = $N`/`AND priority = $N` condicionalmente à query (construção
    dinâmica da cláusula `WHERE` + slice de argumentos, já que `database/sql`
    não tem parâmetro opcional nativo) — mantém o filtro dentro do SQL, não
    "busca tudo e filtra em Go", mesma disciplina que `CLAUDE.md` já exige
    para `userID`/paginação.
  - `internal/task/service.go` — `Service.ListTasks` ganha `status,
    priority string` (não `Status`/`Priority` tipados — o handler só tem a
    string crua da query string). Valida cada um, se não-vazio, contra os
    mapas `validStatuses`/`validPriorities` já existentes (reuso, não
    duplicação); um valor desconhecido retorna `ErrInvalidInput` — mesma
    mensagem/formato de erro que `CreateTask` já usa para uma `priority`
    inválida.
  - `internal/task/handler.go` — `taskService` (a interface que o handler
    depende) ganha os dois parâmetros; `listTasks` lê `r.URL.Query().Get
    ("status")`/`.Get("priority")` (sem parsing numérico — são strings
    passadas direto ao Service, que valida) e repassa ao `Service`.
  - `docs/openapi.yaml` — `GET /v1/tasks`: dois `parameters` novos
    (`status`, `priority`, ambos `schema: $ref Status`/`Priority`, ambos
    opcionais), descrição atualizada explicando que os dois se combinam com
    AND quando ambos presentes, e o `400` já documentado ganha mais uma
    causa (valor de `status`/`priority` que não é um dos enums).
- **Faz:** um cliente pode pedir `GET /v1/tasks?status=pending` ou
  `?priority=high` ou os dois juntos — a combinação é AND, não OR (pedir
  "pending E high" é o caso de uso real; "pending OU high" não foi pedido e
  seria um parâmetro de forma diferente, fora de escopo). Continua ordenado
  por `(created_at, id)` e paginado exatamente como hoje — o filtro só
  reduz o conjunto que a paginação janela.
- **Não faz:** não adiciona busca por texto/título (`AD-1` continua
  parcialmente aberto — ver `validation.md`). Não muda `ListTasks`'s
  ordenação nem introduz um parâmetro de ordenação novo — ninguém pediu
  isso. Não valida `status`/`priority` vazio como erro — string vazia
  continua sendo "sem filtro", exatamente como `priority: ""` já significa
  em `CreateTaskRequest`.
- **Testes:**
  - `internal/task/memory_repository_test.go` — casos novos ao lado de
    `TestFindAll_Pagination`: filtro só por `status`, só por `priority`,
    os dois juntos, filtro que não casa com nada (retorna `[]Task{}`, não
    `nil`), e um teste confirmando que filtro + paginação combinados
    janelam o conjunto já filtrado (não o total).
  - `internal/task/postgres_repository_test.go` (`//go:build integration`)
    — os mesmos casos, contra Postgres de verdade, confirmando paridade com
    `memory_repository_test.go` por `go-repository-parity.md`.
  - `internal/task/service_test.go` — `fakeRepository.FindAll` ganha os
    dois parâmetros novos (e `findAllCalledWith` passa a registrar os
    cinco); `TestListTasks_PassesUserIDLimitOffsetToRepository` renomeado/
    estendido para cobrir status/priority também; casos novos para
    `ErrInvalidInput` em `status`/`priority` desconhecidos.
  - `internal/task/handler_test.go` — `listTasksFn` ganha os dois
    parâmetros; casos novos de `?status=`/`?priority=` válidos, inválidos
    (`400`), combinados, e ausentes (comportamento atual preservado).
- **Verificação:** `make check` (unit, `-race`, vet, staticcheck, gofmt) +
  `make test-integration` contra Postgres real (`make db-up` primeiro).
- **Depende de:** _nada_ — trabalho de backend, sem sobreposição de arquivo
  com nenhum `CI` de frontend desta fase.

### CI-15 — Frontend: controles de filtro em `TaskList`

- **Arquivos:**
  - `web/src/features/tasks/TaskList.tsx`, `TaskList.module.css`,
    `TaskList.test.tsx` — dois `Select` novos (reusa o componente já
    restilizado em `CI-3`) para `status`/`priority`, com uma opção "Todos"
    representando "sem filtro" (mapeada para omitir o query param, não para
    enviar uma string vazia — consistente com `CI-14`'s sentinela).
  - `web/src/features/tasks/useTasks.tsx` — a hook que hoje chama
    `GET /tasks` (sem filtro) passa a aceitar `status`/`priority` e
    incluí-los na query string só quando não-vazios.
  - `web/src/api/client.ts` — se `listTasks` já tiver uma assinatura de
    parâmetros de paginação, os dois filtros entram do mesmo jeito
    (parâmetros opcionais); se hoje não aceita nenhum parâmetro de query,
    ganha os primeiros aqui, seguindo o mesmo padrão já usado por outros
    métodos do client para parâmetros opcionais.
- **Faz:** o usuário filtra a lista de tasks por status e/ou prioridade
  direto no backend (não é mais só reordenação client-side do que já foi
  carregado) — o pedido concreto que reabriu `AM-1`: "gostaria de ter
  filtros também para organizar as tasks". Estado do filtro fica em
  `useState` local de `TaskList` (não em `localStorage`/URL) — nenhum
  pedido de persistir o filtro entre sessões ou compartilhar um link
  filtrado ainda existe; adicionar isso sem pedido seria antecipação
  especulativa (`CLAUDE.md` § "No speculative abstraction").
- **Não faz:** não adiciona um campo de busca por texto (mesmo motivo de
  `CI-14`: `AD-1` continua parcialmente aberto). Não muda a lógica de
  criação/edição/transição de task — só como a lista é buscada.
- **Testes:** `TaskList.test.tsx` — muda o `Select` de status, confirma que
  a chamada a `useTasks`/`listTasks` inclui o `status` escolhido; o mesmo
  para `priority`; escolher "Todos" de volta remove o parâmetro da próxima
  chamada; os quatro estados (loading/empty/error/sucesso) continuam
  passando com o filtro em qualquer posição. `assertOnlyTokens` no CSS
  module tocado.
- **Verificação:** `npm test` + `vite preview` real contra o backend de
  `CI-14` rodando (não só os testes unitários — filtro é exatamente o tipo
  de mudança que "funciona nos testes mas não bate com o parâmetro real do
  backend" se um dos dois lados mudar o nome do query param sem o outro).
- **Depende de:** `CI-14` (o backend precisa aceitar os parâmetros antes do
  frontend poder enviá-los de verdade), `CI-3` (reusa `Select`), `CI-7`
  (cabeçalho de `TaskList` onde os filtros vivem — ainda não implementado
  neste branch; se `CI-15` for implementado antes de `CI-7`, os filtros
  entram no `TaskList` de hoje e são reposicionados quando `CI-7`
  chegar, não bloqueados por ele).

## Mapa de dependências

```
CI-1 ──────────────┐
CI-2 ──┬── CI-4 ──┐ │
       └── CI-5   │ │
CI-3 ──────────────┼─ CI-6 ──┬── CI-7
                    │         ├── CI-8
                    │         └── CI-12
CI-4, CI-6 ─────────┴── CI-13 ─┘
                                │
                                └── CI-9 ── CI-11
                                     │       │
CI-1 ── CI-10 ───────────────────────┴───────┘

CI-14 (independente) ── CI-15 (também depende de CI-3, CI-7)
```
(`CI-13` depende de `CI-4` e `CI-6` — mostrado à parte acima porque um
diagrama em texto não representa bem uma aresta que cruza os outros
ramos; a lista "Depende de" de cada item é a fonte de verdade, não este
desenho.)

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
- [ ] `web/src/components/Menu.{tsx,module.css,test.tsx}` — novo,
      primitive de menu acessível (`CI-12`)
- [ ] `web/src/features/tasks/TaskStatusControls.{tsx,module.css,test.tsx}`
      — redesenhado como menu de ícones (`CI-12`)
- [ ] `web/src/design/tokens.css` — `--color-surface-translucent` e
      `--color-focus-glow` (`CI-13`)
- [ ] `web/src/components/AppShell.module.css` — header fixo com
      desfoque (`CI-13`)
- [ ] `web/src/features/tasks/TaskItem.module.css` — elevação em hover,
      transição de cor nos badges (`CI-13`)
- [ ] `web/src/components/{Button,TextField,Select,Checkbox}.module.css`
      — glow no foco (`CI-13`)
- [ ] Auditoria de a11y + E2E reverificadas, registradas no PR de `CI-9`
- [ ] `web/src/design/tokens.css` + `tokens.test.ts` — paleta escura,
      testada por contraste (`CI-10`)
- [ ] `web/src/components/ThemeToggle.{tsx,module.css,test.tsx}` — novo,
      alternância claro/escuro/sistema (`CI-11`)
- [ ] Auditoria de a11y + navegação por teclado reverificadas com o tema
      escuro ativo, registradas no PR de `CI-11`
- [ ] `internal/task/{repository,memory_repository,postgres_repository,
      service,handler}.go` — filtro `status`/`priority` (`CI-14`)
- [ ] `docs/openapi.yaml` — `GET /v1/tasks` com os dois query params novos
      (`CI-14`)
- [ ] `web/src/features/tasks/{TaskList,useTasks}.tsx` +
      `web/src/api/client.ts` — controles de filtro (`CI-15`)
- [ ] `CHANGELOG.md` — entrada nova quando esta fase for lançada
- [ ] `docs/ARCHITECTURE.md` — o bullet de Future Improvements sobre
      "Task filtering and search" é reescrito para refletir só a metade
      que continua pendente (busca por título) — a metade filtro fecha com
      `CI-14`/`CI-15` (ver `validation.md`'s `AD-1`). Board não vira item
      novo por não ter sido decidido nesta fase.

## Riscos e como o plano os cobre

| Risco | Coberto por |
|---|---|
| Um token de cor novo (status/prioridade) falha WCAG AA e só é percebido depois de aplicado em `CI-6` | `CI-1` testa contraste antes de qualquer componente consumir o token |
| Restilizar `TaskItem`/`TaskList` quebra um `getByRole`/`getByLabel` que os specs de E2E (Fase 13, CI-11) dependem | `CI-9` roda a suíte E2E completa de verdade contra o redesenho, não só os testes unitários locais de cada `CI` |
| Reduzir o peso visual do estado vazio de anexos (`CI-8`) sem querer torna "Upload file" inacessível por teclado | `CI-8` mantém o controle visível e alcançável; `CI-9` reverifica o fluxo de teclado ponta a ponta |
| App shell novo (`CI-4`) esconde ou duplica a lógica de `logout`/`logout-all` que `useAuth` já expõe | `CI-4` só move a apresentação para dentro do shell — `useAuth()` continua a única fonte da lógica, testado em `AppShell.test.tsx` |
| Painel de marketing de `CI-5` acaba na frente do formulário na ordem de tabulação, ou vira uma cópia do template de referência em vez de uma peça própria | `CI-5` testa a ordem de tabulação explicitamente; `validation.md`'s `AM-5` registra que só o princípio (token único, acento restrito) se estende, não a paleta/animação do template |
| Um componente restilizado em `CI-5`–`CI-8` usa uma cor literal em vez de token, e só quebra visualmente quando o tema escuro (`CI-10`) entra em vigor | `assertOnlyTokens` já bloqueia isso no CI de cada componente, antes do tema escuro sequer existir — `CI-11` é a prova final, não a primeira linha de defesa |
| `Menu.tsx` (`CI-12`) reimplementa foco/teclado incorretamente — trap incompleto, Escape não fecha, foco não volta ao gatilho — e uma transição de status vira inacessível por teclado | `Menu.test.tsx` testa cada um desses casos isoladamente no primitive; `CI-9` reverifica o fluxo real de teclado ponta a ponta depois de integrado em `TaskStatusControls` |
| Esconder as transições atrás de um menu (`CI-12`) esconde também informação que antes era visível de relance (quais transições existem, não só a atual) | Cada item do menu usa ícone + rótulo (não só ícone) — a informação continua ali, só não ocupa espaço permanente na linha |
| Escopo crescer de volta para busca por título ou board no meio da implementação, indo além da decisão já tomada em `validation.md` | `CI-14`/`CI-15` cobrem só filtro estruturado por `status`/`priority` — qualquer pedido de busca por texto ou board durante a implementação volta para `/decide` ou uma fase nova, não uma expansão silenciosa deste plano |
| `internal/task/repository.go`'s `FindAll` ganhar parâmetros novos e `memoryRepository`/`postgresRepository` divergirem em como aplicam o filtro (ordem relativa a paginação, por exemplo) | `CI-14` testa os dois lados com os mesmos casos (`memory_repository_test.go` e `postgres_repository_test.go` espelhados), por `.claude/rules/go-repository-parity.md` |
| `CI-15` enviar um nome de query param que não bate com o que `CI-14` implementou (ex.: `status` vs. `state`) | `CI-15` só é testado "de verdade" (`vite preview` contra o backend de `CI-14` rodando), não só mocado — a mesma disciplina de verificação real já usada no resto desta fase |
