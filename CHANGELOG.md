# Changelog

Formato baseado em [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
versionamento seguindo [Semantic Versioning](https://semver.org/lang/pt-BR/).

Este é o primeiro release versionado do projeto — não há tags anteriores.
`v1.0.0` marca o ponto em que a API passa a ter contrato estável (`/v1`).

## [1.0.0] — a definir na tag

### Adicionado
- Anexos em tasks: upload, download, dois backends de storage
  intercambiáveis (filesystem local ou S3-compatível via `minio-go`),
  coletor de blobs órfãos.
- `DELETE /v1/files/{key}` — remove um anexo individual. Metadado e blob
  saem na mesma requisição, não só o metadado com o blob deixado para o
  coletor de órfãos (ver `docs/DECISIONS.md`).
- Quota de armazenamento de anexos por usuário
  (`ATTACHMENT_MAX_BYTES_PER_USER`, 500 MiB por padrão). Checada antes do
  upload; um usuário já no limite recebe `400` sem que o arquivo seja
  lido.
- Teto de sessões simultâneas por usuário (`AUTH_MAX_SESSIONS_PER_USER`,
  10 por padrão) — login além do limite evict a sessão mais antiga em vez
  de recusar. `POST /v1/auth/logout-all` — encerra todas as sessões da
  conta de uma vez, incluindo a que fez a chamada, para quem suspeita de
  um token vazado.
- Rate limiting (via `moat`) agora cobre toda a superfície da API, não
  só `/auth/*`. Um cliente que fazia rajadas contra rotas de task pode
  passar a ver `429`. Configure `TRUSTED_PROXIES` se a API roda atrás de
  proxy reverso — sem isso, todos os clientes dividem um único balde de
  limite.
- Headers de segurança (`secureheaders`, via `moat`) em todas as
  respostas, inclusive de erro.
- Sanitização de campos de texto livre (título/descrição) antes de
  persistir.
- `govulncheck` fail-closed no gate de CI — merge bloqueado se uma
  vulnerabilidade acionável for encontrada.
- Deploy validado sem downtime em Kubernetes (rolling update, probes,
  drain configurável via `HTTP_PRE_SHUTDOWN_DELAY`).
- Espelhamento opcional de logs para um coletor OTLP/HTTP (SigNoz, um
  OTel Collector, etc.), via `CRIER_OTLP_ENDPOINT` — desligado por
  padrão, e nunca substitui o log em stdout. `GET /debug/vars` ganha
  `crier_buffer_depth` e `crier_records_dropped` quando habilitado.
  Detalhes em `docs/DECISIONS.md`.

### Alterado
- **BREAKING:** todas as rotas passam a exigir o prefixo `/v1`. Um
  cliente chamando os caminhos antigos sem prefixo recebe `404`. Rotas
  operacionais (`/health`, `/health/ready`, `/debug/vars`) permanecem
  sem prefixo, deliberadamente — ver `docs/DECISIONS.md`.
- Go atualizado para 1.26.6, fechando 4 vulnerabilidades da stdlib que o
  código alcançava.

### Não muda
- Autenticação por token via header já era obrigatória desde release
  anterior (PR #39) — não é mudança deste release.

### Migração necessária
1. Aplicar a migração `0007` (schema de anexos).
2. Atualizar clientes para usar o prefixo `/v1`.
3. Definir `ATTACHMENT_STORAGE_DIR` ou as variáveis `ATTACHMENT_S3_*`
   conforme o backend de storage escolhido — sem isso, os endpoints de
   anexo não são registrados.
4. Se a API roda atrás de proxy reverso, configurar `TRUSTED_PROXIES`.

### Dívidas técnicas conhecidas, não bloqueantes
- Manifests de Kubernetes usam `Secret` nativo (base64, não criptografia)
  para credenciais do S3 — mitigado por aviso explícito no manifest;
  solução real depende da definição do cluster de produção (issue #62).
