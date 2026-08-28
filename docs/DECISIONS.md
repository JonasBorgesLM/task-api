# Decisões de arquitetura — task-api

Este arquivo registra decisões que não são óbvias só lendo o código ou a
issue isolada. Antes de implementar qualquer issue do backlog, leia as
seções relevantes. Se uma issue parecer contradizer uma decisão aqui
registrada, pare e pergunte em vez de escolher por conta própria.

---

## Autenticação: token via header, não cookie de sessão

Login retorna um token opaco no corpo da resposta (`{"token": "..."}`), não
um cookie. O cliente envia `Authorization: Bearer <token>` em toda
requisição autenticada.

**Por quê:** a alternativa considerada foi cookie de sessão, mas isso abre
superfície de CSRF (o browser anexa cookie automaticamente em requisição
cross-site; não anexa header customizado). Como a API é consumida por
clientes não-browser (scripts, outros serviços) e o roadmap original já
apontava Bearer/API key, cookie era a escolha errada. **CSRF não se aplica
a este modelo de auth** — não implementar `csrf` do moat aqui.

**Trade-off aceito:** se um frontend futuro guardar o token em
`localStorage`, ele fica exposto a roubo via XSS. Isso é decisão do
projeto de frontend quando existir, não deste backend.

---

## Storage de anexos: duas fronteiras, ordem de escrita é escolha

`Repository` (metadado no Postgres) e `BlobStore` (bytes) são interfaces
separadas. Na criação de um anexo, **os bytes são gravados antes da linha
de metadado.**

**Por quê:** a ordem inversa (metadado antes do blob) deixaria, no caso de
falha no meio do caminho, uma linha apontando para um arquivo inexistente —
isso vira um download que retorna 500 permanentemente, sem indicar a causa
real. Bytes-primeiro, no pior caso, deixa um blob sem referência no banco —
custa espaço em disco, nada mais. É o modo de falha mais barato dos dois.

Essa mesma lógica de custo se aplica ao **delete**: ver seção "Blobs
órfãos" abaixo.

---

## Content-type de upload: detectado, não declarado

A validação de tipo de arquivo usa `http.DetectContentType` (sniffing dos
bytes), não o header `Content-Type` que o cliente envia na requisição.

**Por quê:** o cliente escreve o header que quiser — usar o valor
declarado pra decidir uma allow-list de segurança tornaria essa allow-list
decorativa (o atacante simplesmente declara o que a lista aceita).

`text/html` foi excluído da allow-list **de propósito**: servido a partir
da mesma origem da API, um arquivo `text/html` rodaria como script
same-origin. Não é omissão, é decisão.

---

## Path traversal: containment é responsabilidade do store, não da chave

As chaves de anexo hoje são UUIDs gerados pelo servidor — nenhuma delas
pode, na prática, escapar do diretório de armazenamento. Mesmo assim, o
`pathguard` (moat) está na fronteira do `BlobStore`, não confiando nessa
propriedade das chaves.

**Por quê:** "hoje nenhuma chave escapa" é verdade sobre a origem atual das
chaves, não uma garantia do tipo/assinatura de função. Um refactor futuro
que trocasse a origem das chaves (ex. aceitar chave externa) não deveria
silenciosamente reabrir a vulnerabilidade. A defesa vive onde a violação
aconteceria, não onde a chave é gerada hoje.

**Cobertura de teste:** o fuzz do moat (`FuzzResolve`) testa o `pathguard`
isoladamente. Um fuzz próprio do task-api testa se *o nosso código de fato
chama* `guard.Open` em vez de `os.Open` direto — um refactor que trocasse
essa chamada deixaria o fuzz do moat verde e o nosso vermelho. Os dois
testes são necessários; nenhum substitui o outro.

**Onde cada um roda:** o fuzz autoral roda no CI do task-api
(`Fuzz (path containment)`, 45s). O `FuzzResolve` roda no CI do **moat**
(job `short fuzz run`, 30s), a cada mudança lá — que é quando o
containment do `pathguard` pode de fato regredir.

