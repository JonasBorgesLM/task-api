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

**Esse frontend passou a existir (Fase 13) — ver a seção seguinte,
"Autenticação: modo duplo (cookie httpOnly + Bearer)", para a decisão
tomada. O raciocínio acima continua valendo integralmente para o caminho
Bearer; nada aqui foi revertido.**

---

## Autenticação: modo duplo (cookie httpOnly + Bearer), CSRF condicionado à origem da credencial

A API passa a aceitar **duas** formas de credencial na mesma sessão, em vez
de só `Authorization: Bearer`:

- **Cookie `httpOnly`** — caminho do navegador (Fase 13, frontend em
  `web/`). Imune a roubo por XSS, ao contrário do `localStorage` que a
  seção anterior já apontava como o risco real de um frontend futuro.
- **`Authorization: Bearer`** — caminho de script/serviço, inalterado. Todo
  exemplo de `curl` do README continua funcionando exatamente como hoje.

**A regra que faz isso ser seguro, e não reabrir o que a decisão anterior
fechou:** a verificação de CSRF (`moat/csrf`) se aplica exatamente quando
uma requisição mutadora **não** carrega `Authorization` — o que cobre tanto
sessão autenticada por cookie quanto `POST /auth/login`/`register` sem
sessão nenhuma ainda (ver abaixo). Uma requisição com `Authorization`
nunca passa por CSRF, sob nenhuma circunstância.

**Por que a checagem não depende de resolver a sessão primeiro.** Uma
primeira formulação cogitada aqui fazia o gate de CSRF depender de saber,
depois da autenticação, se a credencial resolvida veio do cookie —
mas isso amarraria um middleware global a rodar só depois de
`RequireAuth`, que é por rota, não global. A pergunta real é mais simples
e não precisa de sessão nenhuma resolvida: **`Authorization` está
presente ou não?** Presente → caminho Bearer, nunca CSRF. Ausente →
requisição de navegador, sempre CSRF em método mutador. É a mesma função
que decide qual credencial validar (`credentialSource`) que decide se o
CSRF se aplica — uma decisão, dois usos, sem depender de ordem entre
middlewares.

**Por que `login`/`register` também são protegidos, e não só rotas já
autenticadas.** A formulação original cobria só sessão já autenticada por
cookie — mas isso deixa aberto "login CSRF": forçar o navegador da vítima
a logar numa conta que o atacante controla, fazendo a vítima gravar dados
sem saber numa conta alheia. Diferente do CSRF clássico (não rouba a
sessão da vítima), mas é uma variante real, e não vale a mesma proteção
que o resto da API já tem só porque a rota é pública. `login`/`register`
passam a exigir o mesmo token CSRF que qualquer outra escrita de
navegador exige.

**Como o frontend obtém o token.** `GET /v1/auth/csrf-token`, endpoint
público (sem sessão), devolve `{"csrf_token": "..."}`. O frontend guarda
o valor em memória — nunca em `localStorage`/`sessionStorage`, pela mesma
razão que o token de sessão em si nunca vai lá. `Protector.Rotate` roda
dentro de `login`, logo após autenticar: o token emitido antes do login
deixa de valer depois dele, fechando a janela de fixação que a própria
biblioteca documenta.

**`Secure` do cookie de sessão em desenvolvimento.** `COOKIE_INSECURE`
(default `false`) relaxa `Secure` — e só `Secure` — para permitir
`http://localhost` em dev; nunca usar em produção. Aplica-se igualmente ao
cookie de sessão e ao cookie do `moat/csrf`, um único interruptor para o
mesmo problema, não dois.

**Por que não migrar tudo para cookie.** Seria quebra de contrato
(`v2.0.0`): invalidaria os walkthroughs de `curl` do README e todo uso
backend-to-backend documentado. O modo duplo é aditivo — `v1.2.0`.

**Nota histórica:** as issues de CSRF fechadas na Fase 4 foram encerradas
com "descontinuada — CSRF não se aplica a este modelo de auth". Aquilo
estava correto *dado* header-only, o único modo que existia então. Esta
seção não as reabre nem as contradiz — a premissa que as fechou (só
Bearer) deixou de ser a premissa inteira, e CSRF passa a se aplicar
exatamente à parte nova.

**Trade-off aceito:** uma segunda superfície de configuração sensível
(`CSRF_SECRET`, com o mesmo cuidado de nunca aparecer em log que
`AttachmentS3SecretKey` já tem) e uma segunda checagem em toda escrita de
navegador. Aceito porque a alternativa — cookie sem CSRF — é exatamente a
vulnerabilidade que a decisão original evitou desde o início.

Detalhes de implementação (nomes de variável, ordem de middleware,
esquema de resposta) em `docs/openapi.yaml` e nas issues #112–#118 e
#130.

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

## gosec no CI: G104 excluído por inteiro, sem `-tests`

O `gosec` roda no gate com `-exclude=G104` e sem `-tests`. Os achados
específicos que restam em código de produção são suprimidos um a um com
`#nosec` e o motivo ao lado — nunca em bloco.

**Por quê:** medido neste repositório antes de decidir, não presumido.
Rodando `-tests` sobre o código inteiro: 101 achados. 68 eram G104 (erro
não checado) — quase todos `.Close()` de melhor esforço, o idioma que
este código já usa de propósito para uma limpeza cuja falha não tem para
onde ir. Os ~12 que só apareciam com `-tests` estavam em fixtures de
teste (senha falsa numa string de conexão, um `http.Cookie`/`http.Server`
bare construído para testar uma coisa estreita) — nenhum era defeito
real. Os 7 que restaram em código de produção foram lidos um a um antes
de decidir: SQL parametrizada que o `gosec` lê como concatenação por
causa dos números de placeholder (`internal/task/postgres_repository.go`),
um path de `.env` que vem de configuração do operador e nunca de
requisição (`internal/config/dotenv.go`), um `os.Remove` que já passou
pelo `pathguard.Guard` (`internal/attachment/storage.go`), e os dois
`SetCookie` que já setam `HttpOnly`/`Secure`/`SameSite` corretamente
(`internal/user/handler.go`).

**Trade-off aceito:** excluir uma regra inteira é mais largo que suprimir
linha a linha, e um `.Close()` genuinamente perigoso — um cujo erro
devesse propagar — passa despercebido pelo `gosec` daqui em diante.
Aceito porque o padrão já é resultado de decisão deste projeto, não
descuido: tratar cleanup de melhor esforço como não-crítico já é como
este código é escrito em toda parte, e sinalizar 68 ocorrências do mesmo
padrão não muda esse fato — só produz ruído que treina quem revisa a
ignorar o achado seguinte.

**O que continua ativo:** G701/G202 (SQL injection, string concatenada),
G304/G703 (travessia de caminho), G401 (MD5/SHA1), G402 (TLS mal
configurado) e o resto do conjunto de regras do `gosec` — em código de
produção e de teste igualmente, já que só `-tests` foi omitido, não uma
categoria de arquivo.

---

## `GET /v1/tasks`'s `limit`: teto no valor explícito, não no ausente

Um `limit` acima de 100 é rejeitado com `400`. Um `limit` **ausente**
continua significando "sem limite", exatamente como
`docs/openapi.yaml` já documentava antes desta mudança.

**Por quê a assimetria.** `docs/DECISIONS.md` § "Versionamento" e
`.claude/rules/api-contract.md` já registram a regra: uma mudança que
quebra o contrato é um `/v2` novo, nunca uma edição do que `/v1` já
promete. `limit` ausente devolver tudo é uma promessa publicada
("omitting every one of them returns every task the caller owns");
mudar isso agora seria editar `/v1`, não corrigi-lo. Um valor
**explícito** acima do teto não tem essa mesma promessa — nada em
`docs/openapi.yaml` jamais disse que `?limit=999999` funcionaria — então
recusá-lo fecha a metade do problema que não contradiz nada já escrito.

**O que fica em aberto, deliberadamente.** Um cliente que nunca manda
`limit` continua podendo pedir a lista inteira de uma vez. Fechar isso
por completo exigiria mudar o que `/v1` significa, o que este projeto
reserva para um `/v2` — não para uma issue de endurecimento.

**Por que 400 e não grampear ao teto.** `parsePagination` já rejeita
`limit`/`offset` negativos com `400` em vez de arredondar para zero;
grampear um valor grande demais introduziria uma segunda forma de lidar
com "valor fora do aceitável" no mesmo par de parâmetros. `400` com uma
mensagem que nomeia o teto é descobrível — um cliente que lê a resposta
sabe exatamente o que pedir a seguir (paginar com `offset`, não repetir
a mesma requisição esperando resultado diferente).

---

## Cache-Control em `/v1`: `no-store` no auth, `no-cache` no resto

Toda resposta sob `/v1` carrega `Cache-Control`. `/v1/auth/*` recebe
`private, no-store`; o resto recebe `private, no-cache`. Nenhuma
resposta autenticada emitia esse header antes desta mudança.

**Por quê dois valores, não um só.** Um `Set-Cookie` de login e uma
resposta de logout carregam ou afetam diretamente a credencial de
sessão — nada sobre essas trocas deveria sobreviver em disco ou memória
além da própria resposta, o que é exatamente o que `no-store` promete.
O resto das rotas (`/v1/tasks`, `/v1/tasks/{id}`, `/v1/auth/me`) não tem
esse mesmo risco, e permitir revalidação condicional (`no-cache`, não
`no-store`) é o que deixa uma futura resposta `304` com `ETag` ser
possível sem reabrir esta decisão.

**Por quê `private` nos dois casos, sempre.** Sem `private`, um cache
compartilhado (CDN, proxy corporativo) na frente desta API poderia
servir a resposta de um usuário para outro que passe pelo mesmo
intermediário — a API não tem como saber se existe um na frente dela,
então a garantia tem que valer incondicionalmente.

**O que isto não cobre.** O middleware decide pelo prefixo do caminho
(`/auth/`), não por o que o handler realmente faz — uma rota futura sob
`/v1/auth/` que não carregue nada sensível ainda herda `no-store`, o
lado mais conservador de errar. `/health`, `/health/ready` e
`/debug/vars` ficam fora de `/v1` e fora desta regra, a mesma exceção
que já vale para o versionamento.

---

## ETag de `GET /v1/tasks`: hash de `(id, version)` da própria página, não do corpo inteiro

`GET /v1/tasks/{id}` e `GET /v1/tasks` respondem `ETag`, e aceitam
`If-None-Match` para devolver `304` sem corpo. No detalhe, o validador é
direto — `"<id>:<version>"`, reaproveitando o contador de concorrência
otimista que `Repository` já mantém. Na listagem não há uma única
`Version` para reaproveitar, então `pageETag` deriva uma a partir de
`(id, version)` de cada linha efetivamente devolvida, em ordem, e faz o
hash disso — nunca o corpo JSON inteiro.

**Por que não o corpo inteiro.** Geraria o mesmo resultado prático (um
hash que muda quando o conteúdo muda), mas obrigaria serializar a
resposta antes de decidir se ela precisa ser enviada — exatamente o
trabalho que o `304` existe para evitar. Hash de `(id, version)` é
suficiente porque `version` já muda exatamente quando a linha muda
(Update/status transition incrementam), e `id` é o que torna a
composição da janela — quem entrou, quem saiu, quem trocou de posição —
parte do hash sem precisar comparar ordem explicitamente.

**Por que isso não lê nada além do que a página já leu.** A issue
(15.D2) pedia um validador "sem ler tudo para calcular". `pageETag`
roda sobre as linhas que `Repository.FindAll` já trouxe para montar o
corpo da resposta — nenhuma consulta adicional, nenhum full-scan da
tabela do usuário.

**Comparação estrita, sem `W/`.** Este servidor nunca emite um
validador fraco, então `ifNoneMatchHits` compara por igualdade exata
(depois de aceitar múltiplos valores separados por vírgula e o
curinga `*`, como o cabeçalho HTTP permite) — não há necessidade de
implementar a semântica de comparação fraca que RFC 9110 §8.8.3.2
define para quando ela existe.

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
precisa cobrir este atraso **mais** o `HTTP_SHUTDOWN_TIMEOUT` **mais**,
desde a integração do crier (Fase 11), o orçamento de drain do crier
que roda depois (`crierShutdownTimeout`, `cmd/api/crier.go`) — `closeAll`
roda os dois em sequência, nunca em paralelo. Hoje 5 + 10 + 5 = 20 < 30.
Aumentar qualquer um dos três sem aumentar o grace period é como isso
volta a quebrar, em silêncio. Ver a seção "Fase 11.7" abaixo para a
medição real desse orçamento sob carga, não só a aritmética.

