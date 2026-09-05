# Runbook: backup e restauração

Cobre os dois lugares onde este projeto guarda dado que importa: o Postgres
(usuários, sessões, tasks, metadado de anexo) e o `BlobStore` dos anexos
(bytes — filesystem local ou S3/MinIO, ver `ATTACHMENT_S3_*` no README). Um
backup que só cobre um dos dois não é um backup, é meio backup — ver a seção
"Por que os dois juntam" abaixo.

**Fora de escopo:** `k8s/` é um cluster de validação descartável, com
Postgres e MinIO em `emptyDir` (já avisado em `README.md` e `CLAUDE.md`). Não
há dado ali que sobreviva ao pod, e portanto nada ali para este runbook
cobrir — o alvo aqui é qualquer ambiente onde o Postgres e o object storage
realmente persistem (o próprio `docker-compose.yml` local, ou uma implantação
real).

## Por que os dois juntam

`attachments`' linhas vivem no Postgres; os bytes que elas apontam para
vivem no `BlobStore` (ver `internal/attachment/repository.go`'s doc comment
— "the blob behind an Attachment lives outside the database"). Um backup só
do banco restaura uma linha `storage_key` apontando para um arquivo que
pode não existir mais no storage restaurado (ou vice-versa) se os dois não
vierem do mesmo instante. Isso não é uma dedução teórica — foi verificado
na prática (ver "Drill executado" abaixo).

## Backup

Dois passos, feitos o mais próximo possível um do outro — não há
coordenação atômica entre o Postgres e o object storage aqui, então uma
janela de escrita entre os dois passos é uma inconsistência possível, do
mesmo jeito que já existe entre gravar o blob e gravar a linha em `Upload`
(ver `internal/attachment/service.go`).

### 1. Postgres

```bash
# Dentro do container (docker-compose local); em produção, execute contra
# onde o Postgres real está — a bandeira -Fc produz o formato "custom" do
# pg_dump, que pg_restore consegue restaurar seletivamente se precisar.
docker exec <postgres-container> pg_dump -U task_api -d task_api -Fc \
  -f /tmp/task_api_backup.dump
docker cp <postgres-container>:/tmp/task_api_backup.dump ./backup/
```

### 2. Anexos (BlobStore)

**S3/MinIO** (o backend que `docker-compose.yml` e uma implantação real
tipicamente usam):

```bash
mc alias set drillsource http://<endpoint> <access-key> <secret-key>
mc mirror drillsource/<bucket> ./backup/attachments
```

**Filesystem** (`ATTACHMENT_STORAGE_DIR`, o outro backend — ver
`internal/attachment/storage.go`):

```bash
tar -czf backup/attachments-fs.tar.gz -C "$ATTACHMENT_STORAGE_DIR" .
```

## Restauração

Ordem: **anexos antes do banco**, e migrações depois do banco — nessa
ordem, não a de escrita normal do app.

**Por que anexos antes do banco, aqui — e não o inverso que `Service.Delete`
usa:** isto é fundamentalmente uma operação de *criação* (repovoar um
storage vazio), o mesmo formato de `Upload`, não de `Delete` — `Upload`
grava bytes antes da linha pela mesma razão: se a restauração for
interrompida entre os dois passos, a ordem "anexos primeiro" deixa, no pior
caso, blobs sem linha nenhuma apontando para eles ainda (inofensivo — a
próxima passada do `CollectOrphans`, depois de `ATTACHMENT_ORPHAN_MIN_AGE`,
os varre se de fato não forem referenciados por nenhuma linha real). A
ordem inversa (banco primeiro) deixaria, no mesmo cenário de interrupção,
linhas apontando para arquivos que ainda não voltaram — exatamente a
referência quebrada que a ordem de `Upload` já existe para evitar.

### 1. Restaurar os anexos

```bash
mc mirror ./backup/attachments drillsource/<bucket>
# ou, para filesystem:
tar -xzf backup/attachments-fs.tar.gz -C "$ATTACHMENT_STORAGE_DIR"
```

### 2. Restaurar o Postgres

```bash
docker cp ./backup/task_api_backup.dump <postgres-container>:/tmp/restore.dump
docker exec <postgres-container> pg_restore -U task_api -d task_api \
  --no-owner /tmp/restore.dump
```

`--no-owner` evita que `pg_restore` tente recriar papéis (`ROLE`) que podem
não existir com o mesmo nome no ambiente de destino — o schema e os dados
são o que importa aqui, não a metainformação de posse.

### 3. Rodar as migrações

