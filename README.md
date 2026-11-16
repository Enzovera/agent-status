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

Na Linux / macOS najpierw `chmod +x agent-status-*`. Opcja `-log plik.log` zapisuje log do pliku zamiast na ekran. Stan sesji jest zapisywany w pliku `-state`, więc przetrwa restart serwera. Obok niego serwer trzyma:

- `archive/events-RRRR-MM.jsonl` — archiwum wszystkich zdarzeń (jedno w wierszu, bez limitu czasu), z którego liczone są statystyki. Inny katalog: `-archive KATALOG`, wyłączenie: `-archive ""`. Polecenia są w nim przycięte do 160 znaków, odpowiedzi agenta się tam nie trafiają.
- `machines.json` — ostatni heartbeat każdej maszyny.

Strona: `http://<IP serwera>:7777/`, statystyki: `http://<IP serwera>:7777/stats`. Serwer powinien mieć stały adres IP w LAN (rezerwacja DHCP w routerze).

API:

| | |
|---|---|
| `GET /api/sessions` | stan sesji i maszyn (to, co pokazuje strona) |
| `GET /api/stats` | statystyki jako JSON |
| `GET /api/report` | te same statystyki jako raport Markdown pisany dla agenta AI, który ma pomóc usprawnić pracę z agentami: definicje miar, liczby, fakty do analizy (np. najczęstsze prośby o zgodę jako kandydaci do allowlisty) |
| `GET /api/events` | surowe zdarzenia z archiwum (JSON lines); filtry `host=`, `project=`, `key=` |

Okres dla trzech ostatnich: `?days=7` (domyślnie) albo `?from=2026-11-01&to=2026-11-15` (data lub RFC3339). Żeby agent przeanalizował Twoją pracę, daj mu np. `http://<IP serwera>:7777/api/report?days=14`.

Nazwę maszyny można zmienić na stronie („zmień nazwę” w nagłówku maszyny albo w szczegółach sesji). Zmiana dotyczy tylko tego, co pokazuje strona, obowiązuje dla wszystkich oglądających i jest zapisywana w pliku `-state`; pusta nazwa przywraca tę, którą wysyła agent.

### Strona na tablecie lub drugim ekranie

Przyciski w nagłówku są pomyślane pod ekran, który stoi obok i na który tylko zerkasz:

- **dźwięk** — krótki sygnał, gdy sesja zaczyna czekać na Ciebie, ma błąd, traci sygnał albo kończy pracę. Gra też wtedy, gdy strona jest na wierzchu (powiadomienia w takiej sytuacji się nie pokazują). Po przeładowaniu strony przeglądarka wstrzymuje dźwięk do pierwszego dotknięcia — przycisk pokazuje wtedy „dźwięk: dotknij”.
- **nie wygaszaj** — ekran nie gaśnie, dopóki strona jest widoczna.
- **pełny ekran** — bez pasków przeglądarki.

Gdy coś czeka na Ciebie, cały ekran dostaje czerwoną ramkę, a nad listą pojawia się duży pasek z liczbą sesji i tym, jak długo czeka najdłużej (pomarańczowy, gdy sesja ma błąd albo straciła sygnał). Dotknięcie paska przełącza na widok „kolejka”, gdzie czekające sesje są ułożone od najdłużej czekającej. Wiersz sesji pokazuje, od kiedy czeka, ma błąd albo jest gotowa („od 7 min”); pracującej — kiedy ostatnio się odezwała. Na ekranie dotykowym przyciski są większe.

Nad listą jest też pasek maszyn: zielona kropka — maszyna wysyła heartbeat, pomarańczowe kółko — „offline od …” (brak heartbeatu od 3 min). To samo widać w nagłówku maszyny.

Powiadomienia i „nie wygaszaj” przeglądarka pozwala włączyć tylko na `localhost` albo przez HTTPS. Na urządzeniu, które otwiera stronę po adresie w LAN (`http://192.168.1.10:7777`), otwórz `edge://flags/#unsafely-treat-insecure-origin-as-secure` (w Chrome `chrome://flags/...`), wpisz adres serwera, ustaw *Enabled* i uruchom przeglądarkę ponownie. Dźwięk, ramka i pełny ekran działają bez tego.

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
| `-SkipHeartbeat` | `--skip-heartbeat` | nie ruszaj heartbeatu (maszyna nie będzie pokazywana jako online/offline) |
| `-Uninstall` | `--uninstall` | usuń wpisy agent-status (hooki, plugin, heartbeat) |

