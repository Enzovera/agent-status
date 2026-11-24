package stats

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"agent-status/internal/status"
)

var t0 = time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)

func m(n float64) time.Time { return t0.Add(time.Duration(n * float64(time.Minute))) }

type recOpt func(*status.Record)

func rec(at time.Time, key, kind string, prev, state status.State, opts ...recOpt) status.Record {
	parts := strings.SplitN(key, "/", 3)
	r := status.Record{At: at, Key: key, Agent: parts[0], Host: parts[1], SessionID: parts[2], Kind: kind, Prev: prev, State: state}
	for _, o := range opts {
		o(&r)
	}
	return r
}

func project(p string) recOpt { return func(r *status.Record) { r.Project = p } }
func detail(d string) recOpt  { return func(r *status.Record) { r.Detail = d } }
func perm() recOpt            { return func(r *status.Record) { r.Permission = true } }
func prompt() recOpt          { return func(r *status.Record) { r.Prompt = "zrób to" } }
func tool(t string) recOpt    { return func(r *status.Record) { r.Tool = t } }
func done(t string, ms int64) recOpt {
	return func(r *status.Record) { r.Reason, r.DurationMs = t, ms }
}
func errKind(k string) recOpt { return func(r *status.Record) { r.ErrorKind = k } }

const (
	W  = status.StateWorking
	Q  = status.StateWaiting
	I  = status.StateIdle
	E  = status.StateError
	kA = "claude/h1/a"
	kB = "claude/h2/b"
	kC = "opencode/h1/c"
	kD = "opencode/h1/d"
)

func fixture() []status.Record {
	p1, p2 := project("p1"), project("p2")
	return []status.Record{
		// C: only in the lookback; its capped work ends before the period.
		rec(m(-270), kC, "session.execution.started", I, W, p1),
		// D: waiting since before the period; the episode began outside it.
		rec(m(-90), kD, "permission.asked", W, Q, p1, perm(), detail("zgoda: edit")),
		rec(m(-30), kD, "permission.replied", Q, W, p1),
		rec(m(-15), kD, "session.execution.succeeded", W, I, p1),

		rec(m(0), kA, "UserPromptSubmit", I, W, p1, prompt()),
		rec(m(5), kB, "UserPromptSubmit", I, W, p2, prompt()),
		rec(m(10), kA, "PreToolUse", W, W, p1, tool("Bash")),
		rec(m(15), kB, "PermissionRequest", W, Q, p2, perm(), detail("zgoda: Bash: npm test -- --watch")),
		rec(m(20), kA, "PermissionRequest", W, Q, p1, perm(), detail("zgoda: Bash: npm test")),
		// B approved without a tool completion: the next PreToolUse ends the wait.
		rec(m(20.5), kB, "PreToolUse", Q, W, p2, tool("Bash")),
		// A: the approved tool ran 1 min, so the user answered at 21.
		rec(m(22), kA, "PostToolUse", Q, Q, p1, done("Bash", 60000)),
		rec(m(25), kA, "PreToolUse", Q, W, p1, tool("Read")),
		rec(m(30), kA, "Stop", W, I, p1),
		rec(m(40), kA, "UserPromptSubmit", I, W, p1, prompt()),
		rec(m(50), kA, "PreToolUse", W, Q, p1, tool("AskUserQuestion"), detail("pytanie: dalej?")),
		// The question tool's duration is the wait itself: answered at 55.
		rec(m(55), kA, "PostToolUse", Q, Q, p1, done("AskUserQuestion", 300000)),
		rec(m(60), kA, "Stop", W, I, p1),
		// B silent for 2.5 h while "working": counted as 30 min.
		rec(m(180), kB, "PreToolUse", W, W, p2, tool("Bash")),
		rec(m(185), kB, "StopFailure", W, E, p2, errKind("rate_limit"), detail("błąd: limit zapytań")),
	}
}

