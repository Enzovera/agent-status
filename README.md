# agent-status

Podgląd statusów sesji Claude Code i OpenCode z wielu maszyn w sieci lokalnej. Tylko do odczytu: agenci wysyłają zdarzenia, serwer pokazuje stronę ze stanem każdej sesji (pracuje / czeka na Ciebie / błąd / gotowy). Niczego nie przejmuje i nie blokuje sesji.

## Serwer

Na jednej, stale włączonej maszynie ze stałym IP w LAN:

```
go build -o bin/agent-status.exe ./cmd/agent-status
bin/agent-status.exe -addr :7777 -state agent-status.json
```

Strona: `http://<IP serwera>:7777/`. API: `GET /api/sessions`. Port 7777 trzeba wpuścić w zaporze tylko z sieci lokalnej (profil Prywatna).

## Claude Code (każda maszyna)

Dopisz zawartość `clients/claude/hooks.json` do `~/.claude/settings.json` (klucz `hooks`; jeśli już istnieje, połącz listy). Podmień:

- `SERVER` na IP serwera,
- `HOST` na nazwę maszyny, którą chcesz widzieć na stronie (bez `?host=` serwer pokaże IP).

Hooki są typu `http` z limitem 2 s; gdy serwer nie działa, Claude Code pokazuje nieblokujący „hook error” i pracuje dalej.

## OpenCode (każda maszyna)

Skopiuj `clients/opencode/agent-status.ts` do `~/.config/opencode/plugins/` i podmień `SERVER` (albo ustaw `AGENT_STATUS_URL`). Nazwa maszyny to `hostname()`, nadpisywana przez `AGENT_STATUS_HOST`.

Plugin używa API `Plugin.define` / `ctx.event.subscribe`. Starsze wersje OpenCode mają inny format pluginów; nazwy zdarzeń (`session.status`, `session.idle`, `permission.*`) nie są w pełni udokumentowane, więc nierozpoznane typy tylko odświeżają „ostatnio” sesji, a ostatni typ zdarzenia widać w podpowiedzi wiersza.

## Stany

| Stan | Claude Code | OpenCode |
|---|---|---|
| pracuje | `UserPromptSubmit`, `PreToolUse` | `session.status` busy/retry, `permission.replied` |
| czeka na Ciebie | `PermissionRequest`, `Notification` (permission_prompt, agent_needs_input, elicitation) | `permission.*` |
| gotowy | `SessionStart`, `Stop`, `Notification` idle_prompt | `session.created`, `session.idle`, `session.status` idle |
| błąd | `StopFailure` | `session.error` |
| zakończona | `SessionEnd` | `session.deleted` |

„Brak sygnału”: sesja „pracuje”, ale od 10 min nie przyszło żadne zdarzenie. Zakończone znikają po 2 h, ciche po 24 h.
