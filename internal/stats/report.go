package stats

import (
	"fmt"
	"io"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Report writes the statistics as Markdown meant for an AI agent that will
// advise the user how to work with coding agents. It states facts only.
func Report(w io.Writer, st *Stats) error {
	b := &strings.Builder{}
	p := func(format string, a ...any) { fmt.Fprintf(b, format, a...) }
	loc := st.loc
	if loc == nil {
		loc = time.Local
	}
	ts := func(t time.Time) string { return t.In(loc).Format("2006-01-02 15:04") }
	q := url.Values{}
	q.Set("from", st.Period.From.Format(time.RFC3339))
	q.Set("to", st.Period.To.Format(time.RFC3339))
	period := q.Encode()

	p("# Statystyki pracy z agentami (agent-status)\n\n")
	p("Okres: %s – %s (%s, %.2f dni). Wygenerowano: %s.\n\n", ts(st.Period.From), ts(st.Period.To), st.Period.TZ, st.Period.Days, ts(st.Period.GeneratedAt))
	p("Użytkownik prowadzi równolegle wiele sesji Claude Code / OpenCode na kilku maszynach i odblokowuje je (zgody, pytania, kolejne polecenia). ")
	p("Ten raport opisuje, ile czasu sesje pracowały, ile czekały na użytkownika i dlaczego. Zawiera tylko fakty z danych; wnioski i rady należą do czytelnika.\n\n")
	p("Te same liczby jako JSON: `GET /api/stats?%s`. Surowe zdarzenia (JSON lines, jedno zdarzenie w wierszu): `GET /api/events?%s` (opcjonalnie `&host=`, `&project=`, `&key=`).\n\n", period, period)

	p("## Jak czytać\n\n")
	p("- **Źródło**: archiwum zdarzeń hooków Claude Code i pluginu OpenCode. Każdy rekord ma czas, sesję (`key` = agent/maszyna/id sesji), rodzaj zdarzenia (`kind`, np. `PreToolUse`, `PermissionRequest`, `Stop`) oraz stan sesji przed (`prev`) i po (`state`).\n")
	p("- **Stany**: `working` – agent pracuje; `waiting` – agent czeka na użytkownika (zgoda, pytanie, akceptacja planu, decyzja); `idle` – tura skończona, sesja czeka na kolejne polecenie; `error` – tura przerwana błędem; `ended` – sesja zamknięta.\n")
	p("- **Czas w stanie** (`workingSec`, `waitingSec`, `idleSec`): suma odcinków między kolejnymi zdarzeniami sesji. Limity: pojedynczy odcinek liczy się najwyżej %s; odcinek `working` bez kolejnego zdarzenia najwyżej %s (pracujący agent zgłasza każde narzędzie, dłuższa cisza to zawieszenie albo zgubione zdarzenie), chyba że zakończenie narzędzia z czasem trwania pokrywa przerwę. Do ustalenia stanu na początku okresu odtwarzane są zdarzenia z %s przed nim.\n",
		dur(st.Caps.IntervalMaxSec), dur(st.Caps.WorkingSilenceMaxSec), dur(st.Caps.LookbackSec))
	p("- **waitShare** = waiting / (working + waiting): jaka część aktywnego czasu sesji to blokada na użytkowniku.\n")
	p("- **Czas reakcji użytkownika** (`latency`, „epizod oczekiwania”): od wejścia sesji w `waiting` do wyjścia z niego; przypisany do godziny/dnia rozpoczęcia. Epizody dłuższe niż %s (użytkownik był nieobecny) są liczone osobno (`over4h`) i nie wchodzą do mediany / p90 / max. Gdy po prośbie o zgodę przychodzi `PostToolUse` (Claude Code nie zgłasza samej akceptacji), koniec oczekiwania to start narzędzia (czas zakończenia minus jego czas trwania).\n", dur(st.Caps.LatencyMaxSec))
	p("- **Przyczyny** (`cause`) według zdarzenia, które zaczęło oczekiwanie: `permission` – prośba o zgodę na narzędzie (`zgoda: …`); `question` – pytanie agenta (`pytanie…`); `plan` – plan do akceptacji; `decision` – decyzja zadana na koniec tury (`decyzja: …`); `session_offer` – propozycja nowej sesji; `other` – reszta (ogólne powiadomienia, formularze).\n")
	p("- **Podagenci**: sesje podagentów (Claude Code z własnym id, dzieci sesji w OpenCode) nie są liczone jako sesje, a ich czas pracy jest osobno (`subagentWorkingSec`), bo biegnie w trakcie tury sesji głównej – inaczej byłby liczony dwa razy. Ich polecenia (zadanie od sesji głównej) i tury nie wchodzą do `prompts`/`turns`. W równoległości sesja główna z podagentami to jedna sesja.\n")
	p("- **prompts** – polecenia użytkownika; **turns** – zakończone tury agenta (`working` → `idle`/`error`, albo `Stop` kończący turę pytaniem). **Długość tury** (`turnLength`): od polecenia do końca tury, z czekaniem na użytkownika w środku.\n")
	p("- **idle** to czas otwartych sesji bez pracy (także noce i porzucone sesje, do 4 h na odcinek) – nie jest to czas użytkownika ani strata; do oceny przestojów służą „czas do kolejnego polecenia” i równoległość.\n")
	p("- **Czas do kolejnego polecenia** (`nextPrompt`): od końca tury (`working` → `idle`) do następnego `working` tej sesji; tylko przerwy krótsze niż %s.\n", dur(st.Caps.NextPromptMaxSec))
	p("- **Równoległość**: `avgWorking` – średnia liczba jednocześnie pracujących sesji (w całym okresie i w czasie, gdy pracowała co najmniej jedna); `activeSec` – czas, gdy co najmniej jedna sesja pracowała lub czekała; `anyWaitingSec` – czas, gdy co najmniej jedna czekała na użytkownika; `multiWaitingEvents` – ile razy liczba czekających sesji wzrosła do 2+.\n")
	p("- **Zgody a tryb**: w trybie `auto` (i `bypassPermissions`) Claude Code nie pyta o zgody, więc 0 próśb nie znaczy, że allowlista jest kompletna – patrz „Tryby uprawnień sesji” i odmowy trybu auto (`PermissionDenied`). Zdarzenie `PermissionRequest` z opisem `pytanie: …` to pytanie agenta (AskUserQuestion), nie zgoda – liczy się jako `question`.\n")
	p("- **Narzędzia** są liczone osobno dla każdego agenta (Claude Code: `Bash`, `Read`…; OpenCode: `shell`, `read`…). OpenCode nie zgłasza zakończenia narzędzia, więc nie ma dla niego czasów (`–`); błędy pochodzą z `session.tool.failed` – w zdarzeniach bez nazwy narzędzia (starsza wtyczka) błąd jest przypisany ostatniemu wywołanemu narzędziu sesji.\n")
	p("- **Wzorce zgód** (`permissions.byPattern`): prośby o zgodę pogrupowane po narzędziu i znormalizowanym poleceniu: dla powłoki (Bash, PowerShell) najwyżej 2 pierwsze słowa polecenia, ucięte na fladze, ścieżce lub składni powłoki; adres URL → domena; inne argumenty (zwykle ścieżki) → samo narzędzie. Uwaga: zdarzenia zapisane przed 2026-11-16 mają przy Bash w Claude Code opis wywołania (`description`, np. „Run tests”) zamiast polecenia – wtedy wzorzec zawiera słowa opisu.\n")
	p("- **Godziny i dni tygodnia**: czas lokalny serwera (%s), sumowane po wszystkich dniach okresu. `spanSec` – ile czasu okresu przypada na dany kubełek.\n", st.Period.TZ)
	p("- Czasy podane jako s / min / h; w JSON zawsze sekundy.\n\n")

	p("## Zakres danych\n\n")
	p("- Zdarzeń w okresie: %d (plus %d wcześniejszych użytych tylko do stanu początkowego).\n", st.Coverage.Records, st.Coverage.LookbackRecs)
	cv := st.Coverage
	if !cv.First.IsZero() {
		p("- Pierwsze / ostatnie zdarzenie: %s / %s. Dni z danymi: %d z %.0f.\n", ts(cv.First), ts(cv.Last), cv.DaysWithData, math.Ceil(st.Period.Days))
	}
	if cv.ClaudeSessions > 0 {
		p("- Sesje Claude Code: %d; z nazwą modelu: %d; z kosztem: %d (model i koszt daje tylko skrypt statusLine", cv.ClaudeSessions, cv.ClaudeWithModel, cv.ClaudeWithCost)
		if len(cv.NoStatusline) > 0 {
			p("; maszyny z sesjami Claude Code bez żadnego rekordu statusLine: %s", strings.Join(cv.NoStatusline, ", "))
		}
		p("). Tabele modeli i kosztu obejmują tylko te sesje.\n")
	}
	if len(st.SessionModes) > 0 {
		p("- Tryby uprawnień sesji (ostatni widziany `permission_mode`, sesje główne; OpenCode go nie zgłasza): %s.\n", countList(st.SessionModes, 10))
	}
	if len(st.Coverage.Kinds) > 0 {
		p("- Rodzaje zdarzeń: %s.\n", countList(st.Coverage.Kinds, 30))
	}
	if len(st.Coverage.Missing) > 0 {
		p("- Brak w danych (starsi klienci albo brak hooka; odpowiednie liczby są zerowe, nie znaczy to, że zjawisko nie wystąpiło): %s.\n", strings.Join(st.Coverage.Missing, "; "))
	}
	p("\n")
	if st.Coverage.Records == 0 {
		p("Brak zdarzeń w tym okresie.\n")
		_, err := io.WriteString(w, b.String())
		return err
	}

	t := st.Totals
	par := st.Parallelism
	p("## Podsumowanie\n\n| metryka | wartość |\n|---|---|\n")
	p("| sesje aktywne | %d (+%d sesji podagentów) |\n| polecenia (prompts) | %d |\n| tury (turns) | %d |\n| wywołania narzędzi | %d |\n", t.Sessions, cv.SubagentSessions, t.Prompts, t.Turns, t.ToolCalls)
	p("| czas working | %s (+%s pracy podagentów w tym czasie) |\n| czas waiting (na użytkownika) | %s |\n| czas idle (otwarte sesje bez pracy) | %s |\n| czas error | %s |\n", dur(t.WorkingSec), dur(t.SubagentWorkingSec), dur(t.WaitingSec), dur(t.IdleSec), dur(t.ErrorSec))
	p("| długość tury | mediana %s, p90 %s (n=%d) |\n", dur(st.TurnLength.MedianSec), dur(st.TurnLength.P90Sec), st.TurnLength.Count)
	p("| waitShare | %s |\n", pct(t.WaitShare))
	p("| prośby o zgodę | %d |\n| epizody oczekiwania (≤ 4 h) | %d (mediana %s, p90 %s) |\n", t.Permissions, st.Latency.All.Count, dur(st.Latency.All.MedianSec), dur(st.Latency.All.P90Sec))
	p("| epizody > 4 h / niezakończone na końcu okresu | %d / %d |\n", st.Latency.Over4h, st.Latency.OpenAtEnd)
	p("| czas do kolejnego polecenia | mediana %s, p90 %s (n=%d) |\n", dur(st.NextPrompt.MedianSec), dur(st.NextPrompt.P90Sec), st.NextPrompt.Count)
	p("| błędy / kompaktowania / przerwania | %d / %d / %d |\n", t.Errors, st.Compactions.Total, t.Interruptions)
	p("| średnio pracujących sesji (cały okres / gdy ≥1 pracuje) | %.2f / %.2f |\n", par.AvgWorking, par.AvgWorkingWhenActive)
	p("\n")

	p("## Czas reakcji użytkownika według przyczyny\n\n| cause | epizody | mediana | p90 | max | suma |\n|---|---|---|---|---|---|\n")
	for _, c := range st.Latency.ByCause {
		p("| %s | %d | %s | %s | %s | %s |\n", c.Cause, c.Count, dur(c.MedianSec), dur(c.P90Sec), dur(c.MaxSec), dur(c.TotalSec))
	}
	p("| **wszystkie** | %d | %s | %s | %s | %s |\n\n", st.Latency.All.Count, dur(st.Latency.All.MedianSec), dur(st.Latency.All.P90Sec), dur(st.Latency.All.MaxSec), dur(st.Latency.All.TotalSec))

	p("## Równoległość\n\n| metryka | wartość |\n|---|---|\n")
	p("| długość okresu | %s |\n| czas z ≥1 sesją aktywną (working/waiting) | %s |\n| czas z ≥1 sesją pracującą | %s |\n", dur(par.PeriodSec), dur(par.ActiveSec), dur(par.AnyWorkingSec))
	p("| czas z ≥1 sesją czekającą na użytkownika | %s (%s okresu, %s czasu aktywnego) |\n", dur(par.AnyWaitingSec), pct(par.AnyWaitingShare), pct(par.AnyWaitingShareOfActive))
	p("| czas z ≥2 czekającymi naraz | %s; wystąpienia: %d |\n", dur(par.MultiWaitingSec), par.MultiWaitingEvents)
	p("| maks. pracujących naraz | %d%s |\n| maks. czekających naraz | %d%s |\n\n", par.MaxWorking, at(par.MaxWorkingAt, ts), par.MaxWaiting, at(par.MaxWaitingAt, ts))

	timeTable := func(title string, gs []TimeGroup, skipEmpty bool) {
		p("## %s\n\n| %s | working | waiting | ≥1 aktywna | ≥1 czeka | śr. pracujących | polecenia | zgody | epizody | mediana reakcji | błędy |\n|---|---|---|---|---|---|---|---|---|---|---|\n", title, "kubełek")
		for _, g := range gs {
			if skipEmpty && g.WorkingSec+g.WaitingSec+int64(g.Prompts) == 0 {
				continue
			}
			p("| %s | %s | %s | %s | %s | %.2f | %d | %d | %d | %s | %d |\n", g.Key, dur(g.WorkingSec), dur(g.WaitingSec),
				dur(g.ActiveSec), dur(g.AnyWaitingSec), g.AvgWorking, g.Prompts, g.Permissions, g.Waits.Count, dur(g.Waits.MedianSec), g.Errors)
		}
		p("\n")
	}
	timeTable("Godziny dnia (czas lokalny; puste pominięte)", st.ByHour, true)
	timeTable("Dni tygodnia", st.ByWeekday, false)
	timeTable("Dni (puste pominięte)", st.ByDay, true)

	groupTable := func(title string, gs []Group, limit int) {
		p("## %s\n\n| klucz | sesje | polecenia | tury | working | waiting | waitShare | zgody | epizody | mediana reakcji | błędy | kompakt. | przerwania |\n|---|---|---|---|---|---|---|---|---|---|---|---|---|\n", title)
		for i, g := range gs {
			if i == limit {
				p("| … i %d więcej (pełna lista w /api/stats) | | | | | | | | | | | | |\n", len(gs)-limit)
				break
			}
			key := g.Key
			if g.Label != "" {
				key = g.Label + " (" + g.Key + ")"
			}
			p("| %s | %d | %d | %d | %s | %s | %s | %d | %d | %s | %d | %d | %d |\n", cell(key), g.Sessions, g.Prompts, g.Turns, dur(g.WorkingSec), dur(g.WaitingSec),
				pct(g.WaitShare), g.Permissions, g.Waits.Count, dur(g.Waits.MedianSec), g.Errors, g.Compactions, g.Interruptions)
		}
		p("\n")
	}
	groupTable("Maszyny", st.ByHost, 30)
	groupTable("Projekty", st.ByProject, 30)
	groupTable("Agenci", st.ByAgent, 10)
	if cv.ClaudeSessions > 0 && cv.ClaudeWithModel < cv.ClaudeSessions {
		groupTable(fmt.Sprintf("Modele (sesje Claude Code bez statusLine są w „(nieznany)”: %d z %d)", cv.ClaudeSessions-cv.ClaudeWithModel, cv.ClaudeSessions), st.ByModel, 15)
	} else {
		groupTable("Modele", st.ByModel, 15)
	}
	costReport(p, st)

	pm := st.Permissions
	p("## Prośby o zgodę\n\n")
	if pm.Total == 0 {
		p("Razem: 0. Tryby uprawnień sesji: %s.\n\n", orDash(countList(st.SessionModes, 10)))
	} else {
		p("Razem: %d. Według narzędzia: %s.\n\n", pm.Total, countList(pm.ByTool, 15))
	}
	if len(pm.ByMode) > 0 {
		p("Według trybu uprawnień sesji (`permission_mode`): %s.\n\n", countList(pm.ByMode, 10))
	}
	if len(pm.Denied) > 0 {
		p("Odmowy w trybie auto (`PermissionDenied`), według powodu: %s. Co zostało odmówione:\n\n", countList(pm.Denied, 10))
		for i, d := range pm.DeniedDetails {
			if i == 15 {
				p("- … i %d więcej (w /api/stats)\n", len(pm.DeniedDetails)-15)
				break
			}
			p("- %d× %s\n", d.Count, cell(d.Key))
		}
		p("\n")
	}
	if len(pm.ByPattern) > 0 {
		p("| wzorzec | narzędzie | liczba | sesje | projekty | mediana reakcji | suma czekania | przykład |\n|---|---|---|---|---|---|---|---|\n")
		for i, x := range pm.ByPattern {
			if i == 30 {
				p("| … i %d więcej | | | | | | | |\n", len(pm.ByPattern)-30)
				break
			}
			p("| `%s` | %s | %d | %d | %d | %s | %s | %s |\n", cell(x.Pattern), cell(x.Tool), x.Count, x.Sessions, x.Projects, dur(x.Waits.MedianSec), dur(x.Waits.TotalSec), cell(x.Example))
		}
		p("\n")
	}

	if len(st.Tools) > 0 {
		p("## Narzędzia\n\n| narzędzie | agent | wywołania | zakończone | błędy | %% błędów | śr. czas | maks. czas | zgody |\n|---|---|---|---|---|---|---|---|---|\n")
		for i, x := range st.Tools {
			if i == 30 {
				p("| … i %d więcej | | | | | | | | |\n", len(st.Tools)-30)
				break
			}
			avg, mx, completed := "–", "–", "–"
			if x.Timed > 0 {
				avg, mx = ms(x.AvgMs), ms(x.MaxMs)
			}
			if x.Completed > 0 {
				completed = fmt.Sprint(x.Completed)
			}
			p("| %s | %s | %d | %s | %d | %s | %s | %s | %d |\n", cell(x.Tool), x.Agent, x.Calls, completed, x.Failures, pct(x.FailureRate), avg, mx, x.Permissions)
		}
		p("\n")
	}

	p("## Błędy, kompaktowania, podagenci, zadania, przerwania\n\n")
	p("- Błędy (wejście w stan `error`), według typu: %s.\n", orDash(countList(st.Errors, 15)))
	p("- Kompaktowania kontekstu: %d; według wyzwalacza: %s; według projektu: %s.\n", st.Compactions.Total, orDash(countList(st.Compactions.ByTrigger, 5)), orDash(countList(st.Compactions.ByProject, 10)))
	p("- Podagenci uruchomieni (`SubagentStart`), według typu: %s.\n", orDash(countList(st.Subagents, 15)))
	p("- Listy zadań: utworzone %d, ukończone %d.\n", st.Todos.Created, st.Todos.Completed)
	p("- Przerwania: %s.\n\n", orDash(countList(st.Interruptions, 10)))

	p("## Sesje\n\n")
	var rows []SessionRow
	var subN int
	var subWork int64
	for _, r := range st.Sessions {
		if r.Parent != "" {
			subN++
			subWork += r.WorkingSec
			continue
		}
		rows = append(rows, r)
	}
	if subN > 0 {
		p("Sesje podagentów (%d, łącznie %s pracy) są pominięte; mają w /api/stats pole `parent`.\n\n", subN, dur(subWork))
	}
	if len(rows) > 60 {
		total := len(rows)
		sort.Slice(rows, func(i, j int) bool {
			return rows[i].WorkingSec+rows[i].WaitingSec > rows[j].WorkingSec+rows[j].WaitingSec
		})
		rows = rows[:60]
		p("%d sesji; poniżej 60 z największym czasem working+waiting (pełna lista w /api/stats).\n\n", total)
	}
	p("| sesja | maszyna | projekt | agent / model | początek | koniec | working | waiting | polecenia | tury | zgody | epizody | najdłuższe czekanie | błędy | kompakt. | stan na końcu |\n|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range rows {
		host := r.Host
		if r.HostLabel != "" {
			host = r.HostLabel
		}
		p("| `%s` | %s | %s | %s | %s | %s | %s | %s | %d | %d | %d | %d | %s | %d | %d | %s |\n", cell(r.Key), cell(host), cell(orNone(r.Project)),
			cell(strings.TrimSuffix(r.Agent+" / "+r.Model, " / ")), ts(r.Start), ts(r.End), dur(r.WorkingSec), dur(r.WaitingSec),
			r.Prompts, r.Turns, r.Permissions, r.Waits, dur(r.LongestWaitSec), r.Errors, r.Compactions, r.LastState)
	}
	p("\n")

	p("## Sygnały do analizy\n\nObserwacje wyliczone z danych powyżej (bez interpretacji).\n\n")
	for _, s := range signals(st, ts) {
		p("- %s\n", s)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// signals lists notable facts for the reader to analyse.
func signals(st *Stats, ts func(time.Time) string) []string {
	var out []string
	add := func(format string, a ...any) { out = append(out, fmt.Sprintf(format, a...)) }
	lat := st.Latency
	if lat.All.Count > 0 {
		for _, c := range lat.ByCause {
			if c.Count == 0 {
				continue
			}
			add("Oczekiwanie z przyczyny `%s`: %d z %d epizodów (%s), łącznie %s czasu reakcji, mediana %s, p90 %s.",
				c.Cause, c.Count, lat.All.Count, pct(float64(c.Count)/float64(lat.All.Count)), dur(c.TotalSec), dur(c.MedianSec), dur(c.P90Sec))
		}
	}
	n := 0
	for _, x := range st.Permissions.ByPattern {
		if x.Count < 2 || n == 8 {
			continue
		}
		n++
		add("Powtarzająca się prośba o zgodę (kandydat do allowlisty): `%s` – %d razy w %d sesjach / %d projektach, łącznie %s czekania na odpowiedź (mediana %s). Przykład: %s.",
			x.Pattern, x.Count, x.Sessions, x.Projects, dur(x.Waits.TotalSec), dur(x.Waits.MedianSec), x.Example)
	}
	if len(st.Permissions.ByMode) > 0 && st.Permissions.Total > 0 {
		m := st.Permissions.ByMode[0]
		add("%s próśb o zgodę padło w sesjach w trybie `%s` (%d z %d).", pct(float64(m.Count)/float64(st.Permissions.Total)), m.Key, m.Count, st.Permissions.Total)
	}
	if len(st.Permissions.Denied) > 0 {
		total := 0
		for _, d := range st.Permissions.Denied {
			total += d.Count
		}
		add("Odmowy w trybie auto (`PermissionDenied`): %d; najczęstszy powód: %s; odmówione: %s.", total, st.Permissions.Denied[0].Key, countList(st.Permissions.DeniedDetails, 5))
	}
	if st.Permissions.Total == 0 && len(st.SessionModes) > 0 {
		add("Brak próśb o zgodę; tryby uprawnień sesji: %s.", countList(st.SessionModes, 5))
	}

	// Sessions that waited longest in total and in one go.
	byWait := append([]SessionRow(nil), st.Sessions...)
	sort.Slice(byWait, func(i, j int) bool { return byWait[i].WaitingSec > byWait[j].WaitingSec })
	for i, r := range byWait {
		if i == 3 || r.WaitingSec < 60 {
			break
		}
		tail := ""
		if r.LastState == "waiting" {
			tail = " Na końcu okresu nadal czekała (ostatnie oczekiwanie bez odpowiedzi, liczone najwyżej 4 h)."
		}
		add("Sesja `%s` (projekt %s) czekała na użytkownika łącznie %s przy %s pracy (waitShare %s); zakończonych epizodów oczekiwania: %d, najdłuższy %s.%s",
			r.Key, orNone(r.Project), dur(r.WaitingSec), dur(r.WorkingSec), pct(ratio(float64(r.WaitingSec), float64(r.WaitingSec+r.WorkingSec))), r.Waits, dur(r.LongestWaitSec), tail)
	}
	if lat.Over4h > 0 {
		add("%d epizodów oczekiwania trwało ponad 4 h (poza medianą; zwykle sesja zostawiona na noc lub przerwa).", lat.Over4h)
	}

	// Hours of the day when sessions most often sat blocked on the user.
	type hourShare struct {
		key   string
		share float64
		g     TimeGroup
	}
	var hs []hourShare
	// Only hours with enough activity and episodes, and a share worth noting.
	for _, g := range st.ByHour {
		if g.ActiveSec >= 600 && g.Waits.Count >= 3 {
			hs = append(hs, hourShare{g.Key, ratio(float64(g.AnyWaitingSec), float64(g.ActiveSec)), g})
		}
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i].share > hs[j].share })
	for i, h := range hs {
		if i == 3 || h.share < 0.1 {
			break
		}
		add("Godzina %s:00: przez %s czasu aktywnego co najmniej jedna sesja czekała na użytkownika (%s z %s); mediana reakcji %s (n=%d), średnio %.2f pracujących sesji.",
			h.key, pct(h.share), dur(h.g.AnyWaitingSec), dur(h.g.ActiveSec), dur(h.g.Waits.MedianSec), h.g.Waits.Count, h.g.AvgWorking)
	}
	// Slowest answers by hour (enough episodes to mean something).
	var slow []TimeGroup
	for _, g := range st.ByHour {
		if g.Waits.Count >= 5 {
			slow = append(slow, g)
		}
	}
	sort.Slice(slow, func(i, j int) bool { return slow[i].Waits.MedianSec > slow[j].Waits.MedianSec })
	if len(slow) >= 2 {
		f, l := slow[0], slow[len(slow)-1]
		add("Mediana czasu reakcji wg godziny (godziny z ≥5 epizodami): najwolniej %s:00 (%s, n=%d), najszybciej %s:00 (%s, n=%d).",
			f.Key, dur(f.Waits.MedianSec), f.Waits.Count, l.Key, dur(l.Waits.MedianSec), l.Waits.Count)
	}

	par := st.Parallelism
	if par.ActiveSec > 0 {
		add("Równoległość: średnio %.2f sesji pracuje naraz, gdy cokolwiek pracuje (maks. %d); przez %s czasu aktywnego co najmniej jedna sesja czekała na użytkownika; 2+ sesje czekały naraz %d razy (łącznie %s).",
			par.AvgWorkingWhenActive, par.MaxWorking, pct(par.AnyWaitingShareOfActive), par.MultiWaitingEvents, dur(par.MultiWaitingSec))
	}
	if days := st.Coverage.DaysWithData; days > 0 && par.PeriodSec > 0 {
		add("Wykorzystanie: co najmniej jedna sesja pracowała łącznie %s, czyli średnio %s na dzień z danymi (%d dni); średnio %.2f sesji pracuje naraz, gdy cokolwiek pracuje.",
			dur(par.AnyWorkingSec), dur(par.AnyWorkingSec/int64(days)), days, par.AvgWorkingWhenActive)
	}
	if tl := st.TurnLength; tl.Count > 0 {
		add("Tura (od polecenia do końca, z czekaniem) trwa medianę %s, p90 %s (n=%d).", dur(tl.MedianSec), dur(tl.P90Sec), tl.Count)
	}
	if st.NextPrompt.Count > 0 {
		add("Po zakończonej turze kolejne polecenie w tej samej sesji przychodzi po medianie %s (p90 %s, n=%d).",
			dur(st.NextPrompt.MedianSec), dur(st.NextPrompt.P90Sec), st.NextPrompt.Count)
	}
	// Long-lived sessions: many prompts over days, often with compactions.
	long := 0
	for _, r := range st.Sessions {
		span := r.End.Sub(r.Start)
		if r.Parent != "" || span < 24*time.Hour || (r.Prompts < 30 && r.Compactions < 2) || long == 3 {
			continue
		}
		long++
		add("Długa sesja `%s` (%s): %.1f dni od pierwszego do ostatniego zdarzenia, %d poleceń, %d kompaktowań.",
			r.Key, orNone(r.Project), span.Hours()/24, r.Prompts, r.Compactions)
	}
	// Sessions still "working" long after their last event: a lost event or a hung agent.
	for _, r := range st.Sessions {
		if r.Parent == "" && r.LastState == "working" && st.Period.To.Sub(r.End) > time.Hour {
			add("Sesja `%s` (%s) ma na końcu stan `working`, ale ostatnia aktywność to %s (zgubione zdarzenie albo zawieszony agent; jej praca liczy się najwyżej 30 min po ostatnim zdarzeniu).",
				r.Key, orNone(r.Project), ts(r.End))
		}
	}

	if len(st.Errors) > 0 {
		add("Błędy: %d; najczęstsze typy: %s.", st.Totals.Errors, countList(st.Errors, 3))
		bySess := append([]SessionRow(nil), st.Sessions...)
		sort.Slice(bySess, func(i, j int) bool { return bySess[i].Errors > bySess[j].Errors })
		if r := bySess[0]; r.Errors >= 2 {
			add("Najwięcej błędów w jednej sesji: `%s` (%s) – %d.", r.Key, orNone(r.Project), r.Errors)
		}
	}
	if st.Compactions.Total > 0 {
		auto := 0
		for _, c := range st.Compactions.ByTrigger {
			if c.Key == "auto" {
				auto = c.Count
			}
		}
		add("Kompaktowania kontekstu: %d (auto: %d); projekty: %s.", st.Compactions.Total, auto, countList(st.Compactions.ByProject, 3))
	}
	for _, x := range st.Tools {
		calls := max(x.Calls, x.Completed)
		if calls >= 5 && x.FailureRate >= 0.1 {
			add("Narzędzie `%s` (%s): %d błędów na %d wywołań (%s).", x.Tool, x.Agent, x.Failures, calls, pct(x.FailureRate))
		}
	}
	var slowTools []ToolStat
	for _, x := range st.Tools {
		if x.Timed >= 3 {
			slowTools = append(slowTools, x)
		}
	}
	sort.Slice(slowTools, func(i, j int) bool { return slowTools[i].TotalMs > slowTools[j].TotalMs })
	for i, x := range slowTools {
		if i == 3 {
			break
		}
		add("Narzędzie `%s` (%s) zajęło łącznie %s (%d wywołań z czasem, średnio %s, maks. %s).", x.Tool, x.Agent, ms(x.TotalMs), x.Timed, ms(x.AvgMs), ms(x.MaxMs))
	}
	if len(st.Interruptions) > 0 {
		total := 0
		for _, c := range st.Interruptions {
			total += c.Count
		}
		add("Przerwania: %d (%s).", total, countList(st.Interruptions, 5))
	}
	if st.Totals.Prompts > 0 && st.Totals.Permissions > 0 {
		add("Na jedno polecenie użytkownika przypada średnio %.2f prośby o zgodę i %.2f epizodu oczekiwania.",
			float64(st.Totals.Permissions)/float64(st.Totals.Prompts), float64(lat.All.Count)/float64(st.Totals.Prompts))
	}
	if len(st.Coverage.Missing) > 0 {
		add("Brakujące rodzaje zdarzeń (liczby dla nich są zerowe): %s.", strings.Join(st.Coverage.Missing, "; "))
	}
	if len(out) == 0 {
		add("Za mało danych na obserwacje.")
	}
	return out
}

func at(t time.Time, ts func(time.Time) string) string {
	if t.IsZero() {
		return ""
	}
	return " (" + ts(t) + ")"
}

func orDash(s string) string {
	if s == "" {
		return "–"
	}
	return s
}

func countList(cs []Count, limit int) string {
	var parts []string
	for i, c := range cs {
		if i == limit {
			parts = append(parts, fmt.Sprintf("… (+%d)", len(cs)-limit))
			break
		}
		parts = append(parts, fmt.Sprintf("%s %d", c.Key, c.Count))
	}
	return strings.Join(parts, ", ")
}

// cell makes text safe inside a Markdown table cell.
func cell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "`", "'")
	return strings.Join(strings.Fields(s), " ")
}

func pct(f float64) string { return fmt.Sprintf("%.0f%%", f*100) }

func dur(sec int64) string {
	switch {
	case sec < 90:
		return fmt.Sprintf("%d s", sec)
	case sec < 600:
		return fmt.Sprintf("%.1f min", float64(sec)/60)
	case sec < 90*60:
		return fmt.Sprintf("%.0f min", float64(sec)/60)
	default:
		return fmt.Sprintf("%.1f h", float64(sec)/3600)
	}
}

func ms(v int64) string {
	if v < 1000 {
		return fmt.Sprintf("%d ms", v)
	}
	return dur((v + 500) / 1000)
}