Isso não é lacuna, é a divisão correta. Uma PR do task-api não pode
alterar o `pathguard`, então refuzzá-lo aqui rodaria contra uma tag cujo
fuzz já passou, sem nunca poder falhar por algo nosso. Detalhe técnico
que a investigação produziu e vale preservar: `go test -fuzz` só executa
alvo do módulo **principal** do build, então rodar o `FuzzResolve` daqui
exigiria checkout separado do moat — não basta a dependência estar no
`go.sum`.

O risco que sobra é subir o moat para uma versão cujo CI não passou. Se
isso vier a preocupar, a resposta é uma verificação de versão barata, não
um refuzz.

---

## Blobs órfãos após delete de task

Deletar uma task remove os anexos por cascade no banco, mas os bytes
correspondentes não são removidos automaticamente do `BlobStore` — as duas
fronteiras (Repository/BlobStore) são independentes também na deleção.

**Decisão tomada:** coletor de órfãos periódico, não delete síncrono —
pelo mesmo raciocínio de custo da ordem bytes-primeiro. Um blob órfão
custa só disco; não vale acoplar o sucesso do delete da task ao sucesso
do delete de arquivo.

Implementado em `Service.CollectOrphans`, rodado pelo
`runPeriodicCleanup` do `cmd/api`.

**O período de carência não é opcional, e é a parte que importa.** Como o
upload grava os bytes antes da linha de metadado, existe uma janela em
que um upload perfeitamente saudável é indistinguível de um órfão: um
arquivo que nenhuma linha referencia. Um coletor sem carência correria
contra todo upload em voo e apagaria alguns — de forma intermitente e sob
carga, que é o pior jeito de descobrir. `ATTACHMENT_ORPHAN_MIN_AGE` (1h
por padrão) tem que exceder o maior intervalo plausível entre os dois
passos, e `CollectOrphans` recusa idade não positiva em vez de deixar
alguém abrir mão da margem.

O coletor também só apaga o que consegue identificar positivamente como
seu: `UnreferencedKeys` filtra os candidatos a storage keys bem formadas,
então um arquivo estranho no diretório sobrevive. Em código que apaga
coisas, toda ambiguidade resolve para manter.

---

## Cliente de object storage: minio-go, não aws-sdk-go-v2

**Os motivos que se sustentam:**

- **Um require direto**, não vários. O `minio-go` é um módulo só no
  `go.mod`; o `aws-sdk-go-v2` exige ao menos `service/s3` e `config`.
- **Agnóstico de provedor por construção.** O mesmo cliente fala com
  MinIO em desenvolvimento e com S3 real em produção, sem código
  condicional — nenhum dos dois ambientes exercita um caminho que o
  outro nunca roda.
- **Puro Go**, que é o que mantém o build estático em `scratch`.
- **IAM não é requisito.** O SDK da AWS ganharia em roles via metadata /
  IRSA em EKS. Autenticação por access key/secret basta aqui. **Se o
  alvo virar EKS com IRSA, esta decisão merece revisita** — é o único
  ponto em que o SDK da AWS é claramente superior.

**Um motivo que NÃO se sustenta, registrado para ninguém repetir.** A
formulação inicial citava o `minio-go` como mais enxuto. Medindo os
módulos que de fato contribuem pacotes para o build (não o grafo inteiro
do `go list -m all`): `minio-go` traz **19**, `aws-sdk-go-v2` (s3 +
config) traz **18**. São equivalentes, e o `minio-go` é marginalmente
maior. Tamanho de dependência não foi o critério.

---

## Dois backends de anexo, e por que ambos existem

`ATTACHMENT_STORAGE_DIR` (filesystem) e `ATTACHMENT_S3_ENDPOINT` (object
storage) são alternativas, não complementos — configurar os dois é
recusado na subida.

**Por quê ambos:** o filesystem não precisa de serviço nenhum, o que é o
que torna desenvolvimento local e a suíte de testes baratos. Ele não pode
respaldar um deploy que precisa sobreviver a rolling update: o disco
local de um pod não é compartilhado com o pod que o substitui, e some se
o pod for reagendado em outro nó.

**Por que recusar os dois juntos em vez de escolher um:** o que perdesse
guardaria arquivos que o processo em execução não enxerga, e isso
apareceria como anexo sumido — não como erro de configuração.

