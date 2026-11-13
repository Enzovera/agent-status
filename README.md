# agent-status

Podgląd statusów sesji Claude Code i OpenCode z wielu maszyn w sieci lokalnej. Tylko do odczytu: agenci wysyłają zdarzenia, serwer pokazuje stronę ze stanem każdej sesji (pracuje / czeka na Ciebie / błąd / gotowy). Niczego nie przejmuje i nie blokuje sesji.

Dwie części:

- **serwer** — jeden program na jednej, stale włączonej maszynie w LAN; zbiera zdarzenia i serwuje stronę,
- **klienci** — każda maszyna, na której pracuje Claude Code lub OpenCode; nic nie instalujesz poza wpisem w konfiguracji (hooki HTTP w Claude Code, mały plugin w OpenCode).

## 1. Serwer

Pobierz plik dla swojego systemu z [najnowszego wydania](https://github.com/Enzovera/agent-status/releases/latest) (`agent-status-windows-amd64.exe`, `agent-status-linux-amd64`, `agent-status-darwin-arm64` itd.; `arm64` dla procesorów ARM, w tym Mac z Apple Silicon) i uruchom:

```
agent-status-windows-amd64.exe -addr :7777 -state agent-status.json
```

Na Linux / macOS najpierw `chmod +x agent-status-*`. Opcja `-log plik.log` zapisuje log do pliku zamiast na ekran. Stan sesji jest zapisywany w pliku `-state`, więc przetrwa restart serwera.

Strona: `http://<IP serwera>:7777/`. API: `GET /api/sessions`. Serwer powinien mieć stały adres IP w LAN (rezerwacja DHCP w routerze).

Nazwę maszyny można zmienić na stronie („zmień nazwę” w nagłówku maszyny albo w szczegółach sesji). Zmiana dotyczy tylko tego, co pokazuje strona, obowiązuje dla wszystkich oglądających i jest zapisywana w pliku `-state`; pusta nazwa przywraca tę, którą wysyła agent.

Port 7777 trzeba wpuścić w zaporze, najlepiej tylko z sieci prywatnej. Na Windows (PowerShell jako administrator):

```powershell
New-NetFirewallRule -DisplayName "agent-status" -Direction Inbound -Protocol TCP -LocalPort 7777 -Profile Private -Action Allow
```

Serwer nie ma logowania ani szyfrowania — nie wystawiaj go do internetu.

## 2. Klienci (urządzenia, które raportują)

Na każdej maszynie z Claude Code lub OpenCode uruchom instalator, podając adres serwera. Instalator sam wykrywa, co jest zainstalowane, dopisuje hooki do `~/.claude/settings.json` (zostawia Twoje inne ustawienia i hooki, robi kopię `settings.json.bak`) i/lub zapisuje plugin w `~/.config/opencode/plugins/`. Na końcu sprawdza, czy serwer odpowiada.

**Windows** (PowerShell, bez uprawnień administratora):

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/Enzovera/agent-status/main/install/client.ps1))) -Server 192.168.1.10
```

**macOS / Linux** (wymaga `python3` albo `node`):

```sh
curl -fsSL https://raw.githubusercontent.com/Enzovera/agent-status/main/install/client.sh | sh -s -- --server 192.168.1.10
```

Zamiast `192.168.1.10` wpisz IP serwera (albo `host:port`, jeśli serwer nie działa na 7777). Po instalacji uruchom ponownie otwarte sesje Claude Code / OpenCode.

Opcje:

| Windows | macOS / Linux | Działanie |
|---|---|---|
| `-Server ADRES` | `--server ADRES` | adres serwera (wymagany) |
| `-Name NAZWA` | `--name NAZWA` | nazwa maszyny na stronie (domyślnie nazwa komputera) |
| `-SkipClaude` | `--skip-claude` | nie ruszaj Claude Code |
| `-SkipOpenCode` | `--skip-opencode` | nie ruszaj OpenCode |
| `-Uninstall` | `--uninstall` | usuń wpisy agent-status |

Ponowne uruchomienie instalatora podmienia poprzednie wpisy (np. po zmianie adresu serwera). Instalator dotyka tylko hooków kierujących na `/hook/claude` i pliku `agent-status.ts`.

### Instalacja ręczna

**Claude Code:** dopisz zawartość [`clients/claude/hooks.json`](clients/claude/hooks.json) do `~/.claude/settings.json` (klucz `hooks`; jeśli już istnieje, połącz listy). Podmień `SERVER` na IP serwera, a `HOST` na nazwę maszyny, którą chcesz widzieć na stronie (bez `?host=` serwer pokaże IP).

Hooki są typu `http` z limitem 2 s; gdy serwer nie działa, Claude Code pokazuje nieblokujący „hook error” i pracuje dalej.

**OpenCode:** skopiuj [`clients/opencode/agent-status.ts`](clients/opencode/agent-status.ts) do `~/.config/opencode/plugins/` i podmień `SERVER` (albo ustaw zmienną `AGENT_STATUS_URL`). Nazwa maszyny to `hostname()`, nadpisywana przez `AGENT_STATUS_HOST`.

## Co jest wysyłane

Claude Code wysyła pełne dane hooków (m.in. polecenia i argumenty narzędzi) — serwer zachowuje z nich tylko to, co pokazuje strona: katalog, polecenie, ostatnią odpowiedź, model, nazwę i krótki opis narzędzia. Dane trafiają tylko do Twojego serwera w LAN.

Plugin OpenCode jest dla OpenCode 2.x (`@opencode/plugin`, API `Plugin.define` / `ctx.event.subscribe`; nazwy i pola zdarzeń sprawdzone na typach z wersji 2.3.0). Subskrybuje tylko potrzebne typy, więc strumieniowane fragmenty odpowiedzi nie wychodzą z OpenCode. Wysyła:

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

## Budowanie ze źródeł

Wymaga Go 1.23+:

```
go build -o bin/agent-status.exe ./cmd/agent-status
```

Nowe wydanie: `git tag v0.2.0 && git push origin v0.2.0` — workflow `.github/workflows/release.yml` zbuduje serwer na wszystkie platformy i opublikuje wydanie z plikami.

## Licencja

MIT — zob. [LICENSE](LICENSE).