func compute(t *testing.T, recs []status.Record) *Stats {
	t.Helper()
	dir := t.TempDir()
	a, err := status.OpenArchive(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if err := a.Write(r); err != nil {
			t.Fatal(err)
		}
	}
	_ = a.Close()
	st, err := Load(dir, Options{From: m(-60), To: m(300), Now: m(300), Loc: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestReplayTotals(t *testing.T) {
	st := compute(t, fixture())
	tot := st.Totals
	min := func(f float64) int64 { return int64(f * 60) }
	checks := []struct {
		name      string
		got, want int64
	}{
		// A 20+9+10+5, B 10+30(cap)+5, D 15
		{"working", tot.WorkingSec, min(44 + 45 + 15)},
		// A 1+5, B 5.5, D 30 (clipped at the period start)
		{"waiting", tot.WaitingSec, min(6 + 5.5 + 30)},
		// A 10 + 240 (cap), D 240 (cap)
		{"idle", tot.IdleSec, min(10 + 240 + 240)},
		{"error", tot.ErrorSec, min(115)},
		{"sessions", int64(tot.Sessions), 3},
		{"prompts", int64(tot.Prompts), 3},
		{"turns", int64(tot.Turns), 4},
		{"errors", int64(tot.Errors), 1},
		{"permissions", int64(tot.Permissions), 2},
		{"tool calls", int64(tot.ToolCalls), 5},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	if len(st.Sessions) != 3 {
		t.Errorf("sessions list has %d rows, want 3 (C is outside the period)", len(st.Sessions))
	}
	if st.Coverage.Records != 17 || st.Coverage.LookbackRecs != 2 {
		t.Errorf("coverage = %d in period, %d lookback; want 17, 2", st.Coverage.Records, st.Coverage.LookbackRecs)
	}
}

func TestLatency(t *testing.T) {
	st := compute(t, fixture())
	l := st.Latency
	// A permission 60 s, A question 300 s, B permission 330 s; D's began before the period.
	if l.All.Count != 3 || l.All.MedianSec != 300 || l.All.MaxSec != 330 || l.All.TotalSec != 690 {
		t.Errorf("all = %+v", l.All)
	}
	by := map[string]Dist{}
	for _, c := range l.ByCause {
		by[c.Cause] = c.Dist
	}
	if by[CausePermission].Count != 2 || by[CausePermission].TotalSec != 390 || by[CauseQuestion].Count != 1 || by[CauseQuestion].MaxSec != 300 {
		t.Errorf("by cause = %+v", by)
	}
	if st.NextPrompt.Count != 1 || st.NextPrompt.MedianSec != 600 {
		t.Errorf("next prompt = %+v", st.NextPrompt)
	}
}

func TestParallelismAndHotspots(t *testing.T) {
	st := compute(t, fixture())
	p := st.Parallelism
	if p.MaxWorking != 2 || p.MultiWaitingEvents != 1 || p.MultiWaitingSec != 30 || p.MaxWaiting != 2 {
		t.Errorf("parallelism = %+v", p)
	}
	if len(st.Permissions.ByPattern) == 0 {
		t.Fatal("no permission patterns")
	}
	top := st.Permissions.ByPattern[0]
	if top.Pattern != "Bash npm test" || top.Count != 2 || top.Sessions != 2 || top.Projects != 2 || top.Waits.TotalSec != 390 {
		t.Errorf("top pattern = %+v", top)
	}
	if len(st.Errors) != 1 || st.Errors[0].Key != "rate_limit" {
		t.Errorf("errors = %+v", st.Errors)
	}
	var bash ToolStat
	for _, x := range st.Tools {
		if x.Tool == "Bash" {
			bash = x
		}
	}
	if bash.Calls != 3 || bash.Completed != 1 || bash.TotalMs != 60000 || bash.Permissions != 2 {
		t.Errorf("Bash = %+v", bash)
	}
	h10 := st.ByHour[10]
	if h10.Key != "10" || h10.Prompts != 3 || h10.Waits.Count != 3 {
		t.Errorf("hour 10 = %+v", h10.Group)
	}
	// Hour 13 holds B's last 5 min of work and the start of its error.
	if st.ByHour[13].WorkingSec != 300 {
		t.Errorf("hour 13 working = %d, want 300", st.ByHour[13].WorkingSec)
	}
	if len(st.ByDay) != 1 || st.ByDay[0].Key != "2026-11-10" {
		t.Errorf("days = %+v", st.ByDay)
	}
}

func TestReport(t *testing.T) {
	st := compute(t, fixture())
	var b strings.Builder
	if err := Report(&b, st); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"## Jak czytać", "## Sygnały do analizy", "`Bash npm test`", "/api/events?", "rate_limit"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q", want)
		}
	}
}

func TestPermTarget(t *testing.T) {
	cases := []struct{ in, tool, pat string }{
		{"zgoda: Bash: npm test", "Bash", "Bash npm test"},
		{"zgoda: Bash: npm run build --prod", "Bash", "Bash npm run"},
		{"zgoda: Bash: git -C /x status", "Bash", "Bash git"},
		{"zgoda: Bash: ./build.sh", "Bash", "Bash"},
		{"zgoda: Edit: C:\\src\\main.go", "Edit", "Edit"},
		{"zgoda: WebFetch: https://example.com/a?b=c", "WebFetch", "WebFetch domain:example.com"},
		{"zgoda: mcp__github__create_pr", "mcp__github__create_pr", "mcp__github__create_pr"},
		{"zgoda: Allow running the tests now?", "(inne)", "(inne) Allow running the"},
	}
	for _, c := range cases {
		tool, pat := permTarget(c.in)
		if tool != c.tool || pat != c.pat {
			t.Errorf("permTarget(%q) = %q, %q; want %q, %q", c.in, tool, pat, c.tool, c.pat)
		}
	}
}

func TestParsePeriod(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	from, to, err := ParsePeriod(url.Values{}, now, time.UTC)
	if err != nil || !to.Equal(now) || !from.Equal(now.Add(-7*24*time.Hour)) {
		t.Errorf("default = %v %v %v", from, to, err)
	}
	from, to, err = ParsePeriod(url.Values{"from": {"2026-11-01"}, "to": {"2026-11-02"}}, now, time.UTC)
	if err != nil || !from.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) || !to.Equal(time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("dates = %v %v %v", from, to, err)
	}
	if _, _, err := ParsePeriod(url.Values{"days": {"0"}}, now, time.UTC); err == nil {
		t.Error("days=0 accepted")
	}
	if _, _, err := ParsePeriod(url.Values{"from": {"2026-11-05T00:00:00Z"}, "to": {"2026-11-04"}}, now, time.UTC); err == nil {
		t.Error("from after to accepted")
	}
}

func TestWaitingOvernightIsCapped(t *testing.T) {
	recs := []status.Record{
		rec(m(0), kA, "UserPromptSubmit", I, W, prompt()),
		rec(m(10), kA, "PermissionRequest", W, Q, perm(), detail("zgoda: Bash: make")),
		rec(m(10+9*60), kA, "PreToolUse", Q, W, tool("Bash")),
	}
	dir := t.TempDir()
	a, _ := status.OpenArchive(dir)
	for _, r := range recs {
		_ = a.Write(r)
	}
	_ = a.Close()
	st, err := Load(dir, Options{From: m(-60), To: m(24 * 60), Now: m(24 * 60), Loc: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if st.Totals.WaitingSec != int64(MaxInterval.Seconds()) {
		t.Errorf("waiting = %d, want the 4 h cap", st.Totals.WaitingSec)
	}
	if st.Latency.All.Count != 0 || st.Latency.Over4h != 1 || st.Sessions[0].LongestWaitSec != 9*3600 {
		t.Errorf("latency = %+v, longest %d", st.Latency, st.Sessions[0].LongestWaitSec)
	}
}

func parent(k string) recOpt { return func(r *status.Record) { r.Parent = k } }
func mode(md string) recOpt  { return func(r *status.Record) { r.Mode = md } }

func TestSubagentsCountOnce(t *testing.T) {
	sub := "claude/h1/sub"
	recs := []status.Record{
		rec(m(0), kA, "UserPromptSubmit", I, W, prompt(), mode("auto")),
		// The subagent works 10–20 inside A's turn, and gets its task as a "prompt".
		rec(m(10), sub, "UserPromptSubmit", I, W, prompt(), parent(kA)),
		rec(m(15), sub, "PreToolUse", W, W, tool("Read"), parent(kA)),
		rec(m(20), sub, "Stop", W, I, parent(kA)),
		rec(m(30), kA, "Stop", W, I),
		rec(m(31), kA, "PermissionDenied", I, I, detail("odmowa: Bash: rm -rf build"), func(r *status.Record) { r.Reason = "[Destructive]" }),
	}
	st := compute(t, recs)
	tot := st.Totals
	if tot.WorkingSec != 30*60 || tot.SubagentWorkingSec != 10*60 {
		t.Errorf("working = %d, subagent = %d; want 1800, 600", tot.WorkingSec, tot.SubagentWorkingSec)
	}
	if tot.Sessions != 1 || tot.Prompts != 1 || tot.Turns != 1 || st.Coverage.SubagentSessions != 1 {
		t.Errorf("sessions %d prompts %d turns %d subagents %d; want 1 1 1 1", tot.Sessions, tot.Prompts, tot.Turns, st.Coverage.SubagentSessions)
	}
	if p := st.Parallelism; p.MaxWorking != 1 || p.AvgWorkingWhenActive != 1 {
		t.Errorf("parallelism = %+v; the subagent runs inside its main session", p)
	}
	if st.TurnLength.Count != 1 || st.TurnLength.MedianSec != 30*60 {
		t.Errorf("turn length = %+v", st.TurnLength)
	}
	if len(st.SessionModes) != 1 || st.SessionModes[0].Key != "claude: auto" {
		t.Errorf("session modes = %+v", st.SessionModes)
	}
	if d := st.Permissions.DeniedDetails; len(d) != 1 || d[0].Key != "odmowa: Bash: rm -rf build" {
		t.Errorf("denied details = %+v", d)
	}
}

func TestOpenCodeToolFailureGoesToLastTool(t *testing.T) {
	recs := []status.Record{
		rec(m(0), kC, "session.execution.started", I, W),
		rec(m(1), kC, "session.tool.called", W, W, tool("edit")),
		rec(m(2), kC, "session.tool.failed", W, W, detail("błąd narzędzia: Could not find oldString")),
		rec(m(3), kC, "session.tool.called", W, W, tool("shell")),
		rec(m(4), kC, "session.tool.failed", W, W, tool("shell")),
		rec(m(5), kA, "PreToolUse", I, W, tool("shell")),
	}
	st := compute(t, recs)
	got := map[string]ToolStat{}
	for _, x := range st.Tools {
		got[x.Agent+"/"+x.Tool] = x
	}
	if e := got["opencode/edit"]; e.Calls != 1 || e.Failures != 1 {
		t.Errorf("edit = %+v", e)
	}
	if s := got["opencode/shell"]; s.Calls != 1 || s.Failures != 1 {
		t.Errorf("opencode shell = %+v", s)
	}
	if s := got["claude/shell"]; s.Calls != 1 || s.Failures != 0 {
		t.Errorf("claude shell = %+v (tools are per agent)", s)
	}
}