**O que garante que não divirjam:** `runBlobStoreContract` roda o mesmo
conjunto de asserções contra as duas implementações. Uma asserção que só
existisse para o filesystem seria uma que o S3 pode falhar em silêncio,
em produção, onde ninguém está olhando.

---

## Configuração de storage: obrigatória, sem default

`ATTACHMENT_STORAGE_DIR` não tem valor padrão. Sem ela definida, as rotas
de anexo simplesmente não são registradas — o processo sobe normalmente e
essas rotas respondem 404. Se a variável estiver definida mas apontar para
um diretório inexistente, aí sim a subida falha.

**Por quê:** a imagem de produção é um binário estático rodando em
`scratch`, sem filesystem gravável por padrão. Um caminho default
produziria um deploy que aceita a rota de upload e falha em toda
requisição — erro silencioso e distante da causa. Falhar rápido na
inicialização é a escolha consistente com o resto do projeto (`config.Load()`
já valida tudo antes de subir o servidor).

---

## Topologia de deploy: instância única, em Kubernetes

O deploy roda **uma réplica**, não múltiplas. Decisão de infraestrutura,
revisitável — não uma limitação de código.

**O que essa decisão já resolveu, sem exigir mudança:**
- Rate limit em `MemoryStore` (moat) é suficiente — "por processo" e
  "global" coincidem com uma réplica só. Não usar `redisstore`.
- `DB_AUTO_MIGRATE=true` é seguro (sem risco de duas instâncias competindo
  pra migrar ao mesmo tempo).

**O que essa decisão reabre, apesar de "uma réplica":** rollout do
Kubernetes por padrão sobe o pod novo antes de derrubar o antigo, mesmo
com `replicas: 1` — ou seja, durante a troca existem brevemente dois pods.
Isso significa:
- **Storage de anexos não pode ser disco local do pod.** Pods podem ser
  agendados em nós diferentes, e o disco efêmero de um pod não é visível
  pro outro durante a transição. Decisão de storage compatível com isso é
  tratada em issue própria (S3BlobStore reaproveitando a interface
  `BlobStore` existente, ou volume `ReadWriteMany` — ver issue de decisão).
- **`readinessProbe`** deve apontar para `/health/ready` (rota já
  existente) para o Kubernetes saber quando o pod novo pode receber
  tráfego.
- **`terminationGracePeriodSeconds`** precisa ser maior que o tempo real
  do graceful shutdown já implementado no servidor, senão o Kubernetes
  mata o processo à força antes dele terminar de atender quem já estava
  em atendimento.

---

## Versionamento: `/v1` cobre o contrato, não a superfície operacional

Toda rota que um cliente programa contra vive sob `/v1`. As health probes
(`/health`, `/health/ready`) e o `/debug/vars` **não**.

**Por quê:** uma probe de orquestrador não negocia versão de API — ela é
configurada uma vez, num manifest, por quem opera o serviço e não por quem
o consome. Este mesmo documento compromete o `readinessProbe` com
`/health/ready`; versioná-lo obrigaria a reeditar os manifests a cada
versão da API, sem benefício para ninguém. O mesmo vale para o
`/debug/vars`: scraper de métricas é operação, não cliente.

**Os caminhos sem prefixo não são servidos.** Não há mount duplo nem
redirect. Um alias de compatibilidade tornaria o prefixo decorativo — os
clientes continuariam contra os caminhos não versionados, e o primeiro v2
de verdade quebraria exatamente quem o versionamento deveria proteger.

**Os handlers não sabem do prefixo.** Eles registram `POST /tasks`, e o
composition root monta o sub-mux com `http.StripPrefix`. Um v2 é um
segundo mount, não uma edição em todo `RegisterRoutes` do código.

---

## govulncheck no CI: fail-closed no alcançável

A etapa de `govulncheck` falha o build quando encontra vulnerabilidade que o
código **de fato alcança**. Presença no grafo de dependências, sem chamada,
não bloqueia.

**Por quê:** é o comportamento padrão da ferramenta, e é o único que
distingue risco de ruído. Medido neste repositório: com apenas o achado de
`golang.org/x/crypto/openpgp` (não importamos `openpgp`, usamos `bcrypt`, e
o pacote não tem correção por ser não mantido por design), o comando sai
com **exit code 0**. Com as quatro vulnerabilidades da stdlib que existiam
antes do Go 1.26.6, saía com **exit code 3**. Os dois lados foram
verificados por execução, não por leitura da documentação.

