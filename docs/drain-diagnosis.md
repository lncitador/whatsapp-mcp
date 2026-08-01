# Diagnóstico: backlog offline não drena (`fix/offline-sync-not-draining`)

Data: 2026-08-01. Branch `fix/offline-sync-not-draining`, HEAD `077634c`, working tree com as
mudanças não commitadas das Frentes B e C.
Método: leitura de código, leitura do fonte do whatsmeow `v0.0.0-20260630180629-b572e5bcb92b`
em `$GOMODCACHE`, leitura de `~/.whatsapp-mcp/logs/daemon.log` (27.590 linhas, 2026-07-09 →
2026-08-01) e consultas read-only em `~/.whatsapp-mcp/store/messages.db`.
`go build ./...` e `go test ./...` passam limpos no working tree atual.

Regra deste documento: **sem prova = suspeita, não causa.**

---

## 1. Sintoma (medido, não relatado)

Banco de produção `~/.whatsapp-mcp/store/messages.db`:

```
sqlite> select typeof(timestamp), timestamp from messages order by rowid desc limit 1;
text|2026-07-31 14:43:52 -0300 -03
sqlite> select count(*) from messages;   -- 9223
sqlite> select count(*) from chats;      -- 215
```

A mensagem mais recente do banco inteiro é de **2026-07-31 14:43:52**. O daemon continuou vivo,
reconectando e rodando resync até **2026-08-01 07:46** — ~17 h sem gravar uma única mensagem.

Do lado do servidor, a fila offline **cresce monotonicamente** e nunca é confirmada
(`daemon.log`, todas as linhas reais):

```
26709 08:15:25 Offline sync preview: 127 total (76 messages, ...)
26983 15:10:05 Offline sync preview: 430 total (147 messages, ...)
27086 18:11:21 Offline sync preview: 715 total (317 messages, ...)
27581 07:46:23 Offline sync preview: 787 total (374 messages, 61 notifications, 344 receipts)
```

Agregado da janela 2026-07-31 08:15 → 2026-08-01 07:46 (23,5 h, `awk` sobre o log):

| métrica | valor |
|---|---|
| reconexões (offline sync preview) | 22 |
| **`Offline sync completed`** | **0** |
| `Auto history resync (connected)` | 22 |
| `events.HistorySync` recebidos | 42 |
| mensagens gravadas por history sync | 76 (todas backfill antigo) |
| stream errors | 12 |
| mensagens novas gravadas | **0 depois de 14:43:52** |

Ou seja: o daemon reconecta a cada ~50 min, vê 300–374 mensagens pendentes anunciadas pelo
servidor, dispara o resync que o `077634c` adicionou, e **nada entra no banco**.

---

## 2. Causas CONFIRMADAS

### C1 — O resync pede a direção ERRADA do histórico (é a causa central)

`internal/wa/client.go:511` pega a **última** mensagem conhecida do chat:

```go
lastMsg, err := c.st.GetLastMessageForChat(chat.JID)
```

e `internal/store/queries.go:347-351` retorna a **mais nova**:

```sql
... WHERE messages.chat_jid = ? ORDER BY messages.timestamp DESC LIMIT 1
```

Esse `*store.Message` vira o âncora em `historySyncAnchor` (`internal/wa/client.go:586-614`) e vai
para `c.wm.BuildHistorySyncRequest(anchor, historySyncCount)` (`internal/wa/client.go:525`).

A doc do whatsmeow é explícita
(`go.mau.fi/whatsmeow@…/send.go:558-563`):

```go
// BuildHistorySyncRequest builds a message to request additional history from the user's primary device.
// The response will come as an *events.HistorySync with type `ON_DEMAND`.
// The response will contain to `count` messages immediately before the given message.
```

**"immediately before"**. O campo do protobuf chama-se `OldestMsgID` / `OldestMsgTimestampMS`
(`send.go:573-577`): o parâmetro é a mensagem mais ANTIGA que você já tem, e o servidor devolve o
que veio ANTES dela. Estamos passando a mais NOVA — logo pedimos 50 mensagens que já estão no
banco, e as mensagens perdidas (que são POSTERIORES ao âncora) nunca são pedidas.

