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

## Fase 11: crier embutido, stdout mantido em paralelo

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

### Um "tee" de `slog.Handler`, não uma chamada por call site

A alternativa considerada foi adicionar `crier.Log(...)` em cada um dos
~20 call sites de log do projeto (cmd/api e os três Handlers de domínio).
Rejeitada: exigiria mudar toda linha de log existente e criaria dois
caminhos para divergir. Em vez disso, `crierTeeHandler` embrulha o
`slog.Handler` uma única vez, em `run()` — todo `logger.Error/Info/Warn`
já existente passa a alcançar o crier automaticamente, `request_id`
incluído (é só mais um atributo que a linha de log já carregava).

### Um achado real, não só leitura de documentação: atributos precisam de conversão

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

### Custo de dependência (issue 11.3)

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

### Nome de serviço e versão

`Options.ServiceName` é a constante `"task-api"` (`crierServiceName`).
`Options.ServiceVersion` fica vazio por ora — o mecanismo de
versão/commit do binário (`version`/`commit` em `cmd/api/main.go`) ainda
não existe nesta branch (issue #83/#84, PR separado). Preenchê-lo é
consequência de uma linha só assim que aquele PR mergear; não vale
duplicar aqui o mecanismo de detecção de versão só para adiantar este
campo opcional.

### `crierShutdownTimeout`: provisório, não a aritmética final

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

### `/health/ready` não é acoplado ao crier (issue 11.8)

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