**Trade-off aceito:** um advisory contra a biblioteca padrão pode travar
merges até existir release do Go que o corrija — parte do ritmo de merge
passa a depender do calendário do Go, não só do trabalho do time. Aceito em
troca de não deixar vulnerabilidade alcançável entrar em `main` em
silêncio. As quatro que o projeto carregou até o Go 1.26.6 foram
encontradas rodando a ferramenta à mão, porque nada no pipeline procurava.

**Sem allowlist, e isso é deliberado.** O `govulncheck` não tem mecanismo
nativo de supressão. Se um dia aparecer achado alcançável sem correção
upstream, a saída não será uma flag: será decidir entre esperar o upstream,
trocar a dependência, ou filtrar a saída JSON. Melhor decidir com o caso
concreto na mão do que construir o mecanismo antes de existir o problema.

---

## Drain antes do shutdown: o processo espera, não o orquestrador

O processo continua servindo por `HTTP_PRE_SHUTDOWN_DELAY` depois do
SIGTERM, antes de recusar conexões novas. Padrão 0 — irrelevante para
execução local ou docker-compose; 5s nos manifests de Kubernetes.

**Por quê:** o Kubernetes remove o pod terminando dos endpoints do
Service e manda SIGTERM **ao mesmo tempo**, e propagar essa remoção leva
tempo (o kube-proxy reescreve regras em cada nó). Nessa janela o tráfego
ainda chega aqui. Um processo que para de escutar no instante do sinal
recusa essas requisições — e o rolling update configurado para zero
downtime derruba um punhado assim mesmo.

**Isto não é teórico, e é o exemplo do princípio abaixo.** Com
`maxUnavailable: 0`, readiness em `/health/ready` e 30s de grace period,
o manifest parecia correto. O `k8s/rollout-test.sh` mediu **3 de 654
requisições perdidas**. Com o drain: **0 de 732**. Nenhuma leitura do
YAML teria mostrado isso.

**Por que no processo e não num preStop hook.** O remédio usual é um
`preStop` rodando `sleep`, e a imagem é um binário estático em `scratch`
— sem shell, sem `sleep`, nada para exec. Mas o lugar também é mais
honesto: o processo sabe que está desligando, e a espera é parte de como
ele desliga.

**Restrição a manter:** o `terminationGracePeriodSeconds` do orquestrador
precisa cobrir este atraso **mais** o `HTTP_SHUTDOWN_TIMEOUT`. Hoje
5 + 10 < 30. Aumentar qualquer um dos dois sem aumentar o grace period é
como isso volta a quebrar, em silêncio.

---

## Princípio geral de validação

Decisões e correções neste projeto são verificadas pela execução real, não
pela leitura da própria intenção da mudança. Exemplo já ocorrido: uma
correção de CI foi dada como concluída porque o alvo de fuzz passava
localmente e a edição parecia certa — mas o workflow nunca chegou a
executá-lo, por uma âncora de YAML que não bateu. Só foi considerada
resolvida depois de ver a execução real no pipeline (contagem de
iterações, tempo, resultado). Aplique o mesmo padrão em qualquer issue
que envolva CI, deploy, ou qualquer configuração que se pretende validar:
não feche por ter editado o arquivo certo, feche por ter visto rodar.

---

## Quota de anexos: por usuário, em bytes, checada antes do upload

`ATTACHMENT_MAX_BYTES_PER_USER` (default 500 MiB) limita o total de bytes
que um usuário pode ter armazenado em anexos, somado através de todas as
suas tasks.

**O eixo — por usuário, não por task, não por contagem:** por task não
resolve o problema real — nada impede um atacante de criar mais tasks e
continuar enchendo o storage, cada uma dentro do próprio limite. Contagem
de anexos não impede o cenário real (poucos arquivos grandes). Bytes
totais por usuário é a métrica que a própria motivação da issue nomeia:
"encher o storage".

**Quando a checagem roda:** antes de qualquer byte do upload ser lido —
`Repository.TotalBytesForUser` é consultado primeiro, e se o total atual
já estiver no limite (`>=`), a requisição é recusada sem tocar no corpo.
Verificado com um `io.Reader` que falha o teste se `Read` for chamado.