Prova em produção: os âncoras não mudam entre reconexões porque nada novo entra, e as respostas
vêm vazias.

```
27436 19:40:50 Requested on-demand history sync for 555599950117@s.whatsapp.net (last msg: 3B4AB4B5B09C0F164B74)
27456 20:30:51 Requested on-demand history sync for 555599950117@s.whatsapp.net (last msg: 3B4AB4B5B09C0F164B74)
27542 22:23:53 Requested on-demand history sync for 555599950117@s.whatsapp.net (last msg: 3B4AB4B5B09C0F164B74)
27585 07:46:28 Requested on-demand history sync for 555599950117@s.whatsapp.net (last msg: 3B4AB4B5B09C0F164B74)
```

E o histograma das respostas no log inteiro:

```
337x  History sync complete. Stored 0 messages.
 84x  History sync complete. Stored 19 messages.   (backfill antigo, repetido)
```

**Conclusão dura:** `HISTORY_SYNC_ON_DEMAND` é uma API de *backfill para trás*. Ela não é o
mecanismo para recuperar mensagens recebidas enquanto se estava offline — essas chegam pela fila
offline (`events.Message` no reconnect) ou pelo history sync `RECENT` do servidor. Tanto o
`077634c` quanto o hardening da Frente B melhoram a robustez de um mecanismo que,
estruturalmente, não resolve o sintoma relatado.

### C2 — A fila offline nunca completa; o servidor nunca recebe confirmação

`OfflineSyncCompleted` é despachado quando o servidor manda `<ib><offline count=N/></ib>`
(`whatsmeow/connectionevents.go:86-90`). Em 23,5 h e 22 reconexões esse nó **nunca chegou**
(`Offline sync completed` = 0 depois da linha 26708), enquanto o `offline_preview` chegou 22
vezes com contagem crescente.

Padrão idêntico em toda sessão (`daemon.log:26971-26983`):

```
14:32:49 [wa WARN]  Received stream end frame
14:32:49 [wa ERROR] Unknown stream error: <stream:error><ack class="status" id="3EB014A25D6D150867A05C" type="media"/></stream:error>
14:32:50 [wa INFO]  Successfully authenticated
14:32:51 [wa INFO]  Offline sync preview: 284 total (101 messages, 38 notifications, 136 receipts)
14:32:56 [wa INFO]  Auto history resync (connected)
14:32:56..57         8x Requested on-demand history sync ...
15:10:04 [wa/Socket ERROR] Error reading from websocket: ... EOF     <-- fim da sessão, nada entregue
```

Entre `Successfully authenticated` e o próximo EOF (~40 min) **zero** `events.Message`. O
`stream:error` com `<ack class="status" type="media">` aparece 146 vezes no log e derruba o socket
a cada ~50 min. Isso é servidor/whatsmeow, não nosso código — mas é o que impede o drain, e hoje
nosso `New()` (`internal/wa/client.go:176-208`) **não trata `events.StreamError` nem
`events.KeepAliveTimeout`**, então nada disso aparece no `Status` nem dispara ação.

### C3 — `handleHistorySync` não desembrulha as mensagens; o live path desembrulha

`internal/wa/handlers.go:338-352` lê o conteúdo direto de `msg.Message.Message` (o
`waE2E.Message` cru dentro do `WebMessageInfo`), sem nenhum unwrap.

O whatsmeow fornece exatamente para isso `Client.ParseWebMessage`
(`whatsmeow/client.go:940-1002`), que chama `evt.UnwrapRaw()`
(`whatsmeow/types/events/events.go:367-410`) e desembrulha `DeviceSentMessage`,
`EphemeralMessage`, `ViewOnceMessage`/`V2`/`V2Extension`, `LottieStickerMessage`,
`DocumentWithCaptionMessage`, `EditedMessage`, `BotInvokeMessage`. O caminho ao vivo recebe isso
de graça (`whatsmeow/message.go:1106-1107`: `dispatchEvent(evt.UnwrapRaw())`).

