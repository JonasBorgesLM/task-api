---
slug: web-frontend
stage: context
tier: full
sources_mtime:
  docs/DECISIONS.md: 2026-08-29T13:00:23Z
  docs/ARCHITECTURE.md: 2026-08-29T12:35:31Z
  CLAUDE.md: 2026-08-29T12:35:31Z
  docs/openapi.yaml: 2026-08-29T13:16:05Z
---

# web-frontend — Contexto

## Pedido

Fase 13: SPA em `web/`, mesmo repositório (monorepo), Vite + React +
TypeScript strict, consumindo a API via cookie httpOnly (Fase 12 —
`dual-auth-mode`), nunca `localStorage`. React Query e React Hook Form + Zod
onde fizerem sentido. Ordem: tokens de design → camada de API isolada →
primitives → telas. Nenhum componente visual faz HTTP. As issues #119–#129
existem de verdade no GitHub (criadas via `create-fase12-13-frontend.sh`,
confirmadas via `gh issue list`).

## Escopo

**Dentro:** scaffold `web/`, CI com filtro por caminho, tokens de design,
camada de API tipada com tratamento por código de status, primitives
acessíveis, fluxo de auth, CRUD de task, anexos, responsividade/a11y
verificadas, E2E contra a API real.

**Fora (por ora):** qualquer implementação de fato — este é o round de
plano. BFF/servidor Node (`docs/ARCHITECTURE.md`'s Future Improvements já
lista isso como "não justificado ainda", e nada aqui muda essa conclusão —
ver "Já é um item diferido?"). Filtro/busca de task no backend (também já
listado como diferido — a Fase 13 só pode fazer filtro client-side sobre o
que já carregou, ver issue #125 do rascunho).

## Superfície de código

Não há código de frontend existente — `web/` não existe (`find . -maxdepth 1
-iname web` não retornou nada). Toda a "superfície" desta mudança do lado
Go é infraestrutura de CI e nada além disso:

| Arquivo | Papel na mudança |
|---|---|
| `.github/workflows/ci.yml` | hoje sem `paths`/`paths-ignore` algum — roda inteiro em qualquer push/PR para `main`/`develop`. Precisa ganhar `paths-ignore: ['web/**']` para não rodar o gate Go em PR que só toca `web/` |
| `.github/workflows/*.yml` (novo) | workflow novo para o frontend, `paths: ['web/**']` |
| `docker-compose.yml` | portas em uso: `5432` (postgres), `9000`/`9001` (minio), `8080` (api), `8082` (swagger ui) — `5173` (porta padrão do Vite dev server) está livre, sem conflito |
| `docs/openapi.yaml` | fonte de tipos do frontend (via geração, não à mão — ver "Achados") |

## Decisões registradas que isto toca

- Depende inteiramente de `dual-auth-mode` (Fase 12) para a decisão de
  cookie httpOnly — **não pode ser implementado antes dela**, e o próprio
  rascunho já reconhece isso (#119 "Depende de: #112", #122 "Depende de:
  #118").
- `docs/ARCHITECTURE.md` § "Future Improvements" — "BFF (Backend-for-Frontend)
  layer": *"not justified yet with a single core resource... the natural
  next architectural step once... a web client and a mobile client need
  meaningfully different payload shapes."* A SPA Vite falando direto com a
  API (sem BFF) é exatamente o que este texto já antecipa como correto para
  agora — **confirma** a escolha do rascunho, não contradiz.
- `docs/ARCHITECTURE.md` § "Future Improvements" — "Task filtering and
  search" listado como diferido no backend. Issue #125 do rascunho já
  reconhece isso corretamente ("filtro e ordenação alternativos só existem
  client-side... seja explícito sobre essa limitação").

## Invariantes aplicáveis

Nenhuma de `CLAUDE.md`/`.claude/rules` se aplica diretamente a TypeScript —
todas as regras existentes são Go-específicas (layering, domain errors,
repository parity, etc.). Isto **não** significa "vale tudo": significa que
este projeto ainda não tem um equivalente de `.claude/rules/` para o
frontend, e criar um (ou uma seção nova em `CLAUDE.md`) é provavelmente
parte do que #119/#120 deveriam produzir, não deixar para depois.

## Achados que complementam ou contradizem o rascunho

### 1. `PUT /tasks/{id}`'s 409 não é "dado obsoleto" — é uma corrida real, e o cliente nunca vê a versão

`internal/task/task.go:53`: `Version int \`json:"-"\`` — o campo de
concorrência otimista **nunca é serializado em resposta nenhuma**.
`internal/task/service.go:201-224` (`UpdateTask`) confirma por quê: o
próprio `Service` faz `FindByID` internamente e escreve de volta com a
versão que *ele mesmo* acabou de ler — o cliente HTTP nunca envia nem
recebe uma versão (`UpdateTaskRequest` não tem esse campo, e
`Service.UpdateTask` nem recebe um parâmetro de versão).

**Consequência para #126:** o `409` de `PUT /tasks/{id}` só é alcançável
por uma corrida genuína entre duas escritas quase simultâneas (a janela
entre o `FindByID` interno e o `Update` — não pelo padrão comum de "eu
carreguei isto há 5 minutos e está desatualizado"). Não há dado algum para
o frontend montar uma UI de "isto mudou desde que você carregou, aqui está
o que mudou" — não existe versão para comparar. O tratamento correto é
genérico: reexibir o formulário, mostrar "alguém salvou uma mudança ao
mesmo tempo, tente de novo" (talvez com um retry automático de uma
tentativa), nunca um diff. Isto é mais simples do que #126 presumia, e
vale registrar por quê — para quem ler o código depois não presumir que dá
para pedir a versão de volta.

### 2. A ambiguidade do `404` de anexo (#127) não tem solução client-side — precisa de mudança no backend

Conferido: nem `/health`, nem `/health/ready`, nem `/debug/vars` publicam
qualquer sinal de `ATTACHMENT_STORAGE_DIR` estar configurado. `404` de
`GET/POST /v1/tasks/{id}/attachments` é **literalmente idêntico**, byte a
byte, entre "anexos desligados" e "task não é sua" — mesmo `{"error": "task
not found"}`. Não existe sondagem client-side que distinga os dois: um
`GET` contra qualquer endpoint de anexo de uma task que existe e é do
usuário, se anexos estiverem desligados, dá exatamente o `404` que uma task
alheia daria.

**Isto não é uma decisão que a Fase 13 possa tomar sozinha** (diferente do
que #127 sugere — "decida como detectar"). A única forma real de resolver é
o backend publicar um sinal explícito (ex.: um campo em `GET
/health/ready`, ou um novo `GET /v1/config`/`GET /v1/auth/me` ganhando
`attachments_enabled: bool`). Isto deveria ser uma issue nova de Fase 12
(ou uma 12.x adicional), não uma decisão de frontend — o rascunho atual não
tem essa issue.

### 3. Tipos gerados, não escritos à mão — o rascunho já pede isso (#122), mas não escolhe a ferramenta

`docs/openapi.yaml` já é tratado como fonte de verdade em todo o resto do
projeto (`.claude/rules/api-contract.md`). Gerar tipos TypeScript dele
(`openapi-typescript` é o padrão de fato do ecossistema Vite/React, zero
runtime, só tipos — compatível com "sem biblioteca além do necessário") é
consistente com essa convenção; escrever os tipos à mão seria o mesmo erro
de categoria que o projeto já evita no lado Go (fonte única de verdade).
Não estava decidido no rascunho qual ferramenta.

## Contrato afetado

Nenhuma mudança no `openapi.yaml` é *causada* por esta fase — é
inteiramente um consumidor. A exceção é o achado #2 acima, que **pede** uma
mudança de contrato, mas essa mudança pertence a Fase 12/uma fase própria,
não a esta.

## Superfície de testes

- Não existe hoje. `npx playwright --version` funciona neste ambiente
  (`1.61.1`, via download ad-hoc do `npx`) mas **não há MCP de Playwright
  conectado a esta sessão** e **não há `package.json`/dependência de
  projeto** — rodar E2E de verdade (#129) significa instalar
  `@playwright/test` como dependência de `web/`, não presumir que a
  ferramenta "já está pronta".
- **Skills de design citadas no pedido do usuário (Impeccable, Emil
  Kowalski, Taste) não existem neste ambiente** — nem em
  `.claude/skills/` (nem no repo, nem em `~/.claude/skills`), nem como
  plugin instalado (`~/.claude/plugins` só tem `gopls-lsp`). Reportado
  explicitamente conforme pedido — nada foi simulado.
- Este repositório tem cinco "readers" documentados em `CLAUDE.md`
  (`decisions-reader` etc.) e quatro skills de pipeline
  (`change-context`/`change-validate`/`change-plan`/`implement-change`) —
  **também não estão disponíveis como agentes/skills invocáveis nesta
  sessão** (`Agent`/`Skill` retornam "not found" para todos). Este
  documento foi montado lendo as fontes diretamente, seguindo o mesmo
  procedimento que os readers documentam, mas sem o atalho da ferramenta.
  Ver nota equivalente em `dual-auth-mode/context.md`.

## Artefatos que precisam mudar junto

- [ ] `docs/DECISIONS.md` — nova seção com as decisões de arquitetura do
      frontend (#119 do rascunho já pede isto: Vite não Next.js, monorepo,
      esquema de versionamento, cookie httpOnly)
- [ ] `README.md` — quickstart precisa mencionar `web/` (como rodar,
      como os dois lados se relacionam)
- [ ] `CLAUDE.md` — provavelmente ganha uma seção equivalente à Go, ou
      aponta para um `web/CLAUDE.md`/`.claude/rules/` próprio
- [ ] `docs/ARCHITECTURE.md` — Future Improvements perde a entrada de BFF
      como "ainda sem justificativa" só se um caso de uso mobile aparecer;
      por ora nada muda ali além de talvez uma nota cruzada
- [ ] `.env.example` — `VITE_*` env vars do frontend não pertencem ao
      `.env.example` do backend; `web/.env.example` próprio
- [ ] `CHANGELOG.md` — primeira entrada do frontend

## Já é um item diferido?

Parcialmente. "BFF layer" está em Future Improvements e **não** é o que
esta fase propõe (SPA direto, sem servidor Node) — a leitura correta é que
o texto já existente **valida** essa escolha, não que a Fase 13 feche o
item (o item continua deferido para quando houver >1 cliente consumidor
com payloads divergentes). "Task filtering and search" também está listado
e a Fase 13 explicitamente não tenta resolvê-lo no backend — só reconhece
o limite.

## Perguntas em aberto

1. Ver `dual-auth-mode/context.md` item 1 — issues 12.x/13.x não existem
   no GitHub ainda.
2. Esquema de versionamento do frontend (#119 do rascunho: linha `v1.x.x`
   compartilhada vs. `web/vX.Y.Z` própria) — a analogia com o crier no
   rascunho não se aplica tecnicamente: o crier é um **módulo Go externo**,
   em repositório próprio, com sua própria necessidade de tag por causa do
   `go.mod`/pseudo-versão. `web/` é um pacote npm dentro do **mesmo**
   repositório, sem esse requisito técnico — a escolha aqui é
   inteiramente de convenção do projeto, não uma imposição de ferramenta
   como é para o crier. Precisa de decisão própria, não emprestada.
3. Geração de tipos do `openapi.yaml` — confirmar `openapi-typescript` ou
   outra ferramenta.
4. O sinal de "anexos habilitados" (achado #2 acima) precisa virar uma
   issue de backend antes de #127 poder ser implementada de verdade — sem
   isso, #127 fica bloqueada em uma decisão que não é dela.
5. Ferramentas assumidas pelo pedido original (Playwright MCP, skills de
   design) não existem neste ambiente — local de decisão: usar
   `@playwright/test` como dependência normal de projeto (funciona, só não
   é MCP) e não usar as skills de design citadas (não existem) — a menos
   que o usuário as instale/aponte para onde estão.