**Por que a checagem usa o total *antes* do upload, não *depois*:** o
tamanho real de um upload só é conhecido quando `BlobStore.Put` termina
de ler o stream (`Content-Length` é declarado pelo cliente e pode
mentir — a mesma razão pela qual o limite de um único upload
(`ATTACHMENT_MAX_BYTES`) já não confia nesse header). Checar depois
significaria gravar o blob primeiro e apagá-lo se a quota estourou —
puro desperdício de I/O para um teto que só precisa impedir abuso
sustentado, não cravar um número exato de bytes.

**Trade-off aceito:** um único upload aceito pode levar o total até
`ATTACHMENT_MAX_BYTES` acima da quota — o overshoot máximo é limitado
pelo teto de um upload individual, não é ilimitado. E duas requisições
concorrentes do mesmo usuário, cada uma vendo o total antes da outra
commitar, podem ambas passar e juntas ultrapassar a quota por um
upload a mais do que o previsto — aceito pela mesma razão: é um teto
operacional contra abuso sustentado, não uma invariante financeira que
precise de bloqueio.

**A mensagem de erro não revela uso de outro usuário** — garantido por
construção, já que `TotalBytesForUser` é escopado ao próprio `userID` da
chamada; nunca há outro usuário para vazar.

---

## Limite de sessões: teto com evicção da mais antiga

`AUTH_MAX_SESSIONS_PER_USER` (default 10) bounds quantas sessões de um
usuário ficam vivas ao mesmo tempo. Ao exceder, `CreateSession` evict a
mais antiga (por `CreatedAt`) em vez de recusar o novo login.

**Evicção, não recusa — e por quê:** este projeto não tem (e a issue não
pede) um endpoint para listar sessões ativas. Recusar o login excedente
deixaria o usuário sem saída clara — a única opção seria esperar uma
sessão expirar (até `AUTH_SESSION_TTL` inteiro) ou usar `logout-all`, que
sequer existia antes desta decisão. Evicção nunca trava um login
legítimo: o pior caso é o dispositivo mais antigo perder a sessão
silenciosamente, o mesmo comportamento que serviços reais (bancos, redes
sociais) já usam para "você foi desconectado em outro lugar".

**`POST /v1/auth/logout-all` entra no escopo**, e é o que de fato resolve
a segunda motivação da issue — "não há como um usuário encerrar sessões
que não sejam a atual" — que a evicção sozinha não cobre satisfatoriamente
(evicção é passiva e só ajuda se o dono continuar logando normalmente em
outro lugar; um token roubado usado sem mais logins novos nunca seria
evictado). `LogoutAll` remove **todas** as sessões do usuário,
**incluindo a que fez a chamada** — "sair de todos os lugares" é o
padrão real de segurança para quem suspeita de um token vazado, e deixar
a sessão atual viva contradiria o propósito.

**Implementado no `Repository`, com uma transação *e* um advisory lock —
a transação sozinha não bastou, e isso só foi descoberto porque o CI
achou o que o teste local não achava.**

Uma primeira versão só com a transação (`BEGIN`; `INSERT`; `DELETE ...
NOT IN (SELECT ... ORDER BY created_at DESC LIMIT $2)`; `COMMIT`) passou
localmente, repetidamente, no teste de concorrência abaixo. Um
experimento manual foi além: uma versão deliberadamente *sem* transação
nenhuma, com um delay artificial de 20ms entre o `INSERT` e o `DELETE`
para alargar a janela de corrida, também **nunca produziu overshoot**
localmente — a hipótese testada era que a consulta de evicção é
auto-corretiva (quando roda, aplica a regra contra o estado real da
tabela naquele momento, não contra um snapshot antigo), e o experimento
parecia confirmar isso.