Consequência: no history sync, qualquer mensagem embrulhada cai em `content == "" &&
mediaType == ""` e é descartada em silêncio (`internal/wa/handlers.go:356-358`). **Chats com
mensagens temporárias (ephemeral) — padrão em muitos grupos — perdem 100% do histórico
sincronizado.** Além disso o `sender` é remontado à mão (`handlers.go:360-375`) em vez de usar o
resolvedor do whatsmeow.

### C4 — Tipos de mídia não cobertos são descartados sem log, nos DOIS caminhos

`extractMediaInfo` (`internal/wa/handlers.go:36-77`) só reconhece `ImageMessage`, `VideoMessage`,
`AudioMessage` e `DocumentMessage`. Ficam de fora, entre outros: `StickerMessage`, `PtvMessage`
(vídeo-recado), `LocationMessage`/`LiveLocationMessage`, `ContactMessage`, `PollCreationMessage`,
`ReactionMessage`, respostas de botão/lista.

Como `content` também é "" para esses, o descarte acontece em:

- `internal/wa/handlers.go:98-100` (live) — `return`, sem log;
- `internal/wa/handlers.go:356-358` (history) — `continue`, sem log.

Isso é perda de mensagem silenciosa e **é invisível na observabilidade atual**.

### C5 — O chat sobe no ranking mesmo quando a mensagem é descartada → chat fica sem âncora → resync o pula para sempre

Em `handleMessage`, `StoreChat` roda na linha `internal/wa/handlers.go:91`, **antes** do descarte
da linha 98. Resultado: o chat ganha `last_message_time` novo sem nenhuma linha em `messages`.

Prova no banco de produção:

```
sqlite> select count(*) from chats c where not exists(select 1 from messages m where m.chat_jid=c.jid);
90                                   -- 90 de 215 chats (42%) têm ZERO mensagens

top-10 por last_message_time (n = mensagens armazenadas):
2026-07-31 10:22:08 | 551140039528@s.whatsapp.net       | 0   <-- falou hoje, nada gravado
2026-07-30 21:05:10 | 120363407230729311@newsletter     | 0
2026-07-30 11:55:32 | 551150280224@s.whatsapp.net       | 0
```

Esses chats não têm âncora, então `GetLastMessageForChat` devolve `nil`, `historySyncAnchor`
retorna erro e o chat é pulado (`internal/wa/client.go:517-523`). No log isso confere: o top-10
tinha 10 chats, mas só **8** requests saíram (`daemon.log:27435-27442`) — os dois pulados são
justamente `551140039528` e `120363407230729311@newsletter`.

Pior: o pulo é logado com `c.logger.Debugf` (`internal/wa/client.go:521`) e o logger é criado em
nível `INFO` (`internal/wa/client.go:153`, `waLog.Stdout("wa", "INFO", false)`) — **a razão do
pulo nunca aparece em produção**.

O gap do item 3 da spec é real e se auto-perpetua: chat sem mensagem gravada → sem âncora → nunca
resincronizado → continua sem mensagem.

### C6 — O resync gasta metade das vagas com `status@broadcast` e `@newsletter`

Do top-10 real: `status@broadcast` (1903 linhas de status!) e 3 newsletters. No `077634c`
(`limit=10`) sobravam ~6 conversas de verdade. Nenhum filtro por tipo de chat existe em
`ListChatsForResync` (`internal/store/queries.go:244-270`) nem em `requestHistorySync`
(`internal/wa/client.go:497-540`).

### C7 — Idempotência: OK, não é causa (verificado)

- PK composta existe: `internal/store/store.go:71` → `PRIMARY KEY (id, chat_jid)`.
- Escrita é `INSERT OR REPLACE` (`internal/store/store.go:152`), então reinserção **não duplica**.
- Testei o caso perigoso (REPLACE apaga a linha pai e existe filho em `transcriptions` com FK
  composta e `foreign_keys=ON`): reproduzi o schema no sqlite3 3.50.6 — a reinserção passa sem
  erro e a transcrição sobrevive. Sem regressão aqui.
