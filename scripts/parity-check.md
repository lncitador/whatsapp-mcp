# Parity check — Go daemon vs Python MCP server

Rode cada item nos dois lados sobre o MESMO store e marque. Python:
`cd whatsapp-mcp-server && uv run python -c "import whatsapp; print(whatsapp.<fn>)"`.
Go: `curl -s -X POST localhost:8080/api/rpc/<tool> -d '<json>'`.

| # | Tool | Chamada | Go == Python (semântica)? |
|---|------|---------|---------------------------|
| 1 | search_contacts | query "carlos" | [ ] Go acha por nome (Python NÃO acha — melhoria esperada, validar JIDs retornados contra whatsmeow_contacts) |
| 2 | search_contacts | query por dígitos do telefone | [ ] mesmos JIDs |
| 3 | list_messages | chat_jid do chat mais ativo, limit 5 | [ ] mesmas mensagens, mesma ordem |
| 4 | list_messages | query "obrigado", include_context true | [ ] mesmos hits + contexto |
| 5 | list_chats | sem filtro, limit 10 | [ ] mesmos chats, mesma ordem |
| 6 | get_chat | jid de grupo conhecido | [ ] mesmos campos |
| 7 | get_direct_chat_by_contact | telefone conhecido | [ ] mesmo chat |
| 8 | get_contact_chats | jid de contato ativo | [ ] mesmos chats |
| 9 | get_last_interaction | mesmo jid | [ ] mesma mensagem |
| 10 | get_message_context | message_id do item 3 | [ ] mesmo contexto |
| 11 | send_message | enviar "teste go" para o próprio número | [ ] entregue |
| 12 | send_file | enviar uma imagem pequena | [ ] entregue |
| 13 | send_audio_message | enviar .mp3 (conversão ffmpeg) | [ ] entregue como voice note |
| 14 | download_media | baixar mídia do item 12 | [ ] arquivo salvo em ~/.whatsapp-mcp/media |
| 15 | auth_status | via tools/call no stdio | [ ] state=connected |

Divergência aceitável: timestamps com fuso formatado diferente; ordem estável
diferente apenas em empates exatos de timestamp. Qualquer outra divergência
bloqueia a Task 13.
