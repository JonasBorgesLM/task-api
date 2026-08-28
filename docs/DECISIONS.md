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

## SigNoz: Docker Compose oficial, mesma máquina do cluster, ligado à rede do `kind` (issue 11.5)

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

### A pergunta que não podia ser assumida: como um pod alcança um serviço no host

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