- Os triggers FTS (`store.go:115-124`) tratam delete+insert, então o índice acompanha.

Ressalva menor (não é a causa do sintoma): `INSERT OR REPLACE` sobrescreve **todas** as colunas.
Uma reinserção vinda do history sync com metadados de mídia vazios apaga o que
`StoreMediaInfo` (`internal/store/store.go:160-165`) tenha preenchido.

### C8 — Observabilidade insuficiente para provar o drain (HEAD) — parcialmente resolvido no working tree

No `077634c` só existem 3 linhas de log úteis: `Auto history resync (%s)`,
`Requested on-demand history sync for %s (last msg: %s)` e `History sync complete. Stored N`.
Não há: log de `Disconnected`, contagem de chats selecionados vs. pulados, nem quantas mensagens
novas o resync trouxe. Não é possível distinguir "resync rodou e não veio nada" de "resync nem
selecionou o chat".

O working tree da Frente B fecha boa parte disso (ver §4).

---

## 3. Suspeitas NÃO confirmadas

- **S1 — O burst de 8 `SendPeerMessage` aborta a entrega da fila offline.** Em toda sessão, o
  padrão é: autentica → preview → 5 s depois disparamos 8 peer messages → nenhum `events.Message`
  até o fim da sessão. A correlação é perfeita em 22/22 sessões, mas correlação não é causa; o
  servidor pode simplesmente não estar entregando por outro motivo. **Teste decisivo:** desligar o
  auto-resync por um ciclo (ou `resyncInitialDelay` bem maior) e ver se `Offline sync completed`
  volta a aparecer.
- **S2 — Mensagens estão sendo entregues e descartadas em silêncio** (C4/C3) em vez de não serem
  entregues. Contra: a contagem do `offline_preview` cresce, o que sugere falta de ack, não
  descarte. A favor: o descarte não deixa rastro nenhum, então o log não pode refutar. Só um
  contador de "recebidas vs. persistidas vs. descartadas por tipo" resolve.
- **S3 — O `stream:error` `<ack class="status" type="media">` é reação a algo que o daemon manda.**
  146 ocorrências, sempre com id de mensagem de status. Não consegui amarrar a um envio nosso.
- **S4 — Formato do timestamp.** Gravamos o `time.Time` do Go como texto
  `2026-07-31 14:43:52 -0300 -03` (verificado: `typeof=text`, e todas as 9223 linhas com o mesmo
  sufixo `-0300 -03`). `date()`/`datetime()` do SQLite não parseiam isso, e a ordenação é
  lexicográfica: se aparecer outro offset (viagem/DST), `ORDER BY timestamp DESC` escolhe o
  "último" errado e o âncora sai errado. Hoje não há mistura, então não é causa ativa.
- **S5 — Chamada de rede síncrona dentro do event handler.** `chatName` chama
  `c.wm.GetGroupInfo(context.Background(), …)` (`internal/wa/handlers.go:275`) de dentro de
  `handleMessage`; o whatsmeow processa nós em fila única (`whatsmeow/client.go:861-895`), então
  um IQ lento (timeout default 75 s, `whatsmeow/request.go:150-155`) trava todo o pipeline.
  **Refutado como causa atual:** o whatsmeow avisa quando um handler passa de 5 s
  (`client.go:872-876`) e há **0** ocorrências de `Node handling took` / `Handler queue is full`
  no log inteiro. Continua sendo risco latente, não é o que está acontecendo.

---

## 4. O que o working tree atual já resolve

Frente B (`internal/wa/client.go`, `internal/store/queries.go`, +testes):

