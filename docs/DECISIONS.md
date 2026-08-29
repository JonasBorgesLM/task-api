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