Ponowne uruchomienie instalatora podmienia poprzednie wpisy (np. po zmianie adresu serwera albo gdy doszły nowe hooki). Instalator dotyka tylko hooków kierujących na `/hook/claude`, pliku `agent-status.ts` i zadania heartbeatu.

**Heartbeat.** Instalator ustawia też lekkie zadanie, które co minutę (i zaraz po zalogowaniu / starcie) wysyła `POST /hook/heartbeat?host=NAZWA` — dzięki temu strona pokazuje, czy maszyna jest włączona (offline po 3 minutach ciszy). Heartbeat dotyczy maszyny, nie agenta, więc instaluje się także z `-SkipClaude -SkipOpenCode`. Na Windows to zadanie `agent-status-heartbeat` w Harmonogramie zadań bieżącego użytkownika (ukryte `curl.exe`, bez uprawnień administratora); na macOS LaunchAgent `~/Library/LaunchAgents/com.agent-status.heartbeat.plist`; na Linuksie wpis w `crontab` użytkownika oznaczony `# agent-status-heartbeat`, a gdy `crontab` nie ma — timer `systemctl --user` `agent-status-heartbeat.timer` (bez `loginctl enable-linger` działa tylko, gdy jesteś zalogowany).

### Instalacja ręczna

**Claude Code:** dopisz zawartość [`clients/claude/hooks.json`](clients/claude/hooks.json) do `~/.claude/settings.json` (klucz `hooks`; jeśli już istnieje, połącz listy). Podmień `SERVER` na IP serwera, a `HOST` na nazwę maszyny, którą chcesz widzieć na stronie (bez `?host=` serwer pokaże IP).

Hooki są typu `http` z limitem 2 s; gdy serwer nie działa, Claude Code pokazuje nieblokujący „hook error” i pracuje dalej. Gdy maszyna serwera jest wyłączona (a nie tylko serwer), każde wywołanie narzędzia może czekać do 2 s na `PreToolUse` i drugie tyle na `PostToolUse`. `SessionStart` nie jest używany: Claude Code nie przyjmuje dla niego hooków `http`, więc sesja pojawia się na stronie przy pierwszym poleceniu.

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
| gotowy | `Stop`, `Notification` idle_prompt | `session.created`, `session.execution.succeeded`/`interrupted`, `session.idle`, `session.status` idle |
| błąd | `StopFailure` | `session.execution.failed` |
| zakończona | `SessionEnd` | `session.deleted` |

Claude Code nie zgłasza samej zgody ani odpowiedzi na pytanie, więc sesja czekająca na zgodę albo odpowiedź wraca do „pracuje”, gdy to samo narzędzie się skończy (`PostToolUse`/`PostToolUseFailure`) — albo wcześniej, przy kolejnym narzędziu. Narzędzia podagentów działających w tle tego nie robią.

Bez zmiany stanu w Claude Code, tylko opis i liczniki: `PostToolUse` (czas narzędzia, do statystyk), `PostToolUseFailure` (błąd narzędzia), `PermissionDenied` (odmowa w trybie auto), `SubagentStart`/`SubagentStop` (działające podagenty), `TaskCreated`/`TaskCompleted` (postęp listy zadań, np. „zadania 3/5”), `PreCompact`/`PostCompact` (kompaktowanie kontekstu), `PostModelSwitch` (model). Opis sesji czekającej na Ciebie nie jest przez nie nadpisywany.

Bez zmiany stanu w OpenCode: `session.renamed` (tytuł), `session.model.selected` (model), `session.inbox.enqueued` (polecenie), `session.text.ended` (odpowiedź), `session.tool.failed` (opis). Gdy sesja przechodzi w „gotowy” bez własnego opisu, wiersz pokazuje początek ostatniej odpowiedzi.

„Brak sygnału”: sesja „pracuje”, ale od 10 min nie przyszło żadne zdarzenie. Zakończone znikają po 2 h, ciche po 24 h.

## Budowanie ze źródeł

Wymaga Go 1.23+:

```
go build -o bin/agent-status.exe ./cmd/agent-status
```

Nowe wydanie: `git tag v0.2.0 && git push origin v0.2.0` — workflow `.github/workflows/release.yml` zbuduje serwer na wszystkie platformy i opublikuje wydanie z plikami.

## Licencja

MIT — zob. [LICENSE](LICENSE).