| Item | Status | Onde |
|---|---|---|
| Nil-safety do âncora (last nil, ID vazio, timestamp zero, JID incompleto) | resolvido | `client.go:586-614` |
| `req == nil` antes de `SendPeerMessage` | resolvido | `client.go:526-532` |
| `recover()` nas goroutines de resync | resolvido | `client.go:360-366`, `426`, `471-476` |
| Cobertura de chats: união top-25 + tudo ativo em 48 h, cap 60 | resolvido | `client.go:113-122`, `queries.go:244-270` |
| Força resync após outage > 10 min | resolvido | `client.go:196-201`, `336-354` |
| Backoff se caiu de novo ao acordar | resolvido | `client.go:430-444` |
| Ticker periódico de 15 min | resolvido | `client.go:402-418` |
| Métrica de drain (`CountMessages` antes/depois) + `Status.LastResync*` | resolvido | `client.go:555-571`, `queries.go:276-282` |
| Shutdown limpo das goroutines (`stopCh`) | resolvido | `client.go:246-255`, `370-382` |

Frente C: `internal/stream` + `GET /api/events` (SSE) + `watch.sh` — dá visibilidade em tempo
real do que **foi persistido**, mas por construção não mostra o que foi descartado.

**Sobre a pergunta 2 da spec (panic com nil):** no HEAD `077634c` o caminho era
`client.go:270-296` — `lastMsg == nil` já tinha `continue`, e `BuildHistorySyncRequest` só
desreferencia `lastKnownMessageInfo` (`send.go:572-577`), que nunca era nil ali. O panic mais
plausível no HEAD era `c.wm.Store.ID` nil (não pareado) fora desse trecho, ou `SendPeerMessage`
sem sessão. Não achei prova de panic: **`grep -c panic daemon.log` = 0** nas 27.590 linhas. Ou
seja, o relato de panic não se reproduz no log desta máquina — trate como não confirmado. De todo
modo o working tree já blindou o caminho inteiro.

### O que AINDA falta (nada disso está no working tree)

1. Direção do âncora (C1) — o resync continua pedindo para trás.
2. `handleHistorySync` sem unwrap (C3) e sem `ParseWebMessage`.
3. Tipos de mídia não cobertos + descarte silencioso (C4).
4. `StoreChat` antes do descarte, criando chats sem âncora (C5).
5. Filtro de `status@broadcast`/`@newsletter` no resync (C6).
6. `events.StreamError`, `events.KeepAliveTimeout`, `events.UndecryptableMessage` não tratados.
7. Motivo do skip por chat só em `Debugf`, invisível em nível INFO (C8).

---

## 5. Correções recomendadas, por prioridade

### P0 — Parar de tratar `HISTORY_SYNC_ON_DEMAND` como recuperador de mensagens recentes
**Arquivo:** `internal/wa/client.go` (`requestHistorySync`, `historySyncAnchor`).
Por quê: C1. A API devolve o que vem **antes** do âncora (`whatsmeow/send.go:558-563`), então o
resync atual é estruturalmente incapaz de trazer as mensagens da tarde perdida — e o log de 23,5 h
comprova (337 respostas com 0 mensagens).
O que fazer: (a) mudar a semântica do âncora para a **mensagem mais antiga** de um intervalo que
se quer preencher (exige uma query nova em `queries.go`, tipo `GetOldestMessageSince`), aceitando
que isso serve para *backfill*, e (b) parar de contar com ele para o drain — o drain depende de
P1. Se a intenção for mesmo só backfill, deixar isso explícito no comentário e no log
(`resync backfill`, não `drain`).

### P0 — Diagnosticar e tratar a queda de stream que impede a fila offline de completar
**Arquivos:** `internal/wa/client.go` (switch de `New()`), possivelmente config do whatsmeow.
Por quê: C2 — 0 `OfflineSyncCompleted` em 22 reconexões, fila crescendo de 76 → 374 mensagens.
Enquanto isso não fechar, nenhuma mensagem nova entra, com ou sem resync.
O que fazer: tratar `events.StreamError` (logar o nó cru e refletir no `Status`),
`events.KeepAliveTimeout`/`KeepAliveRestored`, `events.ConnectFailure`, e logar `Disconnected` com
o motivo. Depois, rodar um ciclo com o auto-resync desligado para testar S1 (peer-message burst
abortando a entrega) — é o experimento mais barato com maior chance de fechar o caso.

