---
name: agent-flow-review
description: 'Analiza statystyk pracy z agentami z serwera agent-status (sesje Claude Code / OpenCode z wielu maszyn) i konkretne rekomendacje, jak usprawnić flow - gdzie użytkownik blokuje agentów, które zgody dodać do allowlisty (gotowe reguły permissions), błędy, kompaktowania, równoległość, modele. Użyj, gdy użytkownik prosi np. "przeanalizuj mój flow", "statystyki agentów", "usprawnij pracę z agentami", "agent-status report", "dlaczego agenci na mnie czekają", "jakie zgody dodać".'
argument-hint: "[dni | od do (RRRR-MM-DD)] [host/projekt]"
allowed-tools:
  - 'Bash(curl -s "http://SERVER:7777/api/*)'
  - 'PowerShell(curl.exe -s "http://SERVER:7777/api/*)'
---

# Przegląd flow pracy z agentami (agent-status)

Serwer: `http://SERVER:7777` (wpisany przez instalator; jeśli widzisz tu dosłownie `SERVER`, użyj zmiennej `AGENT_STATUS_URL` albo zapytaj o adres). Argumenty: `$ARGUMENTS`.

Odpowiadaj po polsku, zwięźle. Każda rekomendacja musi wynikać z liczb z raportu.

## 1. Pobierz dane

Tylko `curl` przez powłokę (nie WebFetch - zmienia http na https i nie dosięga LAN). Adres zawsze w cudzysłowie, dokładnie w tej postaci:

- Bash: `curl -s "http://SERVER:7777/api/report?days=14"`
- PowerShell: `curl.exe -s "http://SERVER:7777/api/report?days=14"` (samo `curl` to tam alias Invoke-WebRequest). Gdy polskie znaki są zniekształcone, dodaj `-o "$env:TEMP\agent-flow.md"` i przeczytaj plik.

Okres z argumentów: liczba → `days=N` (domyślnie 14, maks. 400); daty → `from=RRRR-MM-DD&to=RRRR-MM-DD` (data w `to` obejmuje cały dzień). Pozostałe słowa (host, projekt) to zawężenie analizy.

Pusta odpowiedź / błąd połączenia: napisz, że serwer `http://SERVER:7777` nie odpowiada (czy działa, czy ten komputer jest w tej samej sieci, zapora na porcie), i zakończ - nie zgaduj liczb.

Raport (Markdown) ma definicje miar, tabele i sekcję „Sygnały do analizy”. Szczegóły tylko gdy potrzebne:
- `/api/stats?days=N` - te same liczby w JSON (np. pełna lista `permissions.byPattern`),
- `/api/events?days=N&project=P` (też `host=`, `key=`) - surowe zdarzenia JSON lines (`kind`, `detail`, `tool`, `cwd`, `mode`, `project`, `host`). Duże: filtruj potokiem (`| grep '"PermissionRequest"' | head -50`), nigdy nie wypisuj całości.

Mało danych (np. < 2 dni, < 5 sesji, sekcja „Brak w danych”): powiedz to wprost i oznacz wnioski jako słabe.

## 2. Analiza (kolejność wg czasu do odzyskania)

1. **Użytkownik jako wąskie gardło**: waitShare, czas reakcji według przyczyny (suma i p90 ważniejsze niż mediana), godziny/dni z najwyższym „≥1 czeka” i najdłuższą reakcją, „czas do kolejnego polecenia”. Przyczyny → działania: `permission` → allowlista / tryb; `question` i `decision` → pełniejsze polecenia na start, instrukcja w CLAUDE.md, żeby agent sam decydował w drobnych sprawach i kończył turę bez pytania; `plan` → plan mode tylko dla dużych zadań; długie przerwy po turze → kolejka zadań / mniej sesji naraz.
2. **Zgody**: `permissions.byPattern` (liczba, suma czekania, projekty), `byMode`, `denied`. Reguły - punkt 3.
3. **Równoległość**: `avgWorking` gdy aktywne, maks., `multiWaitingEvents`, udział czasu z ≥1 czekającą. Dużo jednoczesnych czekań i rosnąca reakcja → za dużo sesji naraz; ~1 pracująca sesja i długie przerwy → miejsce na więcej równoległych zadań (worktree, podagenci, tło).
4. **Błędy i tarcie**: błędy wg typu, kompaktowania wg projektu (duży kontekst → krótsze zadania, podagenci, odchudzony CLAUDE.md), narzędzia z wysokim % błędów (np. PowerShell/Bash na złej powłoce, flaky testy), przerwania, długie narzędzia.
5. **Maszyny / projekty / modele**: gdzie waitShare i błędy odstają; czy drogi model nie robi prostych rzeczy (i odwrotnie).