---

## crier + SigNoz: registro consolidado da Fase 11 (issue 11.9)

Registro único da Fase 11 (crier embutido + validação contra um SigNoz
real), reunindo aqui o que antes estava espalhado em quatro seções
separadas deste arquivo. Nenhum conteúdo técnico foi alterado nesta
consolidação — apenas a organização; ver a issue 11.9 e o PR que fechou
esta issue para a verificação de que o texto é o mesmo, só reagrupado.
As quatro subseções abaixo mantêm a ordem cronológica original em que as
decisões foram tomadas: a integração do crier em si, a validação de que
os logs chegam de fato ao SigNoz, o comportamento sob shutdown e carga
real, e por fim onde e como o SigNoz roda.

### Fase 11: crier embutido, stdout mantido em paralelo

`cmd/api/crier.go` integra a biblioteca `crier` (`core.New()` + o
exportador `exporters/otlp`) como um segundo destino de log, opt-in via
`CRIER_OTLP_ENDPOINT`. Sem essa variável, `buildCrier` retorna `nil` e
nada muda — mesmo padrão de "desligável por configuração" já usado para
`ATTACHMENT_S3_*`.

**stdout nunca é substituído, só espelhado.** `crierTeeHandler`
(`cmd/api/crier.go`) envolve o `slog.Handler` que já escreve em stdout;
`Handle()` sempre chama o handler original primeiro, e o resultado que
`Handle()` devolve é o dele — o espelhamento para o crier é best-effort e
nunca pode afetar o caminho principal.

**Por quê manter os dois:** o buffer do crier é em memória, de uma
instância só, reiniciada a cada rollout. stdout já é capturado pelo
runtime do container e sobrevive a qualquer coisa que aconteça com o
crier — inclusive ele nunca ter sido configurado. O README do crier é
explícito: entrega é *at-least-once*, e perda no shutdown é possível e
sempre contada (`DrainSummary`), nunca silenciosa.

**Trade-off aceito:** manter os dois sinks custa uma linha de log
duplicada por request quando o crier está ligado — armazenamento e
egress dobrados para o volume de acesso. Aceito porque a alternativa
(substituir stdout) reintroduz exatamente o ponto único de falha que o
parágrafo acima descreve.

#### Um "tee" de `slog.Handler`, não uma chamada por call site

A alternativa considerada foi adicionar `crier.Log(...)` em cada um dos
~20 call sites de log do projeto (cmd/api e os três Handlers de domínio).
Rejeitada: exigiria mudar toda linha de log existente e criaria dois
caminhos para divergir. Em vez disso, `crierTeeHandler` embrulha o
`slog.Handler` uma única vez, em `run()` — todo `logger.Error/Info/Warn`
já existente passa a alcançar o crier automaticamente, `request_id`
incluído (é só mais um atributo que a linha de log já carregava).

#### Um achado real, não só leitura de documentação: atributos precisam de conversão

Verificado por experimento, não por ler o código do crier: um atributo
`slog` de tipo `error` (o que `"error", err` produz — `error` não é um
`slog.Kind` nativo) chega ao `Limits` do crier como tipo não suportado, e
é **silenciosamente substituído** por um marcador `"…[unsupported value
type]"` antes da exportação — descartando exatamente o campo que uma
pessoa abre o log para ler. `crierAttrValue` (`cmd/api/crier.go`) resolve
isso: converte qualquer atributo fora da lista seguros do crier (string,
bool, os inteiros, float, `time.Duration`, `time.Time`) para sua
representação em texto (`slog.Value.String()`, que corretamente chama
`.Error()`/`.String()` do valor por baixo) antes de entregar ao crier.
`TestCrierTeeHandler_PreservesWrappedOutput_AndMirrorsToCrier` e
`TestCrierAttrValue` (`cmd/api/crier_test.go`) travam essa propriedade —
cada um foi verificado falhando de propósito (a conversão removida
temporariamente) antes de mergear.

#### Custo de dependência (issue 11.3)

`go get` real, grafo conferido (não suposto): `CRIER_OTLP_ENDPOINT`
configurado adiciona exatamente **4 módulos** —
`github.com/JonasBorgesLM/crier/core`,
`github.com/JonasBorgesLM/crier/exporters/otlp`,
`go.opentelemetry.io/proto/slim/otlp` (a variante *slim*, que existe
precisamente para não trazer a árvore de dependência do coletor/gRPC
completo) e `google.golang.org/protobuf`. `moat` já era dependência
direta, sem bump de versão. Todos Go puro — confirmado, não só assumido,
pelo próprio build estático (`FROM scratch`, `CGO_ENABLED=0`) continuar
funcionando com eles no grafo. `govulncheck` limpo com as quatro
dependências novas presentes.

**Trade-off aceito:** o exportador é OTLP/HTTP apenas (porta **4318**,
path `/v1/logs` — não há transporte gRPC nem porta 4317, ao contrário do
esboço original desta fase). Se um backend de observabilidade futuro só
falar gRPC, essa decisão precisa ser revisitada — não é o caso do SigNoz,
que traz coletor OTLP/HTTP embutido.

#### Nome de serviço e versão