### P1 — Usar `ParseWebMessage` em `handleHistorySync`
**Arquivo:** `internal/wa/handlers.go` (`handleHistorySync`, ~296-423).
Por quê: C3. Hoje ephemeral/view-once/document-with-caption/edited somem no history sync, e o
`sender` sai em formato diferente do caminho ao vivo (`handlers.go:366-375` grava o JID completo;
`handleMessage:85-87` grava só o número). Trocar o parsing manual por
`c.wm.ParseWebMessage(jid, msg.Message)` alinha os dois caminhos e elimina uma classe inteira de
perda.

### P1 — Nunca descartar em silêncio
**Arquivo:** `internal/wa/handlers.go` (`extractMediaInfo:36-77`, `handleMessage:98-100`,
`handleHistorySync:356-358`).
Por quê: C4 + S2. Sem um contador de descarte não dá para saber se o problema é "não chegou" ou
"chegou e jogamos fora". Mínimo: `logger.Warnf` com o nome do campo de `waE2E.Message` que veio
preenchido, e um contador exposto no `Status`. Ideal: suportar sticker, PTV, location, contact,
poll e reaction (mesmo que só com um `content` sintético) para que a linha exista.

### P1 — Não criar chat sem mensagem / não pular chat sem âncora em silêncio
**Arquivos:** `internal/wa/handlers.go:91` (mover `StoreChat` para depois do descarte, ou marcar o
chat como "sem conteúdo") e `internal/wa/client.go:517-523` (`Debugf` → `Warnf`, ou contador
agregado no log de INFO).
Por quê: C5 — 90 de 215 chats estão nesse estado, incluindo chats que conversaram hoje; eles são
invisíveis para o resync e o log não diz por quê.

### P2 — Filtrar `status@broadcast` e `@newsletter` da seleção de resync
**Arquivo:** `internal/store/queries.go` (`ListChatsForResync:244-270`) ou
`internal/wa/client.go:510`.
Por quê: C6 — hoje `status@broadcast` (1903 linhas) e 3 newsletters ocupam vagas de conversas
reais e geram peer messages inúteis a cada reconexão.

### P2 — Tratar `events.UndecryptableMessage`
**Arquivo:** `internal/wa/client.go` (switch de `New()`).
Por quê: mensagem que falha decriptação hoje é perda definitiva e silenciosa. O whatsmeow expõe
`BuildUnavailableMessageRequest` (`whatsmeow/send.go:544-556`) para pedir o reenvio ao telefone;
a resposta volta como `events.Message` com `UnavailableRequestID` (`send.go:543`).

### P3 — Guardar timestamp em formato ordenável/parseável
**Arquivos:** `internal/store/store.go`, `internal/store/time.go`.
Por quê: S4. `2026-07-31 14:43:52 -0300 -03` não é parseável por `date()`/`datetime()` e a
ordenação depende de todos os registros terem o mesmo offset. Migrar para UTC RFC3339 (ou epoch)
elimina um modo de falha do âncora e destrava consultas analíticas.

### P3 — Tirar a chamada de rede de dentro do event handler
**Arquivo:** `internal/wa/handlers.go:275` (`GetGroupInfo` dentro de `chatName`).
Por quê: S5 — hoje não está travando (0 avisos `Node handling took` no log), mas a fila de nós do
whatsmeow é única (`whatsmeow/client.go:861-895`) e o IQ tem 75 s de timeout
(`whatsmeow/request.go:150`). Resolver o nome do grupo de forma assíncrona remove o risco.

---

## 6. Como provar que a correção funcionou

Sinais objetivos, em ordem de força:

1. `Offline sync completed: N events processed` volta a aparecer no `daemon.log` a cada reconexão
   (hoje: 0 em 22).
2. `Offline sync preview` para de crescer e cai perto de zero (hoje: 76 → 374 em 23,5 h).
3. `select max(timestamp) from messages` acompanha o relógio (hoje: parado em 2026-07-31 14:43:52).
4. `select count(*) from chats c where not exists(select 1 from messages m where m.chat_jid=c.jid)`
   para de crescer (hoje: 90/215).
5. `Status.LastResyncNewMessages` (já implementado pela Frente B) > 0 depois de uma janela offline
   real.
