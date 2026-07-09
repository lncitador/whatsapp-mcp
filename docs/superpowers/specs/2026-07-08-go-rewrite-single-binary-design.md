# whatsapp-mcp: rewrite em Go, binário único + releases

**Data:** 2026-07-08
**Status:** aprovado
**Repo destino:** fork `github.com/lncitador/whatsapp-mcp` (upstream: `lharries/whatsapp-mcp`)

## Problema

Hoje o usuário precisa de toolchain Go (bridge) + Python/uv (MCP server) e de clonar o
repo para usar o projeto. Além disso:

- `search_contacts` não encontra contatos por nome: consulta apenas `messages.db`
  (`whatsapp-mcp-server/whatsapp.py:393`) e ignora a tabela `whatsmeow_contacts`
  em `whatsapp.db`, onde os nomes vivem.
- O bridge precisa de um terminal aberto (ou da skill tmux) para ficar de pé.
- Re-autenticação por QR exige olhar o terminal do bridge.

## Decisão

Reescrever o MCP server (Python, ~1100 linhas) em Go, dentro do módulo do bridge,
produzindo **um binário único `whatsapp-mcp`** distribuído via GitHub Releases com
script de instalação. Big-bang: a reestruturação e a remoção do Python acontecem no
mesmo PR (validação de paridade antes do merge). O código Python é deletado do repo.

Decisões de percurso (registradas nas conversas de 2026-07-08):

| Decisão | Escolha |
|---|---|
| Ordem dos sub-projetos | Especificar tudo antes de implementar |
| Repo de releases | Fork pessoal `lncitador/whatsapp-mcp` |
| UX de instalação | Binários no GitHub Releases + `install.sh` |
| Empacotamento do MCP server | Port para Go (binário único) |
| Fix `search_contacts` em Python antes? | Não — nasce correto no port |
| Arquitetura processo | Daemon + proxy stdio (mesmo binário) |
| Subida do daemon | Proxy auto-inicia daemon on-demand |
| Estratégia do rewrite | Big-bang (opção B) |

## Arquitetura

Um binário, três subcomandos:

- **`whatsapp-mcp serve`** — daemon. Sessão whatsmeow, SQLite
  (`messages.db` + `whatsapp.db`), HTTP local (default `localhost:8080`,
  bind **somente** `127.0.0.1`): API REST interna + health/status/QR.
- **`whatsapp-mcp stdio`** — proxy fino spawnado pelo cliente MCP (Claude etc.).
  Implementa o servidor MCP sobre stdio e traduz cada tool para a API REST do
  daemon. Se o daemon não responde, auto-inicia (ver Lifecycle).
- **`whatsapp-mcp status`** — diagnóstico CLI: daemon vivo? WhatsApp conectado?
  QR pendente?
- **`whatsapp-mcp stop`** — encerra o daemon.
- **`whatsapp-mcp --version`** — versão embutida via ldflags.

**SDK MCP:** preferir o SDK oficial `github.com/modelcontextprotocol/go-sdk`;
fallback `github.com/mark3labs/mcp-go` se o oficial não cobrir algo necessário
(verificar na fase de plano).

### Tools MCP (port 1:1 das 12 existentes)

`search_contacts`, `list_messages`, `list_chats`, `get_chat`,
`get_direct_chat_by_contact`, `get_contact_chats`, `get_last_interaction`,
`get_message_context`, `send_message`, `send_file`, `send_audio_message`,
`download_media`.

### Mudanças de comportamento (não são port 1:1)

1. **`search_contacts` busca por nome:** consulta `whatsmeow_contacts`
   (whatsapp.db) juntando com dados de chat (messages.db). Múltiplos matches →
   retorna todos com JID/nome/telefone para o agente desambiguar com o usuário.
2. **QR/auth via MCP:** tool/resource `auth_status` retorna estado da sessão e o
   QR (imagem ou texto) quando re-auth está pendente. Elimina a necessidade de
   olhar o terminal do daemon.
3. **Áudio:** conversão para opus `.ogg` continua via `ffmpeg` externo
   (`exec`), como no Python. Sem ffmpeg → erro claro pedindo ffmpeg ou arquivo
   já em `.ogg` opus.

## Layout do repo (pós-rewrite)

