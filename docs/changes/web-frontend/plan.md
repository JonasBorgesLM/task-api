---
slug: web-frontend
stage: plan
tier: full
items: 11
sources_mtime:
  docs/changes/web-frontend/context.md: 2026-08-29T00:00:00Z
  docs/changes/web-frontend/validation.md: 2026-08-29T00:00:00Z
---

# web-frontend — Plano

**Nota sobre numeração:** `#119`–`#129` são os números reais das issues
`13.1`–`13.11` no GitHub (criadas via `create-fase12-13-frontend.sh`,
confirmadas via `gh issue list`).

**Nenhum `CI` deste plano começa antes de `dual-auth-mode/plan.md` estar
implementado e mergeado em `develop`** — combinado com o usuário. `CI-4`,
`CI-6` e `CI-9` em particular dependem de código real (não só do plano) de
`dual-auth-mode`.

## Objetivo

Depois desta mudança: existe uma SPA em `web/` (Vite + React + TypeScript
strict), autenticada por cookie httpOnly + CSRF, cobrindo registro, login,
logout, CRUD de task, transições de status e anexos, com os quatro estados
de UI (loading/empty/error/sucesso) tratados explicitamente, responsiva e
navegável só por teclado, com CI próprio que não interfere no gate Go.

## Restrições herdadas

- Nenhum componente visual faz HTTP — toda chamada passa por `api/`.
- `localStorage` nunca guarda credencial — cookie httpOnly (backend) +
  token CSRF em memória (frontend, nunca persistido).
- `503` nunca desloga — é retryable, não é `401`.
- `409` de `PATCH /status` é ambíguo por mensagem — tratado por match de
  string (`AM-5`), com débito técnico registrado, não escolhido em
  silêncio.
- `GET /tasks` não tem contagem total — paginação é infinite-scroll com
  item extra (`AM-4`), nunca numerada.
- `404` de anexo continua ambíguo no wire; `attachments_enabled` (de
  `dual-auth-mode` `CI-8`) é a única fonte confiável, nunca uma sondagem.
- `priority: ""` significa "não informado", nunca é enviado para "limpar"
  o campo.
- Versionamento do frontend é a mesma linha `vX.Y.Z` do repo — sem tag
  própria em `web/`.
- Tipos vêm gerados de `docs/openapi.yaml` (`openapi-typescript`), nunca
  escritos à mão.

## Itens

### CI-1 — Decisões de arquitetura do frontend em DECISIONS.md

- **Arquivos:** `docs/DECISIONS.md` — nova seção "Frontend: Vite (SPA), monorepo em `web/`, mesma linha de versão do repo".
- **Faz:** registra as quatro decisões (Vite não Next.js — ver
  `docs/ARCHITECTURE.md` § Future Improvements "BFF layer" como
  confirmação, não conflito; monorepo com CI filtrado por caminho;
  versionamento compartilhado; cookie httpOnly nunca `localStorage`, com
  link para a seção de `dual-auth-mode`).
- **Não faz:** não decide nada de UI/tokens — isso é `CI-3`.
- **Testes:** _nenhum — não altera comportamento_.
- **Verificação:** leitura humana (revisão de PR).
- **Depende de:** `dual-auth-mode` mergeado (a seção referencia a decisão
  de cookie httpOnly já registrada lá).

### CI-2 — Scaffold de `web/` e CI com filtro por caminho

- **Arquivos:**
  - `web/` — Vite + React + TypeScript (`strict: true`, `noUncheckedIndexedAccess: true`), ESLint, Prettier (ou Biome — decisão de baixo risco, escolher no momento), Vitest configurado. `package.json` com scripts espelhando os alvos Go: `dev`, `build`, `typecheck`, `lint`, `test`.
  - `.github/workflows/ci.yml` — ganha `paths-ignore: ['web/**']` em `on.push` e `on.pull_request`.
  - `.github/workflows/web-ci.yml` (novo) — `paths: ['web/**']`, job com `typecheck`/`lint`/`build`/`test` (unit, Vitest — não E2E, isso é `CI-11`).
- **Faz:** `web/` builda e passa `typecheck` com zero `any` não
  justificado; o gate Go não roda mais em PR que só toca `web/`, e
  vice-versa.