**Essa conclusão estava incompleta, e o CI provou isso**: a mesma versão
com transação, rodada contra o runner do GitHub Actions — rede real
entre goroutines, não localhost — deixou **7 sessões, não 3**, na
primeira execução. A causa real: o isolamento padrão do PostgreSQL,
`READ COMMITTED`, deixa cada transação enxergar apenas o que outras já
commitaram *antes do seu próprio `SELECT` rodar*, mais sua própria linha
recém-inserida. Dez logins chegando perto o suficiente uma da outra
fazem cada transação ver **uma única sessão — a sua própria** — concluir,
corretamente segundo essa visão limitada, que não há nada para evictar
(1 <= 3), e todas commitam. Nenhuma transação nunca chega a ver o
trabalho das outras nove. Isso não é o mesmo cenário do experimento
manual (uma única goroutine com delay, sem outras rodando em paralelo de
verdade) — é uma classe de corrida diferente, entre transações
concorrentes de verdade, que só apareceu com latência de rede real.

A correção: `SELECT pg_advisory_xact_lock(hashtext(user_id))` como
primeira instrução dentro da transação, serializando toda
`CreateSession` concorrente para o **mesmo** usuário (nunca bloqueia
usuários diferentes entre si) — liberado automaticamente no
commit/rollback pela variante `_xact_`, sem precisar de unlock
explícito. Revalidado: 30 execuções consecutivas do teste de
concorrência, localmente, todas com contagem final exata — e a suíte
completa de CI, verde.

**A lição registrada, não só a correção:** um teste de concorrência que
só roda localmente pode passar por acaso, mascarando uma corrida real
que só se manifesta sob latência de rede genuína. O `TestPostgres_
ConcurrentCreateSession_NeverExceedsCap` continua na suíte porque ainda
é útil — mas o comentário do teste registra explicitamente que ele não
pode ser confiado sozinho para pegar essa classe de regressão em
qualquer máquina; é o CI, não a máquina de quem escreveu o código, que
efetivamente encontrou o bug aqui.

**Teste de concorrência** (`TestPostgres_ConcurrentCreateSession_
NeverExceedsCap`), no padrão de `TestConcurrentUpdate_LosersGetErrConflict`:
10 goroutines reais, um gate de início, `-race`, todas criando sessão para
o mesmo usuário ao mesmo tempo — a contagem final nunca excede o teto.

**Migration nova** (`0008_add_sessions_user_id_created_at_index`): índice
composto `(user_id, created_at)`, pela mesma razão de
`idx_tasks_user_id_created_at_id` — a consulta de evicção filtra por
`user_id` **e** ordena por `created_at` juntos; o índice simples que já
existia (`idx_sessions_user_id`, de `0006_add_sessions_indexes`) não serve
os dois ao mesmo tempo.

---

## Delete de anexo: síncrono, não só o coletor