```
cmd/whatsapp-mcp/        main.go — dispatch dos subcomandos
internal/wa/             conexão whatsmeow, event handlers, history sync
internal/store/          SQLite: messages.db + leitura de whatsmeow_contacts
internal/api/            HTTP local: REST interno + health/QR
internal/mcpserver/      definição das 12 tools + proxy stdio
internal/audio/          wrapper ffmpeg
skills/whatsapp-bridge/  skill atualizada para usar o binário
.goreleaser.yaml
.github/workflows/release.yml
install.sh
```

`whatsapp-mcp-server/` (Python) e o layout atual de `whatsapp-bridge/` deixam de
existir; o módulo Go é renomeado/movido para a raiz.

## Daemon + proxy: lifecycle

- **Auto-start:** `stdio` faz `GET /health`; se falhar, spawna
  `whatsapp-mcp serve` detached (setsid; logs em
  `~/.whatsapp-mcp/logs/daemon.log`) e faz poll do health por até ~10s.
- **Corrida:** dois proxies simultâneos → lock file
  `~/.whatsapp-mcp/daemon.lock` (flock). Perdedor apenas espera o health.
- **Config:** porta default `8080`; override por `WHATSAPP_MCP_PORT` ou
  `~/.whatsapp-mcp/config.json`.
- **Persistência:** daemon sobrevive ao fechamento do cliente MCP (objetivo).
  Watchdog/service de SO (launchd/systemd) fica para v2; a skill
  `whatsapp-bridge` vira wrapper fino de `status`/logs/QR e o tmux+watchdog
  deixa de ser necessário.

**Protocolo proxy↔daemon:** REST interno (o que o bridge já tem hoje). Expor MCP
streamable HTTP direto do daemon fica como evolução futura.

## Dados

- **Store:** `~/.whatsapp-mcp/store/{messages.db,whatsapp.db}` (XDG-aware).
  Deixa de ser relativo ao repo.
- **Migração:** na primeira subida, se o novo store estiver vazio e existir
  store antigo (`whatsapp-bridge/store/` no cwd/repo), copiar e logar aviso.
  Evita novo scan de QR pós-upgrade.
- **Fluxo de mensagens:** whatsmeow event → `internal/wa` → `internal/store`
  (código movido de `main.go`, sem mudança de comportamento).
- **Fora de escopo:** gap recovery / `requestHistorySync` — a chamada
  `BuildHistorySyncRequest(nil, 100)` panica na whatsmeow atual
  (v0.0.0-20260630); comportamento atual é mantido.

## Release pipeline

- **Pré-requisito:** trocar `mattn/go-sqlite3` (CGO) por `modernc.org/sqlite`
  (Go puro) — sem isso a matrix de cross-compile exige toolchains C. Muda
  import e connection string; whatsmeow suporta.
- **GoReleaser + GitHub Actions:** tag `v*` → build matrix
  `darwin/linux/windows × amd64/arm64`, checksums, GitHub Release no fork.
- **`install.sh`** (`curl … | sh`): detecta OS/arch, baixa o binário do latest
  release, instala em `~/.local/bin` (ou `/usr/local/bin` com sudo) e imprime o
  snippet de configuração MCP (`claude mcp add whatsapp -- whatsapp-mcp stdio`).
  Windows: instruções manuais no README (script PowerShell em v2).
- **README** reescrito: instalação em 1 comando; seção de desenvolvimento
  (build local) separada.

## Tratamento de erros

- Daemon inalcançável após tentativa de auto-start → erro de tool com instrução
  de rodar `whatsapp-mcp status`.
- WhatsApp desconectado / QR pendente → toda tool responde apontando
  `auth_status`.
- ffmpeg ausente em `send_audio_message` → mensagem pedindo ffmpeg ou `.ogg`
  opus.

## Testes

- `internal/store`: queries das 12 tools contra fixture SQLite — incluindo
  `search_contacts` por nome com múltiplos matches.
- `internal/mcpserver`: schema e serialização das tools.
- Proxy: auto-start contra daemon fake.
- Conexão whatsmeow real: smoke test manual (sem CI).
- **Gate de paridade:** antes do merge que deleta o Python, comparar a saída
  das 12 tools Go vs Python sobre o mesmo store (checklist manual no plano de
  implementação).

## Fora de escopo

- Gap recovery / history sync sob demanda.
- Service de SO (launchd/systemd) e watchdog.
- Script de instalação PowerShell.
- MCP streamable HTTP direto do daemon.
- Autenticação na API HTTP local (mitigado: bind 127.0.0.1).