- **Não faz:** não escreve componente ou tela nenhuma.
- **Testes:** um teste Vitest trivial (`1+1`) só para provar que a esteira
  roda — será substituído por testes reais em `CI-3`+.
- **Verificação:** **execução real**, não leitura do YAML — abrir um PR de
  teste tocando só `web/**` e confirmar que `quality-gate` (Go) não
  dispara; abrir outro tocando só Go e confirmar que o job de `web/` não
  dispara. Mesma disciplina que a Fase 7 já exigiu para âncora de
  workflow.
- **Depende de:** `CI-1` (formalidade — nenhuma dependência técnica real).

### CI-3 — Design tokens

- **Arquivos:** `web/src/design/tokens.css` (custom properties — cor,
  tipografia, spacing, radius, sombra, transição, conforme a tabela
  proposta na rodada anterior desta conversa), `web/src/design/tokens.test.ts`.
- **Faz:** define os tokens com valores medidos, não estimados — cada par
  texto/fundo passa por um teste que calcula a razão de contraste (WCAG
  AA, 4.5:1 para texto normal) e falha se algum par não bater. **Depois**
  dos tokens fechados (ordem confirmada pelo usuário, sem mudar a issue):
  arquivo Figma criado a partir deles, espelho — não fonte. O Figma MCP
  está conectado e autenticado nesta sessão (verificado via `whoami`:
  `jonasleo92@gmail.com`, plano `Jonas Borges's team`), então esta parte
  é executável quando chegar a vez dela.
- **Não faz:** não cria o arquivo Figma **antes** do código, e não deixa
  os dois lados divergirem depois — se um token mudar em `tokens.css`
  após o arquivo Figma existir, o Figma precisa ser atualizado no mesmo
  `CI`, não num "depois" que nunca chega.
- **Testes:** `web/src/design/tokens.test.ts` — um teste por par
  texto/fundo definido, assertando contraste ≥ 4.5:1 (ou ≥ 3:1 para texto
  grande, conforme WCAG).
- **Verificação:** `npm test` (dentro de `web/`) para os tokens; para o
  Figma, o link do arquivo criado, conferido abrindo-o — não só o retorno
  da chamada MCP.
- **Depende de:** `CI-2`.

### CI-4 — Camada de API isolada e tipos gerados