`Options.ServiceName` é a constante `"task-api"` (`crierServiceName`).
`Options.ServiceVersion` fica vazio por ora — o mecanismo de
versão/commit do binário (`version`/`commit` em `cmd/api/main.go`) ainda
não existe nesta branch (issue #83/#84, PR separado). Preenchê-lo é
consequência de uma linha só assim que aquele PR mergear; não vale
duplicar aqui o mecanismo de detecção de versão só para adiantar este
campo opcional.

#### `crierShutdownTimeout`: provisório, não a aritmética final

`crier.Shutdown` roda dentro de `closeAll`, **antes** de `closeDB` e
**depois** de `closeBlobs` — na mesma ordem já estabelecida para os
outros recursos. O timeout usado (`crierShutdownTimeout`, 5s) é uma
constante própria, deliberadamente **não** reaproveitando
`cfg.ShutdownTimeout`: `closeAll` roda *depois* de `srv.Shutdown` já ter
gasto até `cfg.ShutdownTimeout` do seu próprio orçamento, e empilhar um
segundo orçamento completo em cima estufaria o tempo total de shutdown
silenciosamente para além do que `terminationGracePeriodSeconds` no
manifest do Kubernetes contabiliza.

**Isto é provisório.** 5s foi escolhido pelo mesmo raciocínio do timeout
de ping do banco (`openDatabase`), não por medição contra um SigNoz real
— não existe um ainda (issue 11.5, bloqueada em decisão de
infraestrutura). A issue 11.7 revisita essa aritmética depois de existir
um deploy real para rodar `k8s/rollout-test.sh` contra ele, igual ao que
já foi feito para `HTTP_PRE_SHUTDOWN_DELAY` — ver a seção "Drain antes do
shutdown" acima.

#### `/health/ready` não é acoplado ao crier (issue 11.8)

`core.Crier.Health()` existe e reporta liveness/readiness do próprio
pipeline do crier — e **deliberadamente não é consultado por**
`GET /health/ready`. Profundidade do buffer e contadores de perda por
motivo são publicados em `/debug/vars`
(`crier_buffer_depth`, `crier_records_dropped`) em vez disso.

**Por quê:** `/health/ready` existe para a dependência de que a API
*precisa* para servir — o banco (ver `k8s/40-api.yaml`). Um backend de
log inacessível não impede a API de atender ninguém; acoplar os dois
faria um SigNoz fora do ar tirar réplicas saudáveis de serviço, a mesma
inversão de prioridade já rejeitada para liveness não checar dependência.
`core.Health.Live()` do próprio crier documenta esse raciocínio para o
pipeline dele mesmo — este projeto aplica o mesmo princípio uma camada
acima, à sua própria composição do crier como dependência opcional.

**Como o expvar permanece correto entre testes.** `expvar.Publish` só
pode ser chamado uma vez por nome — mas `buildCrier` roda uma vez por
`*testing.T` na suíte deste pacote, não uma vez por processo como em
`main()`. `publishCrierExpvarOnce` (guardado por `sync.Once`) resolve
isso publicando os `expvar.Func` uma única vez, fechando sobre variáveis
de pacote (`currentCrierInstance`/`currentCrierMetrics`, ponteiros
atômicos) que cada chamada de `buildCrier` reaponta — o mesmo padrão já
estabelecido no projeto de "fechar sobre a variável de pacote, nunca
sobre um parâmetro capturado", aplicado aqui pela primeira vez a um
recurso construído mais de uma vez por processo de teste.

### Validação real da issue 11.6: dois registros confirmados no ClickHouse do SigNoz

`CRIER_OTLP_ENDPOINT` verificado entregando de verdade, não apenas "o
exportador não retornou erro" — cada prova consultou diretamente o
`signoz_logs.logs_v2` do ClickHouse por `request_id`, não a UI (que
exigiria resolver o fluxo de login da versão do SigNoz instalada, tempo
melhor gasto verificando o dado em si):

1. **`run()` local → SigNoz real**, sem Kubernetes envolvido: binário
   compilado desta branch, `CRIER_OTLP_ENDPOINT=http://localhost:4318`,
   uma requisição a `/health`, `SIGTERM` gracioso. Log
   `"crier drain completed", "summary":"drain complete in 1ms, no
   records lost"`. `SELECT ... FROM signoz_logs.logs_v2 WHERE
   attributes_string['request_id'] = '<o id do log>'` — **uma linha**,
   `method`/`path`/`duration`/`request_id` todos intactos.
2. **Deploy real via os manifests deste repositório**, dentro de um
   cluster kind: `kubectl apply -f k8s/` com o `CRIER_OTLP_ENDPOINT` já
   registrado no `ConfigMap` (`k8s/30-config.yaml`), `signoz-ingester-1`
   conectado à rede `kind`, requisição real contra o `Service`, e então
   `kubectl rollout restart deploy/task-api` — a mesma operação que
   `k8s/rollout-test.sh` (issue 11.7) vai medir. O `request_id` da
   requisição feita **antes** do restart apareceu no ClickHouse **depois**
   do pod antigo ter sido substituído — prova de que o drain do crier no
   `closeAll` (ver a seção "Fase 11" acima) de fato roda antes do processo
   sair, não só em teoria. `resources_string['service.name'] = 'task-api'`
   confirmado em 87 registros contados no total, batendo com
   `crierServiceName` (`cmd/api/crier.go`).

Recursos de teste (cluster(s) kind descartáveis, imagem
`task-api:crier-e2e-test`) removidos depois; o stack do SigNoz continua
rodando na máquina para a issue 11.7 reaproveitar.

### Issue 11.7: shutdown sob carga real, drain do crier reconciliado com o SigNoz

`k8s/rollout-test.sh` estendido para, quando encontra um SigNoz rodando
(`SIGNOZ_UP=true`, mesma detecção da seção acima), reconciliar o que o
crier reportou ter perdido no drain contra o que de fato chegou ao
ClickHouse — não só medir requisições HTTP perdidas, que já era o que o
script fazia.

**Dois pods, não um.** Uma primeira versão só capturava o drain do pod
que o rollout substitui, deixando os últimos segundos de carga contra o
pod *novo* — que segue rodando até o script terminar — de fora de
qualquer contabilidade. Corrigido fazendo o script também desligar esse
pod final graciosamente (`kubectl delete pod`, o mesmo caminho de
shutdown de um rollout ou de `kind delete cluster` com Kubernetes real,
diferente de um `kind delete cluster` puro — que mata os containers sem
dar chance de shutdown gracioso nenhum) antes de consultar o ClickHouse.

**A divergência apareceu, foi investigada, e a causa raiz não é bug do
task-api.** Com os dois pods desligados graciosamente e ambos reportando
`"crier drain completed"` (zero perda), o ClickHouse ainda assim mostrou
menos registros do que os enviados logo em seguida — 148 de 159 numa
medição. A causa: `Export()` do exportador OTLP retornar sucesso
significa apenas que o **receptor OTLP do SigNoz aceitou o lote** (ver o
doc comment de `exporters/otlp.Exporter.Export`), não que o registro já
está indexado no ClickHouse — o próprio SigNoz faz seu processamento e
inserção de forma assíncrona, num ciclo que o crier não enxerga nem
controla. Confirmado eliminando a divergência com uma espera adicional
*depois* dos dois drains — 15s, medido, não suposto — e reproduzindo o
resultado limpo (sent = received, 0 perdas) em duas execuções
consecutivas com durações de carga diferentes (166/166 com 20s de carga,
207/207 com 25s).

**O que isso não prova, e é importante dizer:** que o crier nunca perde
nada. Prova que, nas condições testadas (rede local, sem pressão de
buffer, sem circuito aberto), o caminho feliz é exato. `DrainSummary`
continua sendo o mecanismo que conta perda real quando ela acontece — a
reconciliação aqui é o que verifica, por execução repetida, que esse
mecanismo bate com a realidade quando diz que não perdeu nada.

**Aritmética do grace period revista** (issue explicitamente pede isto
junto): com o drain do crier agora parte do `closeAll`,
`terminationGracePeriodSeconds` precisa cobrir `HTTP_PRE_SHUTDOWN_DELAY`
(5s) + `HTTP_SHUTDOWN_TIMEOUT` (10s) + `crierShutdownTimeout` (5s) = 20s,
contra os 30s configurados — folga de 1.5x, contra 3x antes da Fase 11.
Comentários atualizados em `k8s/40-api.yaml` e `k8s/30-config.yaml` no
mesmo commit desta seção. Nenhuma das execuções acima chegou perto do
orçamento de 20s — o shutdown gracioso, com o SigNoz local e saudável,
levou uma fração de segundo, não o pior caso.

**Um achado à parte, investigado e não confirmado como bug — registrado
para não se perder.** Numa das primeiras tentativas de criar o cluster
do zero com o crier já configurado, o pod de `task-api` foi
`OOMKilled` (limite de 256Mi) nos primeiros segundos, antes de qualquer
carga real. Comparação direta de RSS local (com e sem
`CRIER_OTLP_ENDPOINT` configurado) mostrou diferença de menos de 1 MiB —
não sustenta um vazamento de memória do crier. Repetindo a mesma sequência
(cluster novo, manifests com o crier já configurado, aplicados a frio)
mais de uma vez depois, o mesmo pod subiu com o padrão de restart já
conhecido e documentado deste projeto (`Error`, exit 1 — Postgres ainda
não pronto no instante em que `task-api` tenta migrar, corrigido
sozinho pela política de restart do Kubernetes em segundos) — **sem**
`OOMKilled`, com ou sem o crier configurado. Não foi possível reproduzir
o OOM sob condições controladas; o mais provável é uma picada
transitória de memória no host durante criação/remoção rápida e repetida
de clusters kind nesta máquina de desenvolvimento, não algo que o código
do crier introduziu. Registrado em vez de descartado, para que uma
reaparição futura tenha este parágrafo como ponto de partida.

### SigNoz: Docker Compose oficial, mesma máquina do cluster, ligado à rede do `kind` (issue 11.5)

**Decisão revisada — substitui a versão anterior desta seção (VM
dedicada + VPN/peering), descartada antes de qualquer provisionamento.**
SigNoz roda via **Docker Compose oficial do projeto** (não build próprio,
não SigNoz Cloud, não VM separada), fora de qualquer cluster Kubernetes,
na **mesma máquina** que o cluster de validação do task-api.

**Por quê a mudança:** a versão anterior assumia uma segunda máquina
(VM) com túnel de rede próprio — infraestrutura real a provisionar e
manter. Rodar na mesma máquina do cluster elimina essa camada inteira:
não há VM para dimensionar, não há VPN/peering para configurar, o
Compose oficial do SigNoz já inclui ClickHouse dimensionado para uso de
desenvolvimento/validação. O dimensionamento de 2 vCPU/4 GB citado na
versão anterior era um piso pensado para uma VM isolada — deixa de fazer
sentido como número autônomo quando o SigNoz divide a máquina com o
Docker do cluster de validação; o que importa agora é a máquina como um
todo ter memória suficiente para os dois (o Compose oficial documenta os
próprios requisitos de recurso).

**Instalação reproduzível.** O `deploy/docker` legado do repositório do
SigNoz está deprecado desde a v0.130.0, substituído pela CLI **Foundry**:

```bash
curl -fsSL https://signoz.io/foundry.sh | bash   # instala foundryctl
cat > casting.yaml <<'YAML'
apiVersion: v1alpha1
kind: Installation
metadata:
  name: signoz
spec:
  deployment:
    flavor: compose
    mode: docker
YAML
foundryctl cast -f casting.yaml   # gera o compose e sobe os containers
```

Com o nome de projeto padrão ("signoz"), o container que recebe OTLP é
`signoz-ingester-1`, publicado em `0.0.0.0:4317-4318` no host e também
acessível por nome dentro da rede Docker `signoz-network` que o Compose
cria.

#### A pergunta que não podia ser assumida: como um pod alcança um serviço no host

A ferramenta que cria o cluster Kubernetes local neste repositório é
**kind** — confirmado lendo `k8s/rollout-test.sh` (`kind create cluster`,
`kind load docker-image`, contexto `kind-$CLUSTER`), não minikube, k3d
ou o Kubernetes embutido do Docker Desktop. Isso importa porque cada uma
resolve "pod alcança serviço no host" de um jeito diferente, e a resposta
certa depende de qual está em uso — presumir errado aqui teria produzido
um `CRIER_OTLP_ENDPOINT` que funciona na máquina de quem escreveu a
decisão e falha em qualquer outra.

**Verificado por execução real, três mecanismos, num cluster kind
descartável criado só para este teste** (não por leitura de
documentação — a documentação oficial do kind não cobre este cenário
diretamente, e os relatos da comunidade sobre `host.docker.internal` em
Linux são, na melhor das hipóteses, de segunda mão):

1. **`host.docker.internal`** — resolveu e respondeu `200` a partir de um
   pod, nesta máquina (macOS, Docker Desktop). `docker inspect` do nó do
   kind mostra `ExtraHosts: null`: a resolução não veio de configuração
   do container nem do kind — veio do DNS embutido do Docker Desktop, que
   resolve esse nome para **qualquer** container sob seu daemon. É
   comportamento do **Docker Desktop**, não do kind, e múltiplos relatos
   da comunidade (issues do próprio `kind`) convergem em: isso **não**
   funciona por padrão em Docker Engine puro no Linux. Não verificado
   aqui por falta de uma máquina Linux à mão — citado com essa ressalva
   explícita, não como fato confirmado.
2. **IP do gateway da rede Docker do `kind`** (`172.21.0.1` no teste) —
   **falhou** (`connection refused`) nesta máquina: no Docker Desktop
   esse gateway vive dentro da VM Linux interna, que não expõe as portas
   publicadas pelo próprio macOS. Em Docker Engine nativo (Linux, sem
   VM), esse mesmo mecanismo tende a funcionar, porque o gateway da
   bridge *é* a interface de rede real do host — mas isso não foi
   verificado aqui pela mesma razão do item anterior.
3. **Container do SigNoz anexado à mesma rede Docker que o kind usa**
   (rede `kind`, criada automaticamente pela própria CLI) — **funcionou,
   por nome DNS do container e por IP**, nesta máquina. Este é o único
   dos três que não depende de VM, de gateway, ou de qual sistema
   operacional roda o Docker: é só "dois containers na mesma rede
   Docker", o caso mais básico e portátil que existe.

**Decisão: usar o mecanismo 3.** O Compose oficial do SigNoz declara sua
própria rede por padrão; o `docker-compose.yml` (ou um override) precisa
declarar essa rede como `external`, apontando para a rede `kind` (nome
padrão que a CLI do kind cria; conferir com `docker network ls` se um
nome de cluster não-padrão estiver em uso). `CRIER_OTLP_ENDPOINT` passa
a apontar para o serviço do coletor OTLP do SigNoz pelo nome do serviço
Compose, não por IP nem por `host.docker.internal` — nome DNS de
container é estável entre restarts, IP não é.

**Sem credencial, `http://` — herdado da decisão anterior, ainda vale.**
SigNoz self-hosted não exige chave de ingestão por padrão, e "mesma
máquina, mesma rede Docker" é, se algo, um limite de confiança mais
apertado do que a VPN cogitada antes. `otlp.Config.Credential` continua
no valor zero. `https://` continua não fazendo sentido pelo mesmo motivo
de antes (a imagem `scratch` não tem bundle de CA nesta branch, e mesmo
com um, não há cadeia de confiança adicional a proteger aqui).

**Trade-off aceito:** esta decisão amarra a disponibilidade do SigNoz à
disponibilidade da própria máquina do cluster — não há isolamento de
falha entre os dois como uma VM separada daria. Aceito porque o cluster
de validação já é, pela própria natureza (`k8s/` é descartável, não
produção — ver `CLAUDE.md`), efêmero e local; um SigNoz que só precisa
sobreviver enquanto essa validação roda não precisa da resiliência de uma
segunda máquina. **Se este projeto ganhar um cluster de produção de
verdade** (fora do que `k8s/` representa hoje), esta decisão — mesma
máquina, mesma rede Docker — deve ser revisitada explicitamente para
esse ambiente, não herdada por inércia.

**Antes de instalar de verdade:** limpar os recursos usados nesta
investigação (cluster kind descartável, container de teste) — feito;
nada do experimento ficou para trás.

---

## Auditoria de log: nenhum valor sensível interpolado, e a garantia agora tem teste

Levantamento completo de todo call site de log do projeto (issue 11.1 da
Fase 11): nenhum deles interpola valor sensível — DSN do Postgres,
credencial de S3, token de sessão, título/descrição de task — na
*mensagem* do log. Todo `logger.Error` do projeto passa `err` como
atributo estruturado (`"error", err`), nunca via `fmt.Sprintf` na string.

**A pergunta que importava não era essa, e sim uma mais sutil:** mesmo um
atributo estruturado carrega o texto de `err.Error()` como valor — se
esse texto em si contiver a credencial, a estrutura do log call site não
salva nada. Verificado por experimento, não por leitura de código:

- **pgx** (erro de `sql.Open`/`Ping` com `DATABASE_URL` malformada ou
  inalcançável): a senha nunca aparece. Erro de conexão mostra só
  `user=... database=...`; erro de parse ecoa a DSN de volta, mas com a
  senha substituída por `xxxxx` — redação embutida no próprio driver.
- **minio-go** (`BucketExists` contra endpoint inalcançável): a
  `SecretKey` nunca aparece no erro — confirmado contra um host que
  recusa conexão.
- **moat/validate**: documentado e garantido pelo próprio pacote a nunca
  ecoar o valor validado (`validate.go`'s doc comment) — é por isso que
  `user.normalizeEmail`'s erro de validação é seguro para logar.
- Nenhum `panic()` explícito no código carrega dado de request — os três
  existentes são falhas de construção na inicialização (dependência
  ausente, hash dummy do bcrypt) ou o re-panic de
  `http.ErrAbortHandler`.
- `middleware.Logging` já excluía corpo e query string deliberadamente
  (só loga `r.URL.Path`, nunca `r.URL.RawQuery`) — confirmado, sem
  mudança necessária.

**Conclusão da 11.2:** não havia call site para *converter* — a auditoria
não encontrou nenhum. O trabalho real foi provar a propriedade com teste,
não com comentário, exatamente como a issue pedia para o caso do S3:
`TestRun_DatabaseURLPasswordNeverLeaks` (`cmd/api/main_test.go`) e
`TestNewS3BlobStore_SecretKeyNeverLeaksOnFailure`
(`internal/attachment/s3_credential_leak_test.go`). Cada um foi
verificado por regressão real antes de mergear: uma interpolação da
credencial injetada temporariamente no código de produção fez o teste
correspondente falhar, depois revertida.

**Trade-off aceito:** a garantia continua dependendo do comportamento de
redação do pgx e do minio-go, bibliotecas de terceiro que o projeto não
controla — não de nada que o task-api implemente. Esses dois testes são o
que detecta uma regressão *deles*, não uma correção definitiva contra
ela: se uma versão futura de qualquer um dos dois parar de redigir a
credencial, o teste começa a falhar em vez de a vulnerabilidade passar
despercebida — a mesma filosofia do `govulncheck` acima, aplicada a uma
garantia que não vem de código próprio.

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

## Teto de anexos por task: 50, fixo — não substitui a quota por usuário acima

`Service.Upload` também recusa a 51ª tentativa de anexo numa mesma task
(`maxAttachmentsPerTask = 50`), independente de quantos bytes ela usa.

**Isto não contradiz a seção acima**, que rejeitou contagem como métrica
de *abuso de storage* — "poucos arquivos grandes" continua sem solução
por contagem, e continua sendo `ATTACHMENT_MAX_BYTES_PER_USER`'s
trabalho. O teto por task resolve um problema diferente: uma task com
centenas de anexos é impraticável de listar e navegar, mesmo que cada um
seja pequeno o bastante para nunca acionar a quota de bytes. Uma é
segurança/custo operacional; a outra é usabilidade da própria lista.

**Por que é uma constante fixa, não uma variável de ambiente como
`ATTACHMENT_MAX_BYTES_PER_USER`:** não há decisão de operador aqui — o
número não muda por ambiente, por cliente, ou por tamanho de deploy.
Virar configurável adicionaria uma variável nova a manter em quatro
lugares (`.claude/rules/config-env.md` § "Keep four places in sync")
para um valor que ninguém tem razão para escolher diferente.

**Checado antes de `Create`, não depois:** `Repository.CountByTask` roda
na mesma posição que `TotalBytesForUser` já ocupa — antes do corpo ser
lido, e antes da checagem de posse que `Create` faz por conta própria.
Um `taskID` que não pertence ao chamador conta 0 aqui (ver o próprio
doc comment de `CountByTask`), não porque a posse não importa, mas
porque `Create` já é quem reporta isso — checar duas vezes seria
duplicar uma regra, não reforçá-la.

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

## Frontend: Vite (SPA), monorepo em `web/`, mesma linha de versão do repo

Quatro decisões de arquitetura para o frontend (Fase 13, `docs/changes/web-frontend/plan.md` `CI-1`), registradas antes de qualquer código para que a implementação não fique escolhendo essas coisas ad hoc conforme avança.

### Vite, não Next.js

O frontend é uma SPA pura (Vite + React + TypeScript), sem SSR e sem rotas de servidor. Isso não é uma omissão — é a confirmação de uma decisão já registrada em `docs/ARCHITECTURE.md` § Future Improvements, "BFF (Backend-for-Frontend) layer": esse item já dizia que um BFF só se justifica quando existir mais de um serviço downstream para agregar, ou quando um cliente web e um cliente mobile precisarem de formatos de payload genuinamente diferentes. Nenhuma das duas condições existe hoje — há um único recurso central (`task-api`) e um único cliente (este frontend). Next.js (ou qualquer framework com SSR/rotas de API embutidas) resolveria um problema que este projeto não tem, ao custo de um servidor Node adicional para operar, testar e fazer deploy. Se um BFF real se justificar no futuro, é uma camada nova e explícita — não uma razão para escolher um framework diferente agora.

### Monorepo com CI filtrado por caminho

`web/` vive dentro deste mesmo repositório, não em um repositório separado. A alternativa (multi-repo) exigiria coordenar duas releases, dois `CHANGELOG.md`, e — o problema real — versionar a compatibilidade entre o contrato do backend e o que o frontend espera dele através de dois históricos de commit diferentes, quando `docs/openapi.yaml` já é a fonte única da verdade para os dois lados dentro de um único commit.

O custo do monorepo é acoplar os dois gates de CI se não houver cuidado: um PR que só toca `web/` não deveria disparar o gate Go (`gofmt`, `staticcheck`, `govulncheck`, testes com PostgreSQL), e vice-versa. `CI-2` do plano resolve isso com `paths-ignore`/`paths` nos dois workflows (`.github/workflows/ci.yml` ganha `paths-ignore: ['web/**']`; `.github/workflows/web-ci.yml`, novo, roda só em `web/**`) — e exige verificação por **execução real** nos dois sentidos antes de considerar o item pronto, não leitura do YAML, porque a Fase 7 já teve um caso real de filtro de workflow que parecia certo lido e não funcionava rodando (ver § "Princípio geral de validação" acima).

### Versionamento compartilhado com o repo

`web/` não tem sua própria tag/release — ele segue a mesma linha `vX.Y.Z` do repositório como um todo (a mesma que `CHANGELOG.md` já versiona). Um release do projeto que inclui mudança de frontend ganha uma entrada no mesmo `CHANGELOG.md`, não um arquivo separado. Isso é consistente com o monorepo: não faz sentido dizer que "o backend está na v1.2.0 mas o frontend está na v1.0.3" quando os dois são publicados, testados e versionados juntos a partir do mesmo commit.

### Cookie httpOnly, nunca `localStorage`

O frontend nunca guarda a credencial de sessão em `localStorage`/`sessionStorage`. A sessão vive exclusivamente no cookie `HttpOnly` que o backend já emite em `POST /auth/login` (ver § "Autenticação: modo duplo (cookie httpOnly + Bearer)" acima) — o próprio motivo de o backend ter adotado esse modo de autenticação já era fechar a superfície de roubo de token via XSS que `localStorage` deixa aberta; seria autocontraditório o frontend reabrir essa mesma superfície guardando o token CSRF, ou qualquer outra coisa sensível, num storage que qualquer script no mesmo documento consegue ler. O token CSRF (obtido de `GET /v1/auth/csrf-token`) vive só em memória — uma variável JS que desaparece a cada reload, exigindo uma nova busca — nunca persistido.

### Navegação: `react-router-dom`, URLs reais desde a Fase 13.6

Nenhum item de `docs/changes/web-frontend/plan.md` (`CI-1`–`CI-11`) nem as issues `#119`–`#129` decidiam isso explicitamente antes de `CI-6` — só nomeavam "Page"s (`RegisterPage`, `LoginPage`, e mais tarde a lista de tasks), sem dizer se cada uma teria sua própria URL ou seria só uma troca de view por estado local. Como a decisão molda a estrutura de todo o resto da Fase 13 (CRUD de task, anexos), foi levada ao usuário em vez de escolhida em silêncio — decisão: `react-router-dom`, com URLs reais desde já (`/login`, `/register`, `/`), não uma alternativa sem dependência nova baseada em estado local.

Consequência direta, não antecipação: `RequireAuth` (`web/src/features/auth/RequireAuth.tsx`) — um guard de rota que redireciona para `/login` quando `useAuth()` não está autenticado — não está na lista de arquivos de `CI-6` em `plan.md`, mas é o que torna a rota `/` protegida possível; sem ele, "URLs reais" e "sessão só sabida via `GET /auth/me`" não se sustentam juntas.

### SPA deployment: um servidor Go próprio, não nginx/Caddy

Issue #229: até esta mudança, `web/`'s build de produção (`npm run build`,
`dist/`) não tinha nenhum caminho de deploy — só `npm run dev` existia. A
consequência não era só operacional: a CSP estrita do backend
(`default-src 'none'`) protege a origem da API, que serve só JSON e nunca
carrega script — ela nunca poderia ser a origem de onde um XSS rodaria. A
origem que serve o documento HTML da SPA é onde isso importa, e nada
emitia CSP, `nosniff`, `Referrer-Policy` ou HSTS ali, porque não existia
processo nenhum responsável por essa origem.

**A escolha: `cmd/web`, um binário Go próprio, no mesmo módulo de
`cmd/api`** — não nginx, não Caddy. Três alternativas reais foram
pesadas:

- **nginx/Caddy** é o padrão de mercado para servir uma SPA, mas
  introduziria uma tecnologia nova neste repositório — um `nginx.conf`
  ou `Caddyfile` sem cobertura de teste em Go possível, só smoke test via
  `curl`. Todo o resto deste projeto é Go, incluindo cada decisão de
  header de segurança já tomada (`internal/middleware`, `moat/
  secureheaders`) — escrita, testada e explicada em código, nunca
  configuração declarativa de terceiros.
- **Servir a SPA a partir do próprio `cmd/api`** foi rejeitado sem
  chegar a ser escrito: misturaria uma origem que serve HTML/JS/CSS com
  uma que existe precisamente para nunca servir nada disso (a CSP
  `default-src 'none'` do backend é essa promessa), e um processo servindo
  duas coisas com modelos de ameaça opostos é dois processos escondidos
  atrás de um.
- **`cmd/web`, reaproveitando `moat/secureheaders` e
  `internal/middleware`** — a escolha feita. Mesmo módulo Go de `cmd/api`
  (não um segundo `go.mod`), mesma imagem `scratch`/`USER 65532:65532`,
  mesmo padrão de graceful shutdown, e os mesmos pacotes de middleware
  genéricos (`RequestID`, `Logging`, `Recovery`) — sem duplicar nada que
  já existe, sem importar conhecimento de domínio (`internal/task`,
  `internal/user`) que este servidor não precisa.

**Custo aceito:** escrever e testar ~300 linhas de Go (`cmd/web/main.go`
+ testes) em vez de um arquivo de configuração pronto. Aceito porque o
resultado é testável com a mesma ferramenta (`go test`, `gosec`,
`govulncheck`) que já cobre o resto do projeto, e porque a CSP que ele
gera não é uma string fixa — ver o próximo ponto.

**A CSP é computada no startup, não escrita como constante.** `index.html`
carrega um script inline (a detecção de tema antes do primeiro paint —
ver o próprio comentário dele em `web/index.html`), e uma CSP restrita
precisa admiti-lo explicitamente via hash (`script-src 'self'
'sha256-...'`) — a alternativa, `'unsafe-inline'`, equivaleria a não ter
política de script nenhuma. `cmd/web` lê o `index.html` que ele
realmente está servindo e calcula o hash a partir dos bytes reais, uma
vez, no startup (`buildCSP`/`cspScriptHashes`), em vez de um hash
hardcoded como constante Go. Um hash fixo ficaria obsoleto em silêncio no
instante em que o conteúdo do script mudasse — bloqueando a detecção de
tema em produção sem nada no build ou no deploy para pegar isso. Calculado
a partir do arquivo real, a política está sempre correta para o que este
processo de fato está servindo.

**Nonce (`moat/secureheaders.WithNonce`) foi considerado e rejeitado.** A
biblioteca suporta isso nativamente — nonce por requisição, injetado nos
`<script>` e lido via `secureheaders.Nonce(r)` — mas exigiria (a)
re-renderizar `index.html` a cada requisição em vez de servi-lo como
arquivo estático, injetando o nonce em **todo** `<script>`, inclusive o
`<script type="module" src="...">` do bundle, e (b) nunca cachear a
resposta (a própria doc do `WithNonce` avisa: "do not put a shared cache
in front of nonced HTML"). Nonce existe para conteúdo inline cujo valor
muda por requisição — não é este caso: `index.html` é um artefato de
build inteiramente estático, sem nada influenciado por dados da
requisição, então um hash fixo por conteúdo é estritamente mais simples
e não abre mão de nenhuma garantia de segurança real.

**`WEB_API_ORIGIN` (runtime) e `VITE_API_BASE_URL` (build-time) são duas
variáveis, não uma, e têm que concordar.** A primeira é o que este
servidor permite em `connect-src`; a segunda é o que o bundle já
carrega embutido para suas próprias chamadas `fetch`. Uma divergência
entre as duas não falha nem o build nem o startup — falha em silêncio no
navegador, como violação de CSP no console. Documentado nos dois lugares
(`web/README.md`, `cmd/web/main.go`'s `loadConfig`) exatamente porque não
há uma checagem mecânica possível entre um valor embutido em JavaScript
já compilado e a configuração de um processo Go separado.

### Cache de páginas: revalidação em segundo plano, e o que fazer quando ela falha

`useTasks` (15.D3) guarda em memória cada página já buscada — chave
`(statusFilter, priorityFilter, pageIndex)` — e a mostra de imediato ao
revisitar, antes mesmo da requisição de revalidação começar
(stale-while-revalidate). A revalidação usa `If-None-Match` com o
`ETag` guardado (15.D2): sem mudança, a resposta é um `304` e nada
precisa ser atualizado.

**O que acontece quando a revalidação falha — decisão, não obviedade.**
Se a página já veio do cache e a requisição de revalidação falha (rede
fora, `503`, o que for), o hook **mantém mostrando os dados do cache**
em vez de substituí-los por uma tela de erro. Só uma busca sem nada em
cache ainda transiciona para o estado de erro, exatamente como
funcionava antes deste cache existir. O raciocínio: os dados que já
estão na tela continuavam corretos um instante atrás, e a revalidação é
uma verificação em segundo plano que o usuário nunca pediu
explicitamente — substituir uma lista boa por uma tela de erro por
causa dela seria pior do que simplesmente tentar de novo na próxima
navegação.

**O que isto não cobre:** não há indicação visual de "isto pode estar
desatualizado" quando uma revalidação falha silenciosamente — o usuário
não tem como saber que uma tentativa de atualização não funcionou. Aceito
por ora porque a única forma de disparar isso é já estar navegando entre
páginas já vistas com a rede instável nesse exato momento; um indicador
dedicado é trabalho futuro, não algo esta issue pedia.

**Invalidação é por evento, não por tempo — sem TTL.** Criar ou excluir
uma task limpa o cache inteiro (todas as páginas, todos os filtros),
não só a página atual: uma linha nova ou removida desloca a composição
de toda página seguinte à sua, e raciocinar sobre *quais* páginas
especificamente foram afetadas custaria mais do que só invalidar tudo e
pagar o preço de algumas buscas refeitas. Uma edição que continua
batendo com o filtro ativo, ao contrário, só atualiza a própria entrada
da página atual — inclusive derrubando o `ETag` guardado para `null`,
porque o hook não tem como calcular qual seria o novo (`Version` nunca
chega no corpo JSON — ver `internal/task/task.go`), e um `ETag` errado
que por acaso ainda bate seria pior que nenhum.

**Nunca `localStorage`.** O cache vive só em memória, dentro da mesma
instância do hook — fecha a aba, perde o cache. A mesma razão da seção
"Cookie httpOnly, nunca localStorage" acima, estendida por instinto e
não por o dado em si ser sensível: uma lista de tasks de um usuário não
tem por que sobreviver ao fechamento da aba só porque é conveniente.

---

## crier: ruído de health-check filtrado por severidade, não por amostragem

`cmd/api/crier.go`'s `buildCrier` passa um `core.Filter` com um `AttributeRule`
(`Key: "path", ValuePrefix: "/health"`) que eleva `MinSeverity` para
`core.SeverityError` só para essa regra — recurso do crier `core` v0.3.0
(ADR-0022 do próprio crier, "attribute-matched sampling"). Isso não muda o
log em stdout (`internal/middleware/logging.go` continua igual); afeta só a
cópia espelhada que sai para o `CRIER_OTLP_ENDPOINT`.

**Por quê:** `/health` e `/health/ready` são batidos por probes de
liveness/readiness em intervalo curto e quase sempre devolvem `200` — volume
que domina o espelho sem carregar sinal, exatamente o caso que o próprio
dashboard do crier documenta ("Health checks: o ruído que a ADR-0022 existe
para conter"). `middleware.Logging` só sobe o nível para `Error` numa
resposta `5xx` (ver seu próprio doc comment) — essas duas rotas não aceitam
parâmetro nenhum, então não têm caminho de `4xx` hoje — o que faz
`MinSeverity: SeverityError` bloquear exatamente o `200` rotineiro e deixar
passar uma falha real (`/health/ready` respondendo `503`) sem exceção.

**Alternativa rejeitada:** `SampleRate` em vez de `MinSeverity` na mesma
regra. Rejeitada porque `SampleRate` é probabilístico — usado aqui,
descartaria uma fração aleatória de qualquer severidade que bater na regra,
inclusive uma falha intermitente de `503`. `MinSeverity` só filtra o que já
sabemos ser ruído puro (o `200` de rotina), nunca por sorte.

**Trade-off aceito:** se `/health`/`/health/ready` um dia passarem a devolver
`4xx` (não acontece hoje — nenhuma delas lê corpo, query string ou parâmetro
de rota), essas linhas também seriam engolidas pelo espelho, silenciosamente,
até alguém notar e revisitar esta regra. Coberto por
`TestBuildCrier_HealthCheckNoise_FilteredUnlessError`
(`cmd/api/crier_test.go`), que prova as duas metades — o `200` rotineiro
some, o `503` continua chegando ao coletor.

---

## crier: dashboard SigNoz provisionado a partir do template oficial, fixado em um commit

`make signoz-dashboard` busca
`docs/observability/signoz/dashboard.json` do próprio repositório do crier
via `raw.githubusercontent.com`, substitui `{{.ServiceName}}` por
`task-api` e faz o `POST /api/v2/dashboards` documentado no README daquele
arquivo. A referência é um SHA de commit fixo
(`CRIER_DASHBOARD_REF` no `Makefile`, hoje `87048ec4…`), não `main`.

**Verificado rodando, não só lendo o `Makefile` (ver § "Princípio geral de
validação" acima):** `make signoz-dashboard` executado de ponta a ponta
contra um SigNoz real (Foundry Compose local, workspace novo) em
2026-09-05 — `POST` retornou `201`, e o dashboard aberto na UI mostrou os
10 de 10 painéis com dado real, não série vazia, contra tráfego real
gerado pela stack local do task-api (registro, login, tasks, respostas
`200`/`401`/`404`). O painel "Health checks" especificamente mostrou `0`
apesar de 12 requisições reais a `/health`/`/health/ready` no mesmo
intervalo — confirmação, contra uma instância real, de que a regra da
seção anterior filtra o que diz filtrar.

**Por quê:** o crier decidiu (ADR-0023 de lá) que esse arquivo é
"documentação, não contrato testado" — nada em CI do crier o exercita, e o
próprio README dele já registrou uma mudança de forma incompatível entre
versões do SigNoz (`tags` deixou de aceitar array de strings). Um `main`
sem pin mudaria o que este `make` alvo provisiona aqui sem nenhum changelog
deste lado. O SHA fixado é o commit que o crier verificou de ponta a ponta
contra um SigNoz real (`v0.139.0`, 10 de 10 painéis retornando dado, não
série vazia) — ver `docs/observability/signoz/README.md` no repositório do
crier.

**Alternativa rejeitada:** vendorizar uma cópia do `dashboard.json` dentro
deste repositório. Rejeitada porque o crier já é a fonte única desse
template (ADR-0023 de lá) — uma segunda cópia aqui divergiria da de lá sem
nada para avisar quando isso acontecesse.

**Trade-off aceito:** melhorias futuras no template do crier não chegam
aqui sozinhas — alguém precisa notar, revisar, e subir
`CRIER_DASHBOARD_REF` de propósito, o mesmo custo que este `Makefile` já
aceita para `STATICCHECK_VERSION`/`GOVULNCHECK_VERSION`.

---

## Exclusão de conta (`DELETE /v1/auth/me`, issue #197): imediata, não soft-delete

`DELETE /v1/auth/me` apaga a conta **na hora** da chamada — sessões,
tasks, anexos (linhas e bytes) e o próprio usuário. Não existe estado
"pendente de exclusão", carência, nem forma de desfazer depois que a
resposta volta `204`.

**Por quê:** a alternativa considerada — soft-delete com carência (ex.:
7–30 dias antes da exclusão de verdade) — exigiria um job de limpeza
recorrente, um estado que bloqueia login sem ser um dos existentes, e um
jeito de cancelar o pedido; nada disso existe hoje neste projeto, e nada
aqui pediu essa complexidade antes desta issue. Escolha do usuário
(`JonasBorgesLM`), levada explicitamente porque o próprio texto da issue
marcava isso como decisão de produto, não técnica.

**Trade-off aceito:** nenhuma proteção contra arrependimento (a exclusão
não tem "desfazer") nem contra uma sessão sequestrada destruindo a conta
— mitigado apenas por exigir a senha atual no corpo da requisição (a
mesma exigência de `POST /v1/auth/password`), nunca a sessão sozinha.

### Ordem de exclusão dos anexos: linha antes do blob, não o inverso

O texto original da issue propunha apagar os **bytes antes das linhas**
dos anexos durante essa cascata, citando o mesmo raciocínio já registrado
em `CLAUDE.md`/`docs/DECISIONS.md` § "Delete de anexo: síncrono, não só o
coletor" — mas invertido: aquela seção decidiu **linha primeiro, blob
depois** (best-effort), exatamente pelo espelho do `Upload` (bytes antes
da linha na escrita, porque a ordem inversa deixaria uma linha apontando
para um arquivo nunca escrito). Bytes-antes-da-linha no delete produz o
mesmo tipo de referência quebrada que essa regra já existe para evitar:
se o passo de apagar a linha falhar depois do blob já ter sumido, sobra
uma linha apontando para um arquivo inexistente — pior, e mais permanente
neste caminho de cascata, que ninguém revisita depois, do que o blob
órfão (que o coletor já recolhe) que a ordem linha-primeiro deixaria no
mesmo cenário de falha.

**Decisão:** a cascata de exclusão de conta reaproveita
`attachment.Service.Delete` — o mesmo caminho, já testado, que
`DELETE /v1/files/{key}` usa — em vez de inventar uma segunda ordem só
para este caso. Levado ao usuário antes de implementar, por ser
exatamente o caso que `CLAUDE.md` descreve: "se uma issue parecer
contradizer uma decisão registrada, pare e pergunte antes de prosseguir."

---

## Cache de ValidateToken (issue #232, 15.D1): TTL curto e fixo, invalidação imediata no mesmo processo

`user.Service.ValidateToken` é a chamada mais quente do código: `RequireAuth`
a executa em toda rota autenticada, e até aqui isso significava uma leitura ao
banco (`FindSessionByTokenHash`) por requisição, mesmo sabendo que a mesma
sessão é validada repetidamente em rajadas curtas. `internal/user/token_cache.go`
guarda o resultado de uma validação bem-sucedida por `tokenCacheTTL = 2 *
time.Second`, em memória, por processo.

**Por que 2 segundos, e não "sem cache" ou um TTL generoso — as duas
alternativas rejeitadas:**
- **Não implementar** eliminaria qualquer risco, mas deixaria a leitura ao
  banco em praticamente toda requisição autenticada, sem necessidade — a
  sessão não muda entre uma requisição e a seguinte na esmagadora maioria dos
  casos.
- **Um TTL generoso** (dezenas de segundos a minutos) maximizaria o ganho de
  performance, mas alargaria proporcionalmente a janela em que uma revogação
  – logout, logout de todas as sessões, troca de senha — pode continuar
  validando em outro processo. Isso ameaça diretamente a garantia que
  `POST /v1/auth/password` foi construído para dar (ver issue #196 e a seção
  "Limite de sessões" acima): trocar a senha porque um token vazou só resolve
  o problema se sessões antigas pararem de funcionar *logo*.
- **TTL curto (1–5s)** foi a faixa escolhida pelo usuário (`JonasBorgesLM`)
  como o ponto de equilíbrio, levada explicitamente porque a issue marcava o
  trade-off como decisão de produto, não técnica. `2s`, o valor concreto
  escolhido dentro dessa faixa, elimina a leitura ao banco em qualquer rajada
  de requisições mais frequente que isso — o caso comum — mantendo o pior
  cenário de staleness na casa de segundos, não minutos.

**Por que o TTL é fixo (não-deslizante), e não uma janela renovada a cada
leitura:** uma janela deslizante deixaria uma sessão revogada em outro
processo continuar validando indefinidamente, desde que as requisições
chegassem mais rápido que o próprio TTL — exatamente o cenário que este cache
existe para limitar. Cada entrada expira `tokenCacheTTL` após a última
confirmação real no `Repository`, ponto final; ler a entrada nunca empurra
esse prazo pra frente (ver `tokenCache`'s doc comment e
`TestTokenCache_FixedWindow_NotSlidingOnRead`).

**Por que o risco residual é limitado a um rolling update, não a réplicas em
regime permanente:** o deploy documentado em "Topologia de deploy" acima roda
uma única réplica em estado estável — não há dois processos concorrentes
servindo tráfego ao mesmo tempo fora de uma transição. A única janela real em
que dois processos existem simultaneamente é a sobreposição breve que o
próprio Kubernetes cria durante um rolling update (pod novo sobe antes do
antigo cair, mesmo com `replicas: 1` — já documentado naquela mesma seção). É
exatamente esse cenário, e não um regime de múltiplas réplicas hipotético,
que o TTL de 2s foi dimensionado para tolerar.

**Invalidação é imediata no mesmo processo — o TTL nunca é o único
mecanismo.** `Logout`, `LogoutAll`, `ChangePassword` e `DeleteAccount` cada um
chama o método correspondente do cache (`delete`/`deleteAllForUser`/
`deleteAllForUserExcept`) logo após a chamada ao `Repository` ter sucesso, e
antes de retornar. Isso significa que a única forma de uma sessão revogada
continuar validando por até `tokenCacheTTL` é ela ter sido cacheada por um
*processo diferente* daquele que processou a revogação — nunca o mesmo
processo aceitando de volta algo que ele mesmo acabou de invalidar. Os quatro
pontos de invalidação têm controle negativo cobrindo justamente essa fiação
(`TestLogout_InvalidatesCache_*`, `TestLogoutAll_InvalidatesCacheForUser`,
`TestChangePassword_InvalidatesCacheForOtherSessions_ButKeepsCallers`,
`TestDeleteAccount_InvalidatesCacheForUser`), e não só a lógica pura de
`tokenCache` isoladamente.

**Por que só uma validação bem-sucedida é cacheada.** Um token desconhecido ou
expirado nunca é memoizado como válido — `ValidateToken` só chama
`tokenCache.set` depois de `Repository` confirmar a sessão e checar a
expiração. Isso significa que este cache não pode transformar um token
momentaneamente inválido em validado; o único viés possível é na direção
oposta (aceitar por mais `tokenCacheTTL` algo que já foi válido e acabou de
ser revogado em outro processo), que é exatamente o trade-off descrito acima.

**Por que `tokenCacheTTL` é uma constante, não uma variável de ambiente.**
Expor isso como configuração deixaria um operador alargar silenciosamente a
janela de atraso de revogação sem que o raciocínio acima fosse revisitado — o
número embute uma decisão de segurança, não um parâmetro de tuning
operacional.

---

## Índices para `status`/`priority` em `tasks` (issue #236, 15.D5): medido, não criado

`idx_tasks_user_id_created_at_id (user_id, created_at, id)` é o único índice
sobre `tasks` além da chave primária. Nenhum cobre `status` ou `priority`
isoladamente — a issue pedia explicitamente para **medir antes de criar**
("índice só onde o plano mostrar problema"), não para adicionar um por
precaução.

**Medição feita:** volume sintético gerado direto via SQL (não por
`cmd/seed`, cujo único contexto de 30s cobre seu uso normal de demonstração,
não uma carga de centenas de milhares de linhas) e descartado depois —
nenhuma linha desta medição ficou no banco. Dois cenários:

1. **50 usuários × 5.000 tasks cada** (~250k linhas) — escala já pesada para
   um gerenciador de tasks pessoal. `EXPLAIN ANALYZE` em toda combinação
   relevante de filtro (sem filtro, `status` único, `status`+`priority`
   combinados) e posição de página (primeira, e uma bem funda —
   `OFFSET 2000` sobre ~1.280 linhas que casam o filtro) ficou entre
   **0,15ms e 2,7ms** de tempo de execução real. Nos casos de offset raso o
   planner caminha direto por `idx_tasks_user_id_created_at_id` (Index Scan);
   no de offset fundo com filtro seletivo, ele troca para Bitmap Heap Scan +
   Sort — mais caro que um Index Scan puro, mas ainda irrelevante em termos
   absolutos.
2. **Um único usuário com 100.000 tasks** — cenário deliberadamente extremo,
   bem além do que este produto tem qualquer indício de precisar hoje. O pior
   caso testado (`status`+`priority` combinados, raros, `OFFSET 5000`) caiu
   para Parallel Seq Scan + Sort, **~26ms** de execução. Criar um índice
   candidato `(user_id, status, priority, created_at, id)` e repetir a mesma
   consulta reduziu para **~11ms** (Bitmap Heap Scan pelo índice novo + Sort
   — mesmo com o índice, o `OFFSET` ainda força materializar e ordenar as
   linhas que casam antes de descartar as primeiras `5000`, então não vira um
   Index Scan puro). Índice e linhas sintéticas foram removidos depois da
   medição — nada disso ficou no schema ou nos dados.

**Decisão: não criar o índice agora.** Mesmo no cenário 1 (que já representa
um uso pesado real) o índice existente resolve tudo em menos de 3ms. O
cenário 2 só aparece com um único usuário acumulando cem vezes mais tasks do
que o cenário pesado — nada neste produto sugere que isso é uma forma de uso
esperada — e mesmo ali o resultado (~26ms) está longe de ser um problema
real: nenhum timeout, nenhuma degradação perceptível numa única requisição.
Pagar escrita mais lenta em toda mutação de `tasks` (a issue já nomeia esse
custo) por um ganho que só aparece numa escala hoje hipotética não se
justifica.

**Quando revisitar — os mesmos gatilhos que a issue já nomeava:** a busca por
título (15.G2, issue #248) e a ordenação por outro campo (15.G3, issue #249)
introduzem formas de consulta que esta medição não cobriu (`LIKE`/`ILIKE` ou
busca textual, e um `ORDER BY` diferente de `created_at, id`) e que mudam o
plano de consulta de verdade — ao contrário de `status`/`priority`, que só
adicionam um `Filter` sobre o mesmo índice já existente. Se um desses
entrar, repetir esta mesma medição (não assumir que o resultado daqui ainda
vale) contra o índice que aquela consulta específica pedir.

---

## Validação de senha forte (issue #218, 15.B1): regra local, não HIBP

Antes desta issue, `user.validatePassword` checava só `8 <= len(password) <=
72` bytes — o próprio comentário do `minPasswordLen` já admitia isso como "a
baseline strength floor, not a full policy". `"12345678"` e `"password"`
passavam.

**As duas rotas que a issue nomeava, e a escolhida:** uma regra local de
composição/entropia, sem dependência nova e sem chamada de rede; ou
verificação de vazamento via HIBP com k-anonimato, que pega exatamente a
senha que a regra local aprova mas o mundo já conhece, ao custo de uma
chamada externa síncrona em todo cadastro/troca de senha e de decidir o que
fazer quando esse serviço está fora do ar (falhar aberto ou fechado).
**Escolha do usuário (`JonasBorgesLM`)**, levada explicitamente porque a
issue marcava a escolha como a própria tarefa: regra local — alinhada com a
preferência já estabelecida deste projeto por checks autocontidos, sem
serviço externo (a mesma razão, por exemplo, por trás de `dummyPasswordHash`
nunca depender de nada fora do processo).

**O que a regra local faz — três checks, nenhum é uma exigência de
composição de caracteres:**
1. **Lista de senhas conhecidas** (`commonWeakPasswords`,
   `internal/user/password_strength.go`) — comparação case-insensitive
   contra ~150 entradas vindas de rankings públicos de senha mais comum
   (SplashData/NordPass) e suas decorações mais previsíveis. `"Password1!"`
   está na lista explicitamente: é o exemplo canônico de senha que satisfaz
   qualquer regra de composição de caracteres e ainda assim é um dos
   primeiros palpites de qualquer ataque real.
2. **Rune única repetida** (`isSingleRepeatedRune`) — `"aaaaaaaa"`,
   `"11111111"`. Cobre qualquer rune, não só ASCII, então não depende de
   estar numa lista.
3. **Sequência ascendente/descendente de code points**
   (`isSequentialRun`) — `"12345678"`, `"abcdefgh"`, `"87654321"`. Genérico
   por design (compara deltas entre runes adjacentes) em vez de uma tabela
   de layout de teclado — mais barato e cobre a família inteira de "só
   digitei os próximos N caracteres" sem enumerar cada caso.

**Deliberadamente sem regra de composição obrigatória** (nunca "precisa ter
maiúscula E dígito E símbolo"). NIST SP 800-63B recomenda contra isso
especificamente: empurra o usuário para decorações previsíveis —
`"Password1!"` é o exemplo padrão citado pelo próprio NIST — sem elevar a
entropia real. Os três checks acima miram o que de fato torna uma senha
adivinhável primeiro, não o que só parece complexo.

**O que isto não cobre, por design:** a lista local é de algumas centenas de
entradas, não as centenas de milhares que um corpus de vazamento real (tipo
HIBP) teria — pega as senhas que todo mundo já sabe que são fracas, não toda
senha que já vazou algum dia. Um padrão intercalado como `"12121212"` também
passa: não está na lista, não é rune única repetida, não é sequência por
delta constante. Cobrir isso exigiria um estimador de entropia de verdade
(tipo zxcvbn) — fora do escopo desta issue, que pedia composição/entropia
local simples, não um motor de análise de senha.

**`"password123"` está deliberadamente fora da lista**, apesar de
genuinamente pertencer a ela: é a senha de demonstração/teste já
estabelecida deste projeto — default de `cmd/seed -password`, e usada como
fixture em dezenas de testes que passam pelo caminho real de
Register/Login em `internal/user`, `internal/task` e `cmd/api`. Bloqueá-la
aqui quebraria a ferramenta de seed e boa parte da suíte por uma string que
já está documentada como "demo only — never reuse" no seu único ponto de
uso próximo de produção (`cmd/seed/main.go`) — o risco que esta lista existe
para fechar não se aplica a um valor que nada real deveria autenticar.

**O teto de 72 bytes (não runes) para `maxPasswordLen` não mudou** — é o
próprio limite do `bcrypt`, e a assimetria proposital com o e-mail (medido em
runes via `validate.MaxLen`) continua documentada em `validatePassword`'s doc
comment.

### Frontend (issue #219, 15.B2): checklist ao vivo, não pontuação — e nunca um portão

`web/src/features/auth/RegisterPage.tsx` tinha um único campo de senha e a
dica estática "At least 8 characters" — um erro de digitação criava uma
conta cuja senha ninguém sabia, sem forma de recuperar (15.B4 ainda não
existe). Duas mudanças, ambas exigidas pela issue: confirmação de senha, e
"medidor de força que espelha a regra do servidor e nunca inventa uma mais
frouxa".

**Checklist contra os predicados reais do servidor, não uma pontuação
fraca/média/forte.** Uma pontuação (tipo zxcvbn) é exatamente o tipo de
coisa que poderia dizer "forte" para uma senha que o servidor ainda
rejeitaria — o oposto do que a issue pedia. `PasswordRequirements.tsx`
mostra os três mesmos predicados de `internal/user/password_strength.go`
(comprimento, não-comum, não-previsível) como itens vivos de uma lista,
cada um com seu próprio estado atendido/não atendido — nunca um número
inventado que não corresponde a nenhuma regra real do lado do servidor.

**`web/src/features/auth/passwordStrength.ts` espelha o Go à mão — mesma
lista, mesmos três checks, mesma exclusão deliberada de `"password123"`
(ver acima).** Este projeto não tem geração de código entre Go e
TypeScript para uma regra como esta; manter os dois arquivos sincronizados
manualmente foi a escolha proporcional ao tamanho do problema (uma lista
de ~150 strings e três funções puras), não algo que justifique construir
ferramenta de codegen. Se a lista ou os checks mudarem de um lado, mudam
do outro na mesma alteração — comentário de topo em ambos os arquivos
aponta um para o outro.

**Puramente informativo, nunca um portão client-side.** `registerSchema`
(Zod) só bloqueia envio por comprimento (8–72, espelhando o servidor) e
por confirmação não bater — nunca por um resultado de
`isCommonWeakPassword`/`isSingleRepeatedRune`/`isSequentialRun`. Uma senha
que passa no comprimento mas falha o checklist ainda chega ao servidor e
recebe o `400` real dele: a alternativa (replicar a rejeição também no
schema do formulário) duplicaria a decisão de política em dois lugares
que já são mantidos à mão — e um deles ficaria, mais cedo ou mais tarde,
desatualizado em relação ao outro sem que ninguém notasse até um usuário
real esbarrar na divergência.

---

## Atraso progressivo por conta (issue #220, 15.B3): curva, não bloqueio

Os três tiers de rate limit (`cmd/api/main.go`) protegem por endereço
(`globalLimiter`, `authLimiter`) e por usuário já autenticado
(`userLimiter`). Nenhum protege uma **conta** ainda não autenticada: um
atacante distribuído tentando senhas contra um e-mail conhecido, uma
origem por tentativa, apresenta a cada endereço poucas tentativas —
exatamente o perfil que `authLimiter` considera normal. O orçamento
contra uma conta específica era, na prática, ilimitado.

**A curva concreta era uma decisão em aberto que a própria issue não
fechava** ("contador por identificador de conta, com atraso crescente",
sem números). Três perfis foram levados ao usuário (`JonasBorgesLM`) —
moderado, agressivo, ou números especificados por ele — e o **moderado**
foi escolhido: sem atraso nas 2 primeiras falhas (tolera erro de
digitação), a partir da 3ª falha `250ms × 2^(falhas-3)` até um teto de
4s, contador zerado no login bem-sucedido ou após 15 minutos sem
tentativas contra aquela conta. Números em
`internal/user/login_backoff.go`'s `loginBackoffThreshold`/
`loginBackoffBase`/`loginBackoffCap`/`loginBackoffIdleReset`.

**As duas armadilhas que a issue nomeava, e como cada uma foi fechada:**

1. **A resposta precisa continuar indistinguível de "credencial
   inválida".** Um "conta bloqueada" distinto seria exatamente o oráculo
   de enumeração que `dummyPasswordHash` já existe para fechar (ver
   `Authenticate`'s doc comment). `Service.Authenticate` calcula o atraso
   **antes** de saber o resultado da tentativa atual (com base só no
   histórico anterior daquela conta) e aplica esse mesmo atraso aos três
   desfechos possíveis — e-mail desconhecido, senha errada, ou sucesso —
   nunca só às falhas. Um atraso que só aparecesse em caso de falha seria
   ele mesmo o vazamento: um observador saberia que a conta está sob
   contenção só de ver a resposta demorar mais. `TestAuthenticate_
   DelayAppliesEvenOnSuccess` prova especificamente isso: uma senha
   *correta* ainda paga o atraso antes de suceder.
2. **Bloqueio duro é negação de serviço contra o dono legítimo.** Nada
   aqui jamais recusa uma senha correta — o atraso só cresce até o teto
   de 4s e depois pára de crescer; quem sabe a senha sempre entra, só
   espera um pouco mais. `TestAuthenticate_SuccessClearsBackoff` prova
   que um login bem-sucedido zera o contador — a conta não fica "sob
   suspeita" indefinidamente por falhas antigas já resolvidas.

**Onde mora o estado: em processo, por réplica — mesma forma de
`tokenCache` e dos três tiers de `moat/ratelimit` já existentes.** A
issue já sinalizava que fazer o contador valer o deploy inteiro seria
"uma mudança arquitetural com discussão própria" — não aberta aqui. Isso
é consistente com a topologia já documentada (réplica única em regime
estável — ver "Topologia de deploy"); o único cenário de múltiplos
processos é a sobreposição breve de um rolling update, e nesse cenário
o pior caso é um atacante conseguir uma janela de tentativas ligeiramente
maior que o perfil moderado prevê, não a ausência total de proteção.

**O que isto não cobre, por design:** o mutex de `loginBackoff` protege
a consistência do mapa, não serializa tentativas concorrentes contra a
mesma conta ponta-a-ponta. Um atacante enviando várias tentativas em
paralelo contra o mesmo e-mail pode ver todas lerem o mesmo `delay()`
(baseado no mesmo estado anterior) antes de qualquer uma delas chamar
`recordFailure` — na prática, um pequeno lote de tentativas "grátis" a
cada rajada paralela, em vez de estritamente uma por vez. Fechar isso
por completo exigiria serializar `Authenticate` inteiro por conta (um
lock por e-mail mantido durante toda a chamada, não só durante a
atualização do contador) — mudança real, mas que teria custo de
concorrência (dispositivos legítimos entrando ao mesmo tempo na mesma
conta esperariam um pelo outro) desproporcional ao que o perfil moderado
já pedia resolver. Aceito como lacuna conhecida, não como omissão
silenciosa.

**Escopo: só `POST /v1/auth/login` (`Service.Authenticate`), não
`ChangePassword`/`DeleteAccount`'s verificação de senha atual
(`verifyPassword`).** A issue nomeava especificamente o login como o
gap — `verifyPassword` já roda atrás de `RequireAuth`, coberto por
`userLimiter` (por usuário autenticado) de um jeito que `Authenticate`,
antes de saber quem é o usuário, não pode ser. Estender a mesma curva a
`verifyPassword` é um escopo maior que esta issue pedia, não uma
inconsistência desta decisão.

---

## Trilha de auditoria (issue #223, 15.B6): reaproveita o log existente, sem sink dedicado

Login, falha de login, `logout-all`, troca de senha e exclusão de conta
não deixavam registro dedicado — depois de um incidente não havia como
responder "de onde e quando" sem cruzar o log de acesso genérico (que
identifica por rota e status, não por conta) com o que quer que a
memória de alguém ainda lembrasse.

**Nenhum sink novo — o `crier` já compilado no binário é o caminho de
saída, o evento é que faltava.** `cmd/api/crier.go`'s `crierTeeHandler`
já espelha **todo** `slog.Record` que passa por `h.logger` para o
coletor OTLP configurado (`CRIER_OTLP_ENDPOINT`), sem que o call site
precise saber que o `crier` existe. Isso significa que fechar esta issue
não pedia nenhuma infraestrutura nova — só cinco chamadas de log
estruturado nos pontos certos, mais um jeito de obter o endereço de
origem sem duplicar a lógica de resolução que o rate limit já tem.

**`Handler.logAuditEvent`** (`internal/user/handler.go`) emite uma linha
`Info` com `event_type`, `account`, `source_ip` e `request_id` — nunca
senha, token de sessão ou hash do token, a lista de "nunca registrar" que
a própria issue nomeava. Cinco call sites, um por evento pedido:
`login_success`/`login_failure` (`login`), `logout_all` (`logoutAll`),
`password_changed` (`changePassword`), `account_deleted`
(`deleteAccount`). Criação/revogação de link (Bloco A) fica de fora
porque o Bloco A ainda não foi implementado — quando entrar, ganha seu
próprio call site nesta mesma função, não uma reabertura desta decisão.

**`account` tem dois significados diferentes, e isso é deliberado, não
inconsistência.** Para `login_success`/`login_failure` é o e-mail
normalizado submetido; para os outros três é o ID do usuário autenticado
(já disponível via `middleware.UserIDFromContext`). A razão é estrutural,
não estilística: `Service.Authenticate` devolve o mesmo
`ErrInvalidCredentials` tanto para e-mail desconhecido quanto para senha
errada — de propósito, ver o próprio doc comment de `Authenticate` — o
que significa que `Handler` **não tem como saber** qual dos dois casos
ocorreu nem para uso interno de auditoria. O e-mail submetido (já
disponível no corpo da requisição, antes de qualquer resolução) é o único
identificador que os dois casos de falha realmente compartilham; forçar
`account_id` também para login exigiria mudar a assinatura de
`Authenticate` para vazar internamente uma distinção que o resto do
sistema foi construído para nunca vazar — mudança maior, e mais arriscada,
do que esta issue pedia.

**`middleware.RealIP` (novo) resolve o endereço de origem uma vez por
requisição e o guarda no contexto**, reaproveitando exatamente a mesma
função (`addressKeyFunc`, já usada pelos tiers de rate limit por
endereço) que já passa por `realip`/`TRUSTED_PROXIES` — a mesma
preocupação que a issue nomeava explicitamente: "um `X-Forwarded-For` cru
numa trilha de auditoria é pior que nenhuma trilha". Isso garante que a
trilha de auditoria e o rate limit **nunca podem discordar** sobre qual é
o endereço de um cliente, porque são literalmente a mesma chamada de
função — uma segunda implementação de "resolver o endereço real" teria
sido exatamente o tipo de duplicação que pode silenciosamente divergir.
`internal/middleware` continua sem conhecimento de domínio: `RealIP` só
sabe resolver e guardar um endereço, não o que é um "evento de
auditoria" — quem faz essa ponte é `user.Handler`, o lado que tem
conhecimento de domínio.

**Por que não em `Service`, e sim em `Handler`.** Um evento de auditoria
é inerentemente uma preocupação de "o que aconteceu nesta requisição
HTTP" — precisa do endereço de origem e do request ID, nenhum dos dois
algo que `Service` deveria conhecer (ver `CLAUDE.md`'s regra de
camadas). `Service` continua retornando só o que já retornava;
`Handler` decide, a partir do resultado, se e como logar.

---

## Tela de sessões ativas (issue #224, 15.B7): id derivado, sem coluna nova

`POST /v1/auth/logout-all` derruba tudo, inclusive a sessão que está
chamando — é a operação de "suspeito que vazou, mata tudo", e continua
correta assim. Mas não havia nada entre "esta sessão" e "todas": o
usuário não via quantas sessões existiam, de quando, nem conseguia
derrubar só a suspeita sem derrubar as outras também.
`AuthMaxSessionsPerUser` (padrão 10) já limitava o total, e
`idx_sessions_user_id_created_at` já indexava exatamente a consulta que
faltava expor — só faltavam as duas rotas e a tela.

**"Identificador opaco derivado", não uma coluna nova — decisão levada
ao usuário (`JonasBorgesLM`) entre as duas formas de fazer isso.**
`GET /v1/auth/sessions` e `DELETE /v1/auth/sessions/{id}` nunca podem
expor o token nem seu hash (`sessions.token_hash`, a chave primária da
tabela) — precisavam de um identificador próprio para endereçar cada
sessão. Duas rotas possíveis: uma coluna `id` nova (gerada em
`CreateSession`, lookup `O(1)` por `WHERE id = $1`, mas exige migration
nova tocando `migrate_test.go` e o schema) ou um id computado sob
demanda a partir do `token_hash` já existente (sem migration nenhuma,
ao custo de `ListSessions`/`RevokeSession` recalcularem o id para cada
linha que `FindSessionsForUser` devolve — no máximo
`AuthMaxSessionsPerUser`, hoje 10). **Escolhida a segunda** — o ganho de
performance da primeira é irrelevante nessa escala, e "derivado" era
literalmente a palavra que a issue já usava.

**`deriveSessionID`** (`internal/user/service.go`) aplica um segundo
SHA-256 sobre `sessionIDPrefix + tokenHash` — domain-separado do próprio
`hashToken` (que hashea o token cru, nunca visto aqui) por um prefixo
fixo, para que as duas finalidades nunca colidam por acidente mesmo
operando sobre dados relacionados. Não é reversível de volta a
`tokenHash` — defesa em profundidade, não o requisito real: `tokenHash`
já é um valor que nada legítimo precisa reconstruir, já que o único
dado realmente sensível (o token cru) nunca entrou nessa cadeia de
derivação.

**`RevokeSession` varre as sessões do usuário e compara o id derivado de
cada uma** com o `id` recebido, em vez de um lookup direto — o custo
aceito pela escolha acima. Revogar a própria sessão atual por esta rota
não tem tratamento especial: é exatamente o que `POST /auth/logout` já
faz por outro endereço, e recusar seria uma inconsistência arbitrária,
não uma proteção real. Um `id` que não bate com nenhuma sessão do
usuário — inclusive um que endereça de verdade uma sessão de **outra**
conta — devolve `ErrNotFound`, a mesma disciplina de nunca confirmar a
existência de uma linha que não pertence a quem pergunta que todo outro
lookup de recurso único nesta API já segue.

**`is_current` existe porque o chamador não tem outro jeito de saber
qual das suas sessões é "esta".** `ListSessions` recebe o token cru que
autenticou a própria chamada (via
`middleware.SessionTokenFromContext`), hashea, e compara contra cada
sessão devolvida — o único lugar onde o token cru e o hash persistido se
encontram nesta função, e só para comparação, nunca para retorno.

**Frontend: "Manage sessions" no menu de conta, não navegação
primária** — é a segunda página autenticada, mas continua sendo um
desvio de configurações alcançado pelo menu, não algo que justifique
uma segunda aba/link no cabeçalho (ver o próprio comentário de
`AppShell.tsx`). Revogar a sessão marcada "This device" também não tem
tratamento especial no frontend, pelo mesmo motivo do backend: a
próxima chamada à API depois disso recebe `401` e cai no mesmo fluxo de
`useAuth` que já trata uma sessão invalidada por qualquer outro motivo.

---

## Total real na listagem (issue #237, 15.E1) e `GET /tasks/stats` (issue #238, 15.E2): header aditivo, sempre calculado

`GET /v1/tasks` sempre devolveu só a página pedida — sem `limit`/`offset`
explícitos, "todas as tasks" já era a página inteira, mas com eles o
chamador nunca sabia quantas linhas existiam além da página em mãos.
A issue pedia explicitamente para medir o custo antes de decidir a forma
da resposta, e as duas issues foram implementadas juntas porque #238
("Depende de: 15.E1 (mesma decisão de forma de resposta)") reusa a mesma
pergunta: adicionar um total nunca deve custar uma segunda leitura da
tabela inteira em Go, tem que ser `COUNT(*)`/`GROUP BY` no próprio banco.

**Forma escolhida: header `X-Total-Count`, não um envelope
`{data, meta}`.** Um envelope muda o formato de toda resposta de
`GET /v1/tasks` — quebra qualquer cliente que já faz
`response.json()` esperando um array diretamente, incompatível com o
contrato que `/v1` já promete (ver `docs/DECISIONS.md` § "A contract is
mounted under /v1" em `CLAUDE.md`, e a regra de nunca reinterpretar o
que `/v1` já significa). Um header é estritamente aditivo: nada que já
lê o corpo da resposta precisa mudar, e um cliente que quer o total
passa a ler um header a mais. `X-Total-Count` foi escolhido sobre um
nome próprio porque é a convenção já estabelecida por várias APIs REST
para exatamente este propósito.

**Medição feita, mesma metodologia da issue #236 acima (volume
sintético gerado direto via SQL, descartado depois):**

1. **50 usuários × 5.000 tasks cada** (~350k linhas nesta rodada) —
   `EXPLAIN ANALYZE` de `SELECT COUNT(*) FROM tasks WHERE user_id = $1
   [AND status IN (…)] [AND priority IN (…)]` para um usuário típico
   ficou entre **1ms e 3ms**, servido por Bitmap Heap Scan através de
   `idx_tasks_user_id_created_at_id` — o mesmo índice que já sustenta
   `FindAll`, e sem overhead perceptível de rodar como uma segunda
   consulta ao lado dela.
2. **Um único usuário com 100.000 tasks** — cenário deliberadamente
   extremo, o mesmo usado em #236: aqui o usuário passa a dominar a
   tabela inteira, o planner troca para Seq Scan, e o `COUNT(*)` sobe
   para **~28ms**. Ainda assim, longe de um problema real — nenhum
   timeout, nenhuma degradação visível numa única requisição.

**Decisão: sempre incluir o total, em toda chamada — sem parâmetro
opt-in.** Um parâmetro (`?include_total=true`) evitaria o custo para
quem não precisa, mas o próprio custo medido (1-3ms no caso pesado
realista) não justifica a complexidade extra de um comportamento
condicional documentado, testado e mantido nos dois `Repository`. Se
`status`/`priority` chegarem a crescer numa direção que mude esse
número (ver os mesmos gatilhos já nomeados na issue #236 — busca
textual, ordenação por outro campo), esta medição deve ser repetida
antes de assumir que ainda vale.

**`X-Total-Count` é calculado antes da checagem de `ETag`/
`If-None-Match`, e por isso está presente tanto num `200` quanto num
`304`.** O validador da listagem (`pageETag`) é derivado só das linhas
da página atual — duas requisições podem carregar o mesmo `ETag` mesmo
que o total tenha mudado (uma task nova em outra página não move as
linhas desta, mas move o total). Calcular o header antes do `return`
do `304` é o que impede essa resposta de servir um total desatualizado
por engano; a ordem é fixada por
`TestListTasks_Handler_XTotalCountSurvivesNotModified`, verificado por
controle negativo (mover o `Set` do header para depois do `if
ifNoneMatchHits` faz o teste falhar como esperado antes de restaurar a
ordem correta).

**`GET /v1/tasks/stats` reusa o mesmo filtro `status`/`priority` e a
mesma validação de `GET /v1/tasks`**, via `buildTaskFilterWhere`
compartilhado entre `FindAll`, `CountAll` e `CountByStatusAndPriority`
(`internal/task/postgres_repository.go`) — extraído nesta mudança para
que as três consultas nunca possam divergir silenciosamente sobre o que
"casar com o filtro" significa. `by_status`/`by_priority` sempre
incluem todo valor do enum, mesmo em `0`: `GROUP BY` só devolve grupos
que existem, e um chamador não deveria ter que distinguir "zero tasks
neste grupo" de "esta chave simplesmente não apareceu" — os dois
`Repository` zeram os dois mapas antes de aplicar os resultados da
consulta/da contagem em memória.

**A rota `GET /tasks/stats` é registrada antes de `GET /tasks/{id}`**
por legibilidade, mas isso não é o que a protege de ser interpretada
como um id de task: o `ServeMux` do Go 1.22+ já prefere um segmento
literal (`stats`) sobre um `{wildcard}` na mesma posição,
independentemente da ordem de registro. `TestTaskStats_Handler_RoutedCorrectly`
prova isso na prática, passando pelo `http.ServeMux` real (não chamando
o handler diretamente) e confirmando que a rota nunca cai em `getTask`
tratando `"stats"` como um id.

---

## Exportação CSV de tasks (issues #239-#243, 15.F1-15.F5)

Função nova — não existia CSV nem impressão no projeto antes disso. Cinco
issues, quatro decisões levadas ao usuário (`JonasBorgesLM`) porque cada
uma é um trade-off real sem resposta universalmente certa; a quinta
(rota dedicada vs. negociação de `Accept`) foi decidida diretamente
durante a implementação, com o raciocínio registrado abaixo.

### Quem gera o CSV (issue #239, 15.F1): servidor

**Alternativa rejeitada: gerar no cliente**, paginando o conjunto
inteiro em JavaScript. Seriam N requisições sem transação, com risco
real do conjunto mudar no meio de uma exportação — e o filtro/dono/
ordenação já resolvidos num só lugar no servidor teriam que ser
reimplementados no cliente, uma segunda cópia da regra livre para
divergir da primeira.

**Escolhido: servidor**, via uma rota que reusa a mesma autorização e o
mesmo filtro que `GET /v1/tasks` já aplica, com o corpo transmitido em
fluxo (`encoding/csv` escrevendo direto no `ResponseWriter`, nunca
montando a resposta inteira em memória antes de escrever).

**Trade-off aceito:** o servidor paga o custo de gerar e transmitir o
arquivo inteiro numa única requisição HTTP, sujeita ao `WriteTimeout` já
existente — daí a necessidade do teto de linhas (15.F5, abaixo).

### Rota dedicada, não negociação por `Accept` (issue #240, 15.F2)

A issue apresentava duas formas igualmente válidas de expor o mesmo
recurso: uma rota dedicada (`GET /tasks/export`) ou negociar o formato
de `GET /tasks` existente via `Accept: text/csv`. Decisão tomada durante
a implementação, não levada ao usuário, porque o próprio código já
tinha um precedente direto para copiar: `GET /tasks/stats` (issue #238)
já resolveu exatamente esta pergunta — "uma rota nova por segmento
literal, nunca sombreada por `/tasks/{id}`" — para uma necessidade com a
mesma forma (mais uma visão sobre o mesmo conjunto filtrado).

**Alternativa rejeitada: negociação por `Accept`.** Manteria um único
caminho de filtro/autorização, mas exigiria ramificar `listTasks` por
tipo de conteúdo — paginação, `ETag`/`X-Total-Count` e o teto de linhas
do export não fazem sentido nos dois formatos ao mesmo tempo, então a
ramificação teria que existir de qualquer forma, só que dentro de um
único handler em vez de dois. Documentar dois comportamentos tão
diferentes sob um único `operationId` no OpenAPI também ficaria confuso.

**Escolhido: `GET /tasks/export`**, rota própria, mesmo tratamento de
`ServeMux` que `/tasks/stats` já usa (segmento literal, nunca sombreado
por `/tasks/{id}` independente da ordem de registro — confirmado por
`TestExportTasks_Handler_RoutedCorrectly`, o mesmo padrão de
`TestTaskStats_Handler_RoutedCorrectly`).

### Neutralizar injeção de fórmula no CSV (issue #241, 15.F3)

Um título ou descrição começando com `=`, `+`, `-`, `@`, tab ou CR é
interpretado como fórmula pelo Excel/Sheets ao abrir o arquivo — a
mesma classe (CWE-1236) documentada na issue. A defesa
(`sanitizeCSVCell`, `internal/task/csv_export.go`) prefixa um apóstrofo
a qualquer célula que comece com um desses caracteres, exatamente onde
a issue pedia: na escrita do CSV, nunca perto de
`validateTitleAndDescription` — dentro da aplicação esses caracteres
são texto legítimo, e o risco só nasce no momento em que o valor vira
célula de planilha.

**Achado durante a implementação, não previsto pela issue:**
`encoding/csv.Writer` com `UseCRLF = true` (obrigatório para RFC 4180,
ver seção seguinte) **descarta silenciosamente um `\r` isolado dentro de
um campo** — o pacote assume que qualquer `\r` que aparece ali pertence
a um par `\r\n` na convenção de quebra de linha do próprio texto de
origem, não ao terminador de linha do escritor. Confirmado
empiricamente (não assumido) escrevendo o campo `"'\rcmd"` e inspecionando
os bytes de saída: o `\r` nunca chega ao arquivo, restando `'cmd`. Na
prática isso significa que o gatilho CR é neutralizado duas vezes de
forma independente — pelo apóstrofo de `sanitizeCSVCell` e, mesmo sem
ele, pelo próprio `encoding/csv` — o que só reforça a defesa, nunca a
enfraquece. `TestExportTasks_Handler_NeutralizesFormulaInjection`
documenta esse comportamento explicitamente no caso CR em vez de deixar
um teste falhando parecer um bug.

**Controle negativo aplicado** (mesmo padrão do resto do projeto):
removida temporariamente a chamada a `sanitizeCSVCell` em
`exportTasks`, confirmado que
`TestExportTasks_Handler_NeutralizesFormulaInjection` falha em todo
caso com gatilho (e continua passando no caso de texto comum, provando
que a asserção não estava vazia), restaurada a chamada.

### Formato do arquivo (issue #242, 15.F4)

**RFC 4180:** `csv.Writer.UseCRLF = true` — o padrão do
`encoding/csv` do Go é `\n` puro, não `\r\n`; precisa ser ligado
explicitamente. Cabeçalho de colunas fixo (`id, title, description,
status, priority, created_at, updated_at`) tratado como contrato: uma
coluna nova entra no fim, nunca no meio.

**Nome e `Content-Disposition`:** montado por `mime.FormatMediaType`,
nunca à mão — o mesmo padrão que `internal/attachment/handler.go` já
usa para o download de anexos, pela mesma razão (um nome com aspas ou
acento não pode quebrar o valor do header). Nome inclui a data e, se
houver filtro, uma indicação dele (`tasks-2026-09-08-status-pending.csv`)
— um arquivo baixado mais de uma vez, ou comparado com o de outro dia,
precisa do próprio escopo legível sem abrir o arquivo.

**BOM UTF-8: incluído — decisão levada ao usuário.** Sem BOM, o Excel
no Windows interpreta mal o byte de acentuação e corrompe título/
descrição em português, o conteúdo mais comum deste projeto. Com BOM,
algumas ferramentas de linha de comando leem os três bytes como parte
da primeira coluna. **Escolhido incluir o BOM** porque o público
principal deste export (uma pessoa abrindo o arquivo no Excel para ler
ou imprimir, não um pipeline automatizado) é exatamente o caso que o
BOM resolve, e é o caso que a ausência dele quebra de forma visível e
confusa (acentos virando caracteres ilegíveis) — o custo do BOM para
quem usa uma ferramenta de linha de comando é, na pior hipótese,
precisar aparar três bytes conhecidos, um problema bem documentado e
fácil de contornar.

**Trade-off aceito:** um parser de CSV ingênuo que não trata BOM pode
ler a primeira coluna do cabeçalho como `"﻿id"` em vez de `"id"`.

### Teto do export (issue #243, 15.F5): 10.000 linhas, `400` antes de transmitir — decisão levada ao usuário

O tier por usuário do `moat/ratelimit` já cobre a frequência de chamadas
(o export fica atrás de `authenticated`, como toda rota de task) — o que
faltava era um teto de **linhas**, já que o export por definição devolve
o conjunto inteiro (nunca janelado por `limit`/`offset`, ao contrário de
`GET /tasks`) sob o `WriteTimeout` de 10s do servidor.

**Truncar em silêncio foi descartado explicitamente** (a própria issue
já nomeia isso como "a pior saída possível"): produziria um arquivo que
parece completo e não é, e quem o imprimir não tem como saber. A
alternativa adotada é responder honestamente antes de começar: `Service.
ExportTasks` chama `Repository.CountAll` **antes** de `FindAll`, e
rejeita com `ErrInvalidInput` (`400`) se o total do filtro passar do
teto — nada é buscado nem escrito nesse caso
(`TestExportTasks_RejectsAboveCap` confirma que `FindAll` nunca é
chamado).

**Por que 10.000 e não outro número:** confortável dentro do
`WriteTimeout` de 10s mesmo numa conexão lenta — cada linha do CSV tem
algumas centenas de bytes, então mesmo 10k linhas ficam na casa de
poucos MB, ordens de magnitude abaixo do que um upload lento
conseguiria saturar em 10s — e bem acima do uso esperado de um
gerenciador de tasks pessoal (o mesmo raciocínio de escala já usado em
`docs/DECISIONS.md` § "Índices para status/priority", issue #236: uma
conta com dezenas de milhares de tasks não é uma forma de uso que este
produto tem qualquer indício de precisar suportar hoje).

**Mapeamento de erro:** `ErrInvalidInput`, não um sentinel novo — mesmo
padrão já usado por `attachment.Service.Upload` para o limite de
tamanho de upload (`ErrTooLarge` da store vira `ErrInvalidInput` no
Service): "o pedido como está não pode ser atendido, mude o filtro" é
uma instância do mesmo `400` que qualquer outro filtro inválido já usa,
não um código novo (`docs/openapi.yaml`'s "Status codes this API has
already settled" não precisou de uma linha nova).

**Quando revisitar:** se o teto passar a ser atingido com frequência
real, a resposta certa é um export assíncrono (gerado em background,
baixado depois pronto) — issue nova, não um remendo neste teto, como a
própria 15.F5 já nomeia.