## 3. Reguły uprawnień Claude Code (składnia z code.claude.com/docs/en/permissions)

- Bash: `Bash(npm test *)` - `*` po podkomendzie, spacja przed `*` (dopasowuje też samo `npm test`). `Bash(npm test:*)` działa tak samo, ale okno zgody zapisuje formę ze spacją - pisz ją. Bez `*` = dokładnie jedno polecenie. Polecenia złożone (`&&`, `|`, `;`) wymagają zgody na każdą część osobno.
- PowerShell: `PowerShell(Get-ChildItem *)` (aliasy i wielkość liter bez znaczenia).
- WebFetch: `WebFetch(domain:example.com)`; subdomeny `WebFetch(domain:*.example.com)`.
- MCP: `mcp__serwer__narzędzie` albo `mcp__serwer__*` - bez nawiasów.
- Pliki: `Edit(/src/**)` w ustawieniach projektu (anchor = katalog projektu), `Read(~/x/**)`, absolutnie `Read(//c/Users/x/**)`. Dla wielu zgód na edycję rozważ tryb `acceptEdits` zamiast reguł.

Wzorzec z raportu → reguła: `Bash npm test` → `Bash(npm test *)`; `WebFetch domain:x` → `WebFetch(domain:x)`. Gdy wzorzec to samo narzędzie, jedno słowo (`Bash git`) albo opis zamiast polecenia (zdarzenia sprzed 2026-11-16), sprawdź prawdziwe polecenia w `/api/events` i zaproponuj węższą regułę z konkretną podkomendą.

Gdzie: wzorzec w wielu projektach → `~/.claude/settings.json` → `permissions.allow` (na każdej maszynie z tych próśb; host jest w zdarzeniach); jednym projekcie → `<projekt>/.claude/settings.json` (wspólne z repo) albo `.claude/settings.local.json` (tylko dla siebie); ścieżkę projektu weź z `cwd` w zdarzeniach. Przed propozycją przeczytaj istniejące `permissions` (allow/ask/deny) - nie proponuj duplikatów; jeśli reguła już jest, a prośby trwają, wyjaśnij, czemu nie pasuje (złożone polecenie, inna forma, ask/deny ma pierwszeństwo).

**Nigdy nie proponuj zbiorczo** (oznacz jako ryzykowne, najwyżej dokładne polecenie albo `ask`): `rm`/`Remove-Item`/`del`, `git push`, `git reset --hard`, `git clean`, `git checkout --`/`restore`, szerokie `Bash(git *)`/`Bash(npm *)`/`Bash(*)`/`Bash`/`PowerShell`, `*` przed podkomendą, `curl`/`wget`/`Invoke-WebRequest` do dowolnych hostów (wolisz `WebFetch(domain:...)`), `ssh`/`scp`, `sudo`, `chmod`/`chown`, `bash -c`/`sh -c`/`python -c`/`node -e`/`iex`/`Invoke-Expression`, uruchamiacze `npx`/`docker run|exec`/`uv run` (tylko z konkretną komendą w środku), `npm publish`, `gh pr merge`, `terraform apply`, `kubectl delete|apply`, `WebFetch(domain:*)`.

## 4. Wynik

1. 2-3 zdania: okres, skala (sesje, maszyny), ile czasu agenci czekali na użytkownika.
2. Rekomendacje (najwyżej 7), od największego zysku: **obserwacja z liczbami → działanie → szacowany zysk** (np. „~25 min/tydzień mniej czekania”).
3. Proponowane reguły w bloku `json` pogrupowane według pliku docelowego, z liczbą próśb i czasem czekania, które usuwają; osobno odrzucone ryzykowne wzorce z powodem.
4. Pytanie: które reguły zastosować (wszystkie / numery / żadne).

**Nie zmieniaj żadnych ustawień bez wyraźnego „tak”.** Po zgodzie, dla każdego pliku: zrób kopię `<plik>.bak-agent-flow` (nadpisz poprzednią), wczytaj JSON, dopisz do `permissions.allow` tylko brakujące reguły (bez duplikatów, zachowaj kolejność i wszystkie inne klucze), zapisz jako UTF-8 bez BOM, sprawdź, że plik nadal jest poprawnym JSON-em, i pokaż dopisane reguły. Plik nie istnieje → utwórz z samym `permissions.allow`. Przypomnij, że reguły działają od następnego wywołania narzędzia, a reguły projektu wymagają zaufania do folderu.