```bash
DATABASE_URL=<url> go run ./cmd/migrate -direction=up
# ou: make migrate-up
```

Necessário sempre que o binário rodando é mais novo que o dump: um dump
tirado antes de uma migração mais recente ter sido aplicada precisa dela
aplicada por cima depois de restaurado, ou o binário atual encontra um
schema que não bate com o que espera. Se o dump já estiver na mesma versão
de schema do binário (o caso mais comum — backup e restauração próximos no
tempo), este passo não aplica nada e termina como no-op — visto na prática
no drill abaixo ("migrations applied" sem nenhuma migração pendente de
verdade).

### 4. Verificar

Não considere a restauração terminada por ter rodado os comandos —
confirme com uma leitura real: logar com um usuário existente, listar suas
tasks, baixar um anexo e conferir os bytes. É exatamente esse último passo
que prova que os dois backups (banco e storage) realmente voltaram
consistentes um com o outro — uma linha existir não prova que o arquivo por
trás dela também voltou.

## Risco: banco e storage restaurados de momentos diferentes

Se o dump do Postgres e o backup do storage não vierem do mesmo instante,
duas formas de inconsistência são possíveis — e uma delas é
**silenciosamente destrutiva**, não só "quebrada":

1. **Storage mais novo que o dump restaurado.** Blobs que foram enviados
   *depois* do instante do dump não têm linha nenhuma no banco restaurado —
   ficam órfãos do ponto de vista do banco que acabou de voltar.
   `CollectOrphans` (`internal/attachment/service.go`) roda periodicamente
   (ver `runPeriodicCleanup` em `cmd/api/main.go`) e apaga qualquer blob
   sem linha que já tenha `ATTACHMENT_ORPHAN_MIN_AGE` (padrão `1h`) de
   idade no storage. Um blob recém-enviado, que sobreviveu à perda de
   dados e que você acabou de restaurar com sucesso, pode ser apagado de
   verdade pelo próprio coletor de órfãos algumas horas depois — não por
   um bug, mas porque o coletor está fazendo exatamente o que promete
   fazer, com uma premissa (banco e storage consistentes entre si) que
   deixou de valer.
2. **Storage mais antigo que o dump restaurado.** Linhas no banco
   restaurado apontam para blobs que o backup do storage nunca chegou a
   conter — um download desses anexos volta `404`
   (`ErrNotFound`, ver `internal/attachment/storage.go`'s `Open`), não um
   erro que corrompe nada, mas o dado está mesmo perdido: não há como
   `CollectOrphans` ajudar aqui, ele só remove blob sem linha, nunca
   preenche linha sem blob.

**Mitigação:** tirar os dois backups o mais próximo possível no tempo (ver
"Backup" acima), e — se restaurar de backups que se sabe não serem do
mesmo instante — considerar desligar `ATTACHMENT_ORPHAN_MIN_AGE` (setando
bem alto) temporariamente até confirmar que a base restaurada está
consistente, antes de deixar o coletor periódico voltar a rodar sem essa
guarda.

## Drill executado

Testado de ponta a ponta em 2026-09-05, contra o `docker-compose.yml` local
(Postgres 17 + MinIO), não apenas escrito:

1. Registrado um usuário real, criada uma task, e enviado um anexo real
   (arquivo de texto com conteúdo único).
2. Backup: `pg_dump -Fc` do banco (89 KB) + `mc mirror` do bucket completo
   (52 objetos, 2.05 KiB).
3. Simulada a perda: `DROP SCHEMA public CASCADE` no Postgres, e
   `mc rm --recursive --force` no bucket inteiro — confirmado vazio dos
   dois lados antes de prosseguir.
4. Restaurado: `mc mirror` de volta (52/52 objetos), depois
   `pg_restore --no-owner` do dump.
5. `go run ./cmd/migrate -direction=up` — terminou com "migrations
   applied", sem nada pendente de verdade (o dump já continha o
   `schema_migrations` corrente).
6. Verificação real, não só contagem de linhas: login com o usuário
   original e a senha original funcionou (confirma que o hash bcrypt
   voltou intacto); a task criada antes do drill apareceu com todos os
   campos corretos; o anexo foi baixado via `GET /v1/files/{key}` e
   comparado byte a byte com o arquivo original enviado —
   **idêntico**.

Contagens finais pós-restauração (banco de desenvolvimento acumulado ao
longo da sessão, não um ambiente limpo): 473 `users`, 215 `tasks`, 52
`attachments`, 268 `sessions`, 8 `schema_migrations` — todas as cinco
tabelas presentes e consistentes com o que existia antes da simulação de
perda.