- **Arquivos:**
  - `web/src/api/types.ts` (gerado — script `npm run generate:types`
    rodando `openapi-typescript ../docs/openapi.yaml -o
    src/api/types.ts`, não editado à mão).
  - `web/src/api/client.ts` — `fetch` wrapper com `credentials: 'include'`
    (obrigatório para o cookie ir junto), busca o token CSRF de `GET
    /v1/auth/csrf-token` uma vez (guardado em memória, nunca em
    `localStorage`/`sessionStorage`), anexa `X-CSRF-Token` em toda
    requisição mutadora — o que cobre automaticamente login/register
    também, já que o frontend nunca envia `Authorization` (é sempre
    caminho cookie, por desenho — ver `dual-auth-mode/plan.md` `CI-6`).
  - `web/src/api/errors.ts` — mapeamento por código: `401` limpa estado e
    redireciona; `503` **não** limpa estado, é retryable (ver `AM-5`
    equivalente do lado Go — `docs/openapi.yaml`'s `ServiceUnavailable`);
    `429` lê os headers padrão de rate limit para backoff; `409` de
    `PATCH /status` faz match de string nas duas mensagens conhecidas
    (`"invalid status transition"` vs. `"modified concurrently"` — ver
    `docs/openapi.yaml:743-750`); `400` devolve a mensagem humana como
    está (mapeamento campo-a-campo fica para `CI-8`, onde o formulário
    existe).
- **Faz:** toda chamada HTTP do app passa por aqui — nenhum componente
  visual importa `fetch` diretamente (verificável por lint rule/grep no
  CI).
- **Não faz:** não decide qual componente mostra qual erro — só
  classifica.
- **Testes:** `web/src/api/errors.test.ts` — um teste por código
  (`401`/`503`/`429`/`409` nas duas variantes/`400`), com `fetch` mockado;
  teste explícito provando que `503` **não** limpa o estado de sessão;
  teste provando que o header CSRF é anexado em `POST`/`PUT`/`PATCH`/`DELETE`
  e ausente em `GET`.
- **Verificação:** `npm test`.
- **Depende de:** `CI-2`, e de `dual-auth-mode` estar implementado (o
  endpoint `/auth/csrf-token` precisa existir de verdade para o app rodar
  contra a API real — os testes unitários com `fetch` mockado não
  precisam esperar isso).

### CI-5 — Primitives

- **Arquivos:** `web/src/components/{Button,TextField,Select,Checkbox,Modal,Toast,Skeleton}.tsx` + testes.
- **Faz:** cada primitive consome só tokens de `CI-3`; HTML semântico
  primeiro, ARIA só onde HTML não resolve; todos os estados de `Button`
  (default/hover/active/focus/disabled/loading); `prefers-reduced-motion`
  respeitado nas transições.
- **Não faz:** não cria componente reutilizável por antecipação — só os
  sete listados, o resto espera uma segunda necessidade real.
- **Pendência de `CI-3` a decidir aqui:** `color/border` (`#d9dde3`) tem
  1.36:1 de contraste contra `color/bg` — abaixo do 3:1 que WCAG 1.4.11
  (Non-text Contrast) exige quando essa cor é o único indicador visual do
  limite de um componente interativo no estado padrão (ex.: borda de
  `TextField` sem foco). Confirmado por segunda leitura independente do
  arquivo Figma (fórmula WCAG real, não estimativa — a maioria dos pares
  texto/fundo passa AA com folga, 5:1+; este é o único abaixo do limiar
  relevante). Se algum primitive usar `color/border` como único sinal do
  limite de um controle interativo, escurecer o token ou somar um segundo
  indicador (ex. `--shadow-sm`) nesse componente. Se o uso for só divisor
  entre seções (não interativo), não há problema — segue como está.
- **Testes:** `*.test.tsx` (RTL) por primitive — navegável por teclado,
  foco visível, atributos ARIA corretos, nenhum valor de cor/spacing fora
  de `tokens.css` (checável por um teste que falha se um estilo inline
  usar um valor literal em vez de `var(--token-*)`).
- **Verificação:** `npm test`.
- **Depende de:** `CI-3`.

### CI-6 — Fluxo de autenticação

- **Arquivos:** `web/src/features/auth/{RegisterPage,LoginPage,useAuth}.tsx` + testes.
- **Faz:** registro/login com React Hook Form + Zod; `GET /auth/me`
  hidrata a sessão no boot (única forma possível, já que o cookie é
  httpOnly); `429` no login mostra mensagem própria, distinta de
  credencial inválida; `401` (e-mail desconhecido vs. senha errada) nunca
  é diferenciado na UI, mesmo texto para os dois — preserva a
  anti-enumeração que o backend já garante; `logout-all` exposto como
  "sair de todos os dispositivos".
- **Não faz:** não implementa recuperação de senha (fora do contrato
  atual — não existe endpoint para isso).
- **Testes:** fluxo completo com `msw` ou `fetch` mockado — registro →
  login → `/me` hidrata → logout limpa estado; `429` distinto de `401`;
  mensagem de erro de login idêntica para e-mail/senha errados (teste que
  falharia se alguém "melhorasse" a mensagem para ser mais específica).
- **Verificação:** `npm test`.
- **Depende de:** `CI-4`, `CI-5`.

### CI-7 — Lista de tasks

- **Arquivos:** `web/src/features/tasks/{TaskList,useTasks}.tsx` + testes.
- **Faz:** quatro estados explícitos (loading com skeleton na forma do
  conteúdo real, empty, error, sucesso); paginação por infinite scroll
  com a técnica do item extra (`AM-4`: pede `limit+1`, se voltar `limit+1`
  itens descarta o último e sinaliza "há mais", nunca "página N de M");
  nenhum filtro de status/ordenação na UI que sugira abrangência que a
  API não tem (ordenação é sempre `created_at` — qualquer filtro é
  client-side, só sobre o que já carregou, e dito explicitamente na UI).
- **Não faz:** não implementa busca por título (não existe no backend, é
  item diferido em `docs/ARCHITECTURE.md`).
- **Testes:** os quatro estados renderizados distintamente; teste da
  técnica `limit+1` (mock retorna `limit+1` itens, componente mostra
  `limit` e sinaliza "carregar mais").
- **Verificação:** `npm test`.
- **Depende de:** `CI-6`.

### CI-8 — CRUD de task e transições de status

- **Arquivos:** `web/src/features/tasks/{TaskForm,TaskStatusControls}.tsx` + testes.
- **Faz:** criar/editar/excluir; tabela de transições legais espelhada no
  cliente (desabilita botão de transição ilegal), **e ainda assim** trata
  o `409` de verdade (o espelho pode ficar defasado — servidor é a
  autoridade; ver `AM-5`, o `409` é tratado por `CI-4`, aqui só se garante
  que o componente reage a ele, ex. reexibindo o formulário com a
  mensagem); exclusão exige confirmação explícita (modal, não só
  toast-com-desfazer); `priority` nunca envia `""` para "limpar" — omite
  o campo do corpo quando o usuário não escolheu nada.
- **Não faz:** não implementa nenhuma UI de "diff de concorrência" — o
  `409` de `PUT` não carrega versão nenhuma para comparar (ver achado em
  `context.md` — `Task.Version` é `json:"-"`), então a UI correta é
  genérica: "alguém salvou uma mudança ao mesmo tempo, tente de novo",
  nunca um comparador de campos.
- **Testes:** transição ilegal não é clicável **e** um `409` simulado (via
  mock, driblando o espelho client-side de propósito) ainda é tratado
  corretamente; confirmação de exclusão bloqueia o delete até confirmar;
  `priority` vazio não é enviado no corpo.
- **Verificação:** `npm test`.
- **Depende de:** `CI-7`.

### CI-9 — Anexos

- **Arquivos:** `web/src/features/attachments/{AttachmentList,Upload,Preview}.tsx` + testes.
- **Faz:** feature-detection via `attachments_enabled` de `GET /auth/me`
  (`dual-auth-mode` `CI-8` — não sondagem, não heurística); upload
  (`multipart/form-data`, parte `file`) com progresso, erro de
  quota/tipo tratados com mensagens distintas; download por
  `<a href="/v1/files/{key}" download>` simples — o cookie httpOnly vai
  junto automaticamente, sem `fetch`+object URL (ganho direto da Fase
  12); preview de imagem via `fetch`+object URL com `URL.revokeObjectURL`
  no unmount (obrigatório — `Content-Disposition: attachment` +
  `nosniff` impedem `<img src>` direto); limites de 10 MiB/arquivo e 500
  MiB/conta refletidos **antes** do envio, mas a allow-list é só
  orientação no cliente — o servidor decide pelos bytes, nunca pela
  extensão.
- **Não faz:** não tenta detectar anexos desligados por sondagem HTTP —
  isso foi descartado explicitamente (`AM-2` de `validation.md`).
- **Testes:** `attachments_enabled: false` esconde toda a UI de anexo (não
  só desabilita); upload com tipo rejeitado mostra mensagem distinta de
  quota excedida; preview revoga o object URL ao desmontar (teste que
  falharia com um vazamento de memória silencioso).
- **Verificação:** `npm test`.
- **Depende de:** `CI-8`, e de `dual-auth-mode` `CI-8`
  (`attachments_enabled`) estar implementado de verdade.

### CI-10 — Responsividade e acessibilidade verificadas

- **Arquivos:** ajustes de layout conforme necessário; nenhum arquivo novo
  fixo — depende do que a verificação encontrar.
- **Faz:** três larguras verificadas (mobile pensado desde o layout, não
  desktop encolhido); navegação completa por teclado em todo fluxo;
  auditoria automatizada (axe ou Lighthouse, via `@axe-core/playwright` —
  reaproveita a dependência de `CI-11`, não introduz uma segunda
  ferramenta) sem violação crítica.
- **Não faz:** não é um redesign — é verificação e ajuste do que `CI-3`–`CI-9`
  já produziram.
- **Testes:** o próprio resultado da auditoria automatizada, registrado no
  PR (não é um `go test`/`npm test` no sentido usual — é uma execução e um
  resultado colado, mesmo padrão de "visto rodando" que o resto do
  projeto já exige).
- **Verificação:** execução real do auditor + navegação manual só por
  teclado em cada tela — não leitura de código.
- **Depende de:** `CI-9` (precisa de todas as telas existindo para
  verificar de verdade).

### CI-11 — E2E dos fluxos reais

- **Arquivos:** `web/e2e/*.spec.ts`, `web/package.json` ganha
  `@playwright/test` como dependência real de projeto (não MCP, não
  presumido já instalado — ver `context.md` § "Ferramentas assumidas").
- **Faz:** registro, login, criação/edição/transição/exclusão de task
  (com confirmação), upload/download de anexo, logout, e o teste que mais
  importa: **`503` não desloga** — simulado derrubando o Postgres do
  `docker-compose` no meio de uma sessão válida e confirmando que a UI
  não redireciona para login. Roda contra a API real
  (`docker-compose up`), nunca contra mock — o ponto é validar a
  integração de verdade, um mock só confirmaria o que já foi assumido.
- **Não faz:** não substitui os testes unitários de `CI-3`–`CI-10` — E2E
  cobre fluxo, não cada estado isolado.
- **Testes:** os fluxos listados acima, um `spec.ts` por fluxo; zero erro
  de console durante a execução (checado explicitamente, não só "os
  asserts passaram").
- **Verificação:** `npx playwright test` contra `docker-compose up`
  rodando de verdade — instalar `@playwright/test` neste momento, não
  presumir que já está pronto (confirmado nesta sessão que não está).
- **Depende de:** `CI-10`.

## Mapa de dependências

```
CI-1 → CI-2 → CI-3 → CI-5 → CI-6 → CI-7 → CI-8 → CI-9 → CI-10 → CI-11
                  └→ CI-4 ─────────┘
```

## Entregáveis

- [ ] `docs/DECISIONS.md` — seção do frontend
- [ ] `web/` — scaffold, CI próprio verificado por execução real
- [ ] `web/src/design/tokens.css` + teste de contraste
- [ ] `web/src/api/` — client, tipos gerados, mapeamento de erro
- [ ] `web/src/components/` — sete primitives
- [ ] `web/src/features/{auth,tasks,attachments}/`
- [ ] Auditoria de a11y registrada no PR
- [ ] `web/e2e/` com Playwright real
- [ ] `README.md` — quickstart menciona `web/`
- [ ] `CHANGELOG.md` — primeira entrada do frontend
- [ ] Issue de débito técnico: código de erro legível por máquina para o
      `409` de `PATCH /status` (de `AM-5`, registrada, não esquecida)

## Riscos e como o plano os cobre

| Risco | Coberto por |
|---|---|
| Componente visual acaba fazendo `fetch` direto, quebrando o isolamento de `CI-4` | Lint rule/grep no CI de `web/` (a definir em `CI-2`) proibindo `fetch`/`axios` fora de `src/api/` |
| CSRF token guardado em `localStorage` por engano (reabre o risco que o cookie existe pra fechar) | Restrição herdada explícita + revisão de PR; candidato a um teste que falha se `localStorage`/`sessionStorage` forem tocados em `src/api/client.ts` |
| `409` tratado por match de string quebra silenciosamente se a mensagem do backend mudar | Teste de `CI-4` referencia o texto exato do `openapi.yaml`; qualquer mudança de mensagem no backend precisa passar pelo mesmo `docs/openapi.yaml` que `.claude/rules/api-contract.md` já exige manter sincronizado — a quebra apareceria no teste, não em produção |
| Filtro de CI (`paths-ignore`) silenciosamente não aplicado, como já aconteceu na Fase 7 | `CI-2` exige verificação por execução real, nos dois sentidos, antes de considerar o item pronto |
| Preview de anexo vaza `ObjectURL` | Teste explícito de revogação no unmount, em `CI-9` |
| Paginação por item extra falha silenciosamente se o backend mudar o comportamento de `limit` | Teste de `CI-7` fixa o comportamento esperado (`limit+1` → descarta o último) contra um mock fiel ao contrato documentado |