`DELETE /v1/files/{key}` remove a linha de metadado e o blob **na mesma
requisição** — não apenas a linha, deixando o blob para
`Service.CollectOrphans` (#46) encontrar no próximo ciclo.

**Os dois caminhos considerados:**

1. **Síncrono** (escolhido): `Service.Delete` remove a linha via
   `Repository.Delete`, depois chama `BlobStore.Delete` na mesma
   requisição, best-effort.
2. **Só o coletor**: `Service.Delete` só remove a linha; o blob vira
   candidato a órfão e some quando `CollectOrphans` rodar — hoje até
   `ATTACHMENT_ORPHAN_MIN_AGE` (1h de default) depois.

**Por quê o síncrono:** um `DELETE` que só *agenda* a remoção por até uma
hora não corresponde ao que o endpoint promete a quem o chama. O coletor
existe para cobrir o rastro de **falhas** — um upload que gravou o blob e
morreu antes da linha, um delete cujo passo de blob falhou — não para ser
o caminho normal de uma operação que o próprio `Service` já tem acesso
completo a executar de ponta a ponta. É a mesma distinção que já existe no
próprio código: `CollectOrphans`' doc comment explica que o *cascade* de
task (`ON DELETE CASCADE` no SQL) não pode limpar blobs porque nada no
caminho do SQL alcança o filesystem — mas `Service.Delete` não tem essa
restrição arquitetural: ele já segura tanto `Repository` quanto
`BlobStore`, exatamente como `Upload` segura os dois.

**Ordem dentro do síncrono — metadado primeiro, blob depois — é o espelho
deliberado de `Upload`:** `Upload` grava bytes antes da linha porque a
ordem inversa deixaria uma linha apontando para um arquivo que nunca foi
escrito — um download que 500 pra sempre, sem indicar por quê. `Delete`
inverte isso pela mesma razão invertida: remover a linha primeiro faz uma
falha no passo seguinte (apagar o blob) deixar **um arquivo órfão**, que
custa disco e nada mais — a alternativa (blob primeiro) deixaria, no
mesmo cenário de falha, uma linha apontando para um arquivo que já não
existe, exatamente a forma de referência quebrada que a ordem de `Upload`
existe para evitar.

**A falha no delete do blob é best-effort, não propagada ao chamador:**
uma vez que a linha foi removida, o anexo já está fora do alcance do
chamador (`Download`/`ListByTask` já não o veem) — reportar a requisição
como falha nesse ponto seria enganoso, o cliente pediu "remova isto" e o
efeito observável já aconteceu. A falha vira um blob órfão, que
`CollectOrphans` recolhe no próprio ciclo — o mesmo mecanismo de segurança
que já protege o lado inverso da falha em `Upload`.

**Idempotência — o que "coerente com `BlobStore.Delete` e `Logout`" da
issue original significa aqui, e o que não significa:** os dois
`BlobStore` já são idempotentes por conta própria ("deleting a key that
is not there is not an error" — `fsBlobStore` e `s3BlobStore`), e isso é
aproveitado sem código extra: se o blob já tiver sido removido antes por
qualquer motivo, a chamada best-effort dentro de `Service.Delete` não
falha por isso. Isso **não** significa que o endpoint HTTP inteiro seja
idempotente no sentido de `user.Service.Logout` (sucesso silencioso
sempre, mesmo para um token que nunca existiu) — essa comparação não se
aplica aqui porque `Logout` não tem verificação de dono formal, enquanto
`attachment` tem uma invariante mais forte para preservar: **um anexo de
outro usuário é `ErrNotFound`, nunca sucesso** (a própria issue exige
isso). Deletar a mesma chave duas vezes segue o mesmo contrato que
`task.Service.DeleteTask` já estabelece neste projeto: primeira chamada
sucesso (`204`), segunda chamada `ErrNotFound` (`404`) — porque a linha já
não existe. Isso ainda é "idempotente" no sentido REST formal (o estado
final do servidor é o mesmo depois de qualquer número de chamadas), só
que o código HTTP muda entre a primeira e as seguintes — o mesmo padrão
já usado em toda parte deste projeto para "delete de um recurso
identificado por dono".

**Rota — `DELETE /v1/files/{key}`, não `/v1/tasks/{id}/attachments/{key}`
como a issue original propunha:** o comentário já existente em
`Handler.RegisterRoutes` explica por que `GET /files/{key}` não é
aninhado sob `/tasks/{id}` — evita o *confused-deputy shape* em que o
`{id}` do path e a task real por trás da `{key}` podem discordar, e um
dos dois é acreditado por engano. A mesma razão vale, sem alteração, para
o delete: a issue propôs o path aninhado sem levar essa decisão em conta,
e a decisão já registrada prevalece.

**Trade-off aceito:** a requisição `DELETE` fica um pouco mais lenta (um
segundo I/O, contra o `BlobStore`, além do `UPDATE`/`DELETE` no banco), e
ganha um segundo ponto de falha na latência — mas nunca na correção: uma
falha nesse segundo ponto nunca faz a requisição inteira falhar.

---

## `startupProbe` cobre a migration lenta; Job separado foi rejeitado

`k8s/40-api.yaml` ganhou um `startupProbe` em `/health`, à frente de
`readinessProbe`/`livenessProbe`. `cmd/api` aplica migrations pendentes
dentro de `openDatabase` — **antes** de `ListenAndServe` — então até isso
terminar o processo não escuta em porta nenhuma; sem esse probe, o
orçamento de liveness sozinho (`initialDelaySeconds: 5` + `periodSeconds:
10` × `failureThreshold: 3` ≈ 35s) era o teto real para qualquer migration,
e uma que passasse disso derrubava o pod no meio da migration — o
seguinte reiniciava no mesmo ponto, crash loop.

**Alternativa considerada:** `DB_AUTO_MIGRATE=false` no `Deployment`, com
um `Job` separado de `cmd/migrate` rodando antes dele.

**Por que `startupProbe` em vez do `Job`:** `k8s/` deste projeto é
deliberadamente `kubectl apply -f k8s/` puro — sem Helm, sem ArgoCD, sem
qualquer ferramenta que garanta ordem entre recursos. Sem um hook de
release, nada impede o novo `ReplicaSet` do `Deployment` de começar a
subir antes de o `Job` terminar; garantir a ordem exigiria um passo manual
de dois comandos (`kubectl apply job.yaml && kubectl wait ... && kubectl
apply deployment.yaml`) — risco real de rodar fora de ordem, para um
projeto que já se descreve como cluster de validação descartável, não
como alvo de produção. `startupProbe` é o mecanismo que o próprio
Kubernetes oferece exatamente para esse formato de problema (GA desde a
1.20): nenhum recurso novo, nenhuma orquestração de ordem, e a falha
continua correta — uma migration que realmente quebra ainda derruba o
processo (`os.Exit(1)` a partir de `run()`), então o `Job` não teria
comprado uma detecção de falha melhor, só uma complexidade a mais.

`150 * periodSeconds(2s) = 300s` (5 minutos) é deliberadamente generoso —
não medido contra nenhuma migration existente hoje (as sete atuais rodam
em frações de segundo), é um teto para uma migration que ainda não foi
escrita. Depois que o `startupProbe` sucede uma vez, ele para de rodar
para sempre, e é aí que `readinessProbe`/`livenessProbe` assumem — o
`initialDelaySeconds: 5` do liveness passa a contar a partir desse
momento, não do início do container.

**Trade-off aceito, e um limite real:** um `SIGTERM` recebido durante a
migration não a interrompe — `migrateCtx` em `cmd/api/main.go` é derivado
de `context.Background()`, não do `ctx` de sinal que `run()` recebe, então
o processo só reage ao sinal depois que a migration terminar (sucesso,
erro, ou seu próprio timeout de 30s). Isso já era assim antes desta
mudança; o `startupProbe` só dá ao operador mais folga antes de decidir
matar o pod, não muda o que acontece depois que decide. Não é escopo desta
decisão corrigir — fica registrado para quem for mexer em
`RunMigrations`/`openDatabase` depois.

**Validado com o `k8s/rollout-test.sh` existente**, não só lido no
manifest — ver o resultado real na issue/PR que introduziu esta decisão.

---

## Bundle de CA na imagem `scratch`: copiado do builder, não instalado

A imagem final (`FROM scratch`) passou a incluir
`/etc/ssl/certs/ca-certificates.crt`, copiado do estágio `builder`
(`golang:1.26.6-alpine`, que já traz `ca-certificates` instalado — é o
mesmo arquivo que o próprio `go mod download` usa para falar HTTPS com o
proxy de módulos).

**Por quê:** sem ele, toda verificação de certificado de saída falhava com
`x509: certificate signed by unknown authority` — confirmado rodando o
binário real dentro do container contra `s3.amazonaws.com` com
`ATTACHMENT_S3_USE_SSL=true`: sem o bundle, a falha é de certificado; com
o bundle, o handshake TLS completa e o erro que volta é da AWS
autenticando a credencial (a prova de que a rede/TLS funcionou). O mesmo
buraco existia para `DATABASE_URL` com `sslmode=verify-full`, documentado
como limitação conhecida desde a criação do Dockerfile — nunca corrigido
porque nada até agora exercitava esse caminho.

**Alternativa rejeitada:** não suportar TLS de saída e documentar a
limitação, como o comentário original fazia. Rejeitada porque a Fase 11
(integração do crier + SigNoz) depende de falar HTTPS com um endpoint
OTLP externo, e o próprio exportador do crier recusa enviar credencial
sobre `http://` sem `AllowInsecureCredential` — sem o bundle, a única
saída seria aceitar essa opção insegura por padrão, o que é pior.

**Trade-off aceito:** ~179 KB a mais na imagem, e um segundo lugar (o
`Dockerfile`) que precisa saber que o builder tem esse arquivo no caminho
esperado. Nenhum pacote novo, nenhuma dependência de rede em tempo de
build além da que `go mod download` já faz — o arquivo já estava lá, só
não estava sendo copiado.
