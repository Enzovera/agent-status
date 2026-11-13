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

Plugin jest dla OpenCode 2.x (`@opencode/plugin`, API `Plugin.define` / `ctx.event.subscribe`; nazwy i pola zdarzeń sprawdzone na typach z wersji 2.3.0). Subskrybuje tylko potrzebne typy, więc strumieniowane fragmenty odpowiedzi nie wychodzą z OpenCode. Wysyła:

- tytuł sesji, model i katalog,
- treść poleceń (`session.inbox.enqueued`) i ostatni tekst odpowiedzi (`session.text.ended`), oba przycięte do 4000 znaków,
- wywołania narzędzi z nazwą i krótkim opisem: z argumentów idą tylko `description`, ścieżka, `pattern`, `url`, `command`, `query`, nigdy treść plików ani łatek,
- prośby o zgodę, pytania agenta (`form.*`), błędy i przerwania.

OpenCode 1.x ma inny format pluginów i ten plugin tam nie zadziała.

## Stany

| Stan | Claude Code | OpenCode |
|---|---|---|
| pracuje | `UserPromptSubmit`, `PreToolUse` | `session.execution.started`, `session.tool.called`, `session.status` busy/retry, `permission.replied`, `form.replied`/`cancelled` |
| czeka na Ciebie | `PermissionRequest`, `Notification` (permission_prompt, agent_needs_input, elicitation) | `permission.asked`, `form.created` |
| gotowy | `SessionStart`, `Stop`, `Notification` idle_prompt | `session.created`, `session.execution.succeeded`/`interrupted`, `session.idle`, `session.status` idle |
| błąd | `StopFailure` | `session.execution.failed` |
| zakończona | `SessionEnd` | `session.deleted` |

Bez zmiany stanu: `session.renamed` (tytuł), `session.model.selected` (model), `session.inbox.enqueued` (polecenie), `session.text.ended` (odpowiedź), `session.tool.failed` (opis). Gdy sesja przechodzi w „gotowy” bez własnego opisu, wiersz pokazuje początek ostatniej odpowiedzi.

„Brak sygnału”: sesja „pracuje”, ale od 10 min nie przyszło żadne zdarzenie. Zakończone znikają po 2 h, ciche po 24 h.
