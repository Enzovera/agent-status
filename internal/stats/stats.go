// Package stats turns the event archive into statistics about how the user
// works with coding agents: how long sessions work, how long they wait for the
// user and why, how many run in parallel, which permission prompts repeat.
package stats

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"agent-status/internal/status"
)

// Caps keep a lost or very late event from inflating time totals.
const (
	// MaxInterval caps any single interval between two events of a session
	// (e.g. a session left waiting overnight counts at most 4 h).
	MaxInterval = 4 * time.Hour
	// MaxWorkingSilence caps a "working" interval with no further event:
	// a working agent reports every tool call, so a longer silence means the
	// agent hung or an event was lost. A tool completion whose duration covers
	// the gap lifts the cap (up to MaxInterval).
	MaxWorkingSilence = 30 * time.Minute
	// MaxLatency: waiting episodes longer than this (the user was away) are
	// counted separately and left out of median / p90 / max.
	MaxLatency = 4 * time.Hour
	// MaxNextPrompt: gaps between a finished turn and the next prompt longer
	// than this are breaks, not response time.
	MaxNextPrompt = 4 * time.Hour
	// Lookback: records this long before the period are replayed only to know
	// each session's state when the period starts.
	Lookback = MaxInterval
)

// Wait causes, by what put the session into "waiting".
const (
	CausePermission = "permission"    // the agent asked for permission ("zgoda: …")
	CauseQuestion   = "question"      // a question to the user ("pytanie…")
	CausePlan       = "plan"          // plan waiting for approval
	CauseDecision   = "decision"      // decision asked when the turn ended ("decyzja: …")
	CauseOffer      = "session_offer" // proposal to start another session
	CauseOther      = "other"         // anything else (generic notifications, elicitation)
)

var causes = []string{CausePermission, CauseQuestion, CausePlan, CauseDecision, CauseOffer, CauseOther}

func waitCause(r status.Record) string {
	d := strings.ToLower(strings.TrimSpace(r.Detail))
	switch {
	case r.Permission || strings.HasPrefix(d, "zgoda"):
		return CausePermission
	case strings.HasPrefix(d, "pytanie"):
		return CauseQuestion
	case strings.HasPrefix(d, "plan do akceptacji"):
		return CausePlan
	case strings.HasPrefix(d, "decyzja"):
		return CauseDecision
	case strings.HasPrefix(d, "propozycja sesji"):
		return CauseOffer
	}
	return CauseOther
}

// Options select the period. To is exclusive; intervals still open at the end
// are counted up to min(To, Now).
type Options struct {
	From, To  time.Time
	Now       time.Time
	Loc       *time.Location    // for days, hours and weekdays (default: local)
	HostNames map[string]string // display names of machines
}

type Stats struct {
	Period        Period       `json:"period"`
	Caps          Caps         `json:"caps"`
	Coverage      Coverage     `json:"coverage"`
	Totals        Group        `json:"totals"`
	Latency       Latency      `json:"latency"`
	NextPrompt    Dist         `json:"nextPrompt"`
	Parallelism   Parallelism  `json:"parallelism"`
	ByHour        []TimeGroup  `json:"byHour"`
	ByWeekday     []TimeGroup  `json:"byWeekday"`
	ByDay         []TimeGroup  `json:"byDay"`
	ByHost        []Group      `json:"byHost"`
	ByProject     []Group      `json:"byProject"`
	ByAgent       []Group      `json:"byAgent"`
	ByModel       []Group      `json:"byModel"`
	Permissions   Permissions  `json:"permissions"`
	Tools         []ToolStat   `json:"tools"`
	Errors        []Count      `json:"errors"`
	Compactions   Compactions  `json:"compactions"`
	Subagents     []Count      `json:"subagents"`
	Todos         Todos        `json:"todos"`
	Interruptions []Count      `json:"interruptions"`
	Sessions      []SessionRow `json:"sessions"`

	loc *time.Location
}

type Period struct {
	From        time.Time `json:"from"`
	To          time.Time `json:"to"` // effective end: min(to, now)
	Days        float64   `json:"days"`
	TZ          string    `json:"tz"`
	GeneratedAt time.Time `json:"generatedAt"`
}

type Caps struct {
	IntervalMaxSec       int64 `json:"intervalMaxSec"`
	WorkingSilenceMaxSec int64 `json:"workingSilenceMaxSec"`
	LatencyMaxSec        int64 `json:"latencyMaxSec"`
	NextPromptMaxSec     int64 `json:"nextPromptMaxSec"`
	LookbackSec          int64 `json:"lookbackSec"`
}

type Coverage struct {
	Records      int       `json:"records"`         // records inside the period
	LookbackRecs int       `json:"lookbackRecords"` // replayed only for the starting state
	First        time.Time `json:"first,omitzero"`
	Last         time.Time `json:"last,omitzero"`
	Kinds        []Count   `json:"kinds"`
	// Missing names data the statistics could use but the archive has none of
	// in this period (older clients or hooks not installed).
	Missing []string `json:"missing,omitempty"`
}

// Dist summarizes durations in seconds.
type Dist struct {
	Count     int   `json:"count"`
	MedianSec int64 `json:"medianSec"`
	P90Sec    int64 `json:"p90Sec"`
	MaxSec    int64 `json:"maxSec"`
	TotalSec  int64 `json:"totalSec"`
}

type CauseDist struct {
	Cause string `json:"cause"`
	Dist
}

// Latency is the user's response time: from a session entering "waiting" to
// it leaving "waiting", per episode, attributed to the time it started.
type Latency struct {
	All       Dist        `json:"all"`
	ByCause   []CauseDist `json:"byCause"`
	Over4h    int         `json:"over4h"`    // episodes longer than MaxLatency, not in All
	OpenAtEnd int         `json:"openAtEnd"` // still waiting at the end of the period
}

type Parallelism struct {
	AvgWorking           float64   `json:"avgWorking"`           // mean sessions working over the whole period
	AvgWorkingWhenActive float64   `json:"avgWorkingWhenActive"` // mean while ≥1 session works
	MaxWorking           int       `json:"maxWorking"`
	MaxWorkingAt         time.Time `json:"maxWorkingAt,omitzero"`
	MaxWaiting           int       `json:"maxWaiting"`
	MaxWaitingAt         time.Time `json:"maxWaitingAt,omitzero"`
	PeriodSec            int64     `json:"periodSec"`
	ActiveSec            int64     `json:"activeSec"`     // ≥1 session working or waiting
	AnyWorkingSec        int64     `json:"anyWorkingSec"` // ≥1 working
	AnyWaitingSec        int64     `json:"anyWaitingSec"` // ≥1 waiting for the user
	AnyWaitingShare      float64   `json:"anyWaitingShare"`
	// AnyWaitingShareOfActive: of the time anything was going on, how much had
	// at least one session blocked on the user.
	AnyWaitingShareOfActive float64 `json:"anyWaitingShareOfActive"`
	MultiWaitingSec         int64   `json:"multiWaitingSec"`    // ≥2 waiting at once
	MultiWaitingEvents      int     `json:"multiWaitingEvents"` // times the count of waiting sessions rose to 2+
}

// Group aggregates one slice of the data (a day, a machine, a project…).
type Group struct {
	Key           string  `json:"key"`
	Label         string  `json:"label,omitempty"`
	Sessions      int     `json:"sessions"` // sessions with any event or activity in the slice
	Prompts       int     `json:"prompts"`
	Turns         int     `json:"turns"`
	ToolCalls     int     `json:"toolCalls"`
	WorkingSec    int64   `json:"workingSec"`
	WaitingSec    int64   `json:"waitingSec"`
	IdleSec       int64   `json:"idleSec"`
	ErrorSec      int64   `json:"errorSec"`
	WaitShare     float64 `json:"waitShare"` // waiting / (working + waiting)
	Permissions   int     `json:"permissions"`
	Waits         Dist    `json:"waits"` // user response latency (episodes ≤ 4 h)
	Errors        int     `json:"errors"`
	Compactions   int     `json:"compactions"`
	Interruptions int     `json:"interruptions"`

	keys                     map[string]bool
	work, wait, idle, errSec float64
	lat                      samples
}

// TimeGroup is a Group over a span of time (an hour of the day, a weekday, a
// day), with how the sessions overlapped in it.
type TimeGroup struct {
	Group
	SpanSec         int64   `json:"spanSec"` // how much of the period falls into the bucket
	AvgWorking      float64 `json:"avgWorking"`
	ActiveSec       int64   `json:"activeSec"` // ≥1 session working or waiting
	AnyWorkingSec   int64   `json:"anyWorkingSec"`
	AnyWaitingSec   int64   `json:"anyWaitingSec"`
	MultiWaitingSec int64   `json:"multiWaitingSec"`

	span, active, anyWork, anyWait, multiWait float64
}

type Count struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type Permissions struct {
	Total     int           `json:"total"`
	ByTool    []Count       `json:"byTool"`
	ByPattern []PermPattern `json:"byPattern"`
	ByMode    []Count       `json:"byMode"`
	ByProject []Count       `json:"byProject"`
	Denied    []Count       `json:"denied"` // PermissionDenied (auto mode refused), by reason
}

// PermPattern groups similar permission requests: an allowlist candidate.
type PermPattern struct {
	Pattern  string `json:"pattern"`
	Tool     string `json:"tool"`
	Count    int    `json:"count"`
	Sessions int    `json:"sessions"`
	Projects int    `json:"projects"`
	Example  string `json:"example"`
	Waits    Dist   `json:"waits"` // how long the user took to answer these

	keys, projects map[string]bool
	lat            samples
}

type ToolStat struct {
	Tool        string  `json:"tool"`
	Calls       int     `json:"calls"`     // PreToolUse / session.tool.called
	Completed   int     `json:"completed"` // PostToolUse + PostToolUseFailure
	Failures    int     `json:"failures"`
	FailureRate float64 `json:"failureRate"` // failures / max(calls, completed)
	Timed       int     `json:"timed"`
	TotalMs     int64   `json:"totalMs"`
	AvgMs       int64   `json:"avgMs"`
	MaxMs       int64   `json:"maxMs"`
	Permissions int     `json:"permissions"`
}

type Compactions struct {
	Total     int     `json:"total"`
	ByTrigger []Count `json:"byTrigger"`
	ByProject []Count `json:"byProject"`
}

type Todos struct {
	Created   int `json:"created"`
	Completed int `json:"completed"`
}

type SessionRow struct {
	Key            string    `json:"key"`
	Agent          string    `json:"agent"`
	Host           string    `json:"host"`
	HostLabel      string    `json:"hostLabel,omitempty"`
	Project        string    `json:"project"`
	Model          string    `json:"model,omitempty"`
	Title          string    `json:"title,omitempty"`
	Start          time.Time `json:"start"`
	End            time.Time `json:"end"`
	LastState      string    `json:"lastState"`
	WorkingSec     int64     `json:"workingSec"`
	WaitingSec     int64     `json:"waitingSec"`
	IdleSec        int64     `json:"idleSec"`
	Prompts        int       `json:"prompts"`
	Turns          int       `json:"turns"`
	ToolCalls      int       `json:"toolCalls"`
	Permissions    int       `json:"permissions"`
	Waits          int       `json:"waits"`
	LongestWaitSec int64     `json:"longestWaitSec"` // raw, may exceed the 4 h cap
	Errors         int       `json:"errors"`
	Compactions    int       `json:"compactions"`
	Interruptions  int       `json:"interruptions"`

	work, wait, idle float64
}

// Load reads the archive in dir for the period (plus Lookback) and computes.
func Load(dir string, opt Options) (*Stats, error) {
	var recs []status.Record
	err := status.ReadArchive(dir, opt.From.Add(-Lookback), opt.To, func(r status.Record) error {
		recs = append(recs, r)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return Compute(recs, opt), nil
}

// Compute replays the records session by session. Records before opt.From
// only set the starting state.
func Compute(recs []status.Record, opt Options) *Stats {
	c := newCalc(opt)
	order := []string{}
	by := map[string][]status.Record{}
	for _, r := range recs {
		if !r.At.Before(opt.To) || r.Key == "" {
			continue
		}
		if _, ok := by[r.Key]; !ok {
			order = append(order, r.Key)
		}
		by[r.Key] = append(by[r.Key], r)
	}
	for _, k := range order {
		rs := by[k]
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].At.Before(rs[j].At) })
		c.replay(k, rs)
	}
	return c.finish()
}

type calc struct {
	opt        Options
	loc        *time.Location
	from, end  time.Time
	totals     *Group
	hours      [24]*TimeGroup
	weekdays   [7]*TimeGroup
	days       map[string]*TimeGroup
	dims       map[string]map[string]*Group // host, project, agent, model
	byCause    map[string]samples
	over4h     int
	openAtEnd  int
	nextPrompt samples

	kinds                   map[string]int
	records, lookback       int
	first, last             time.Time
	permTool, permMode      map[string]int
	permProject, denied     map[string]int
	patterns                map[string]*PermPattern
	tools                   map[string]*ToolStat
	errors, compactTrig     map[string]int
	compactProj, subagents  map[string]int
	interrupts              map[string]int
	todosCreated, todosDone int
	permTotal, compactTotal int
	sessions                []*sess
	workRuns, waitRuns      []run
	sawPost                 bool
}

type run struct{ a, b time.Time }

func newCalc(opt Options) *calc {
	if opt.Loc == nil {
		opt.Loc = time.Local
	}
	end := opt.To
	if !opt.Now.IsZero() && opt.Now.Before(end) {
		end = opt.Now
	}
	if end.Before(opt.From) {
		end = opt.From
	}
	c := &calc{
		opt: opt, loc: opt.Loc, from: opt.From, end: end,
		totals: &Group{Key: "total"}, days: map[string]*TimeGroup{},
		dims:    map[string]map[string]*Group{"host": {}, "project": {}, "agent": {}, "model": {}},
		byCause: map[string]samples{}, kinds: map[string]int{},
		permTool: map[string]int{}, permMode: map[string]int{}, permProject: map[string]int{}, denied: map[string]int{},
		patterns: map[string]*PermPattern{}, tools: map[string]*ToolStat{},
		errors: map[string]int{}, compactTrig: map[string]int{}, compactProj: map[string]int{},
		subagents: map[string]int{}, interrupts: map[string]int{},
	}
	for h := range c.hours {
		c.hours[h] = &TimeGroup{Group: Group{Key: twoDigits(h)}}
	}
	for d, name := range weekdayNames {
		c.weekdays[d] = &TimeGroup{Group: Group{Key: name}}
	}
	// Every day of the period, even an empty one, so charts keep their shape.
	for d := dayStart(c.from, c.loc); d.Before(c.end); d = d.AddDate(0, 0, 1) {
		k := d.Format("2006-01-02")
		c.days[k] = &TimeGroup{Group: Group{Key: k}}
	}
	// How much of the period falls into each bucket.
	c.eachHour(c.from, c.end, func(at time.Time, d time.Duration) {
		for _, g := range c.timeGroups(at) {
			g.span += d.Seconds()
		}
	})
	return c
}

var weekdayNames = [7]string{"pon", "wt", "śr", "czw", "pt", "sob", "ndz"}

func twoDigits(n int) string { return string([]byte{byte('0' + n/10), byte('0' + n%10)}) }

func dayStart(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
}

// eachHour splits [a, b) at local hour boundaries.
func (c *calc) eachHour(a, b time.Time, fn func(at time.Time, d time.Duration)) {
	for a.Before(b) {
		l := a.In(c.loc)
		next := time.Date(l.Year(), l.Month(), l.Day(), l.Hour()+1, 0, 0, 0, c.loc)
		if !next.After(a) {
			next = a.Add(time.Hour)
		}
		if next.After(b) {
			next = b
		}
		fn(a, next.Sub(a))
		a = next
	}
}

func (c *calc) timeGroups(at time.Time) []*TimeGroup {
	l := at.In(c.loc)
	k := l.Format("2006-01-02")
	day, ok := c.days[k]
	if !ok {
		day = &TimeGroup{Group: Group{Key: k}}
		c.days[k] = day
	}
	return []*TimeGroup{c.hours[l.Hour()], c.weekdays[(int(l.Weekday())+6)%7], day}
}

// groups returns every slice an event of session s at time at belongs to.
func (c *calc) groups(at time.Time, s *sess) []*Group {
	tg := c.timeGroups(at)
	return []*Group{
		c.totals, &tg[0].Group, &tg[1].Group, &tg[2].Group,
		c.dim("host", s.host), c.dim("project", orNone(s.project)),
		c.dim("agent", s.agent), c.dim("model", orUnknown(s.model)),
	}
}

func orNone(p string) string {
	if p == "" {
		return "(bez projektu)"
	}
	return p
}

func orUnknown(m string) string {
	if m == "" {
		return "(nieznany)"
	}
	return m
}

func (c *calc) dim(name, key string) *Group {
	m := c.dims[name]
	g, ok := m[key]
	if !ok {
		g = &Group{Key: key}
		m[key] = g
	}
	return g
}

func (g *Group) touch(key string) {
	if g.keys == nil {
		g.keys = map[string]bool{}
	}
	g.keys[key] = true
}

// sess is the replay state of one session.
type sess struct {
	key, agent, host, project, model, title string
	row                                     SessionRow
	active                                  bool
	runState                                status.State
	runA, runB                              time.Time
	compactPre, compactPost                 []compaction
}

type compaction struct {
	at      time.Time
	trigger string
}

func (s *sess) meta(r status.Record) {
	s.agent, s.host = r.Agent, r.Host
	if r.Project != "" {
		s.project = r.Project
	}
	if r.Model != "" {
		s.model = r.Model
	}
	if r.Title != "" {
		s.title = r.Title
	}
}

func (s *sess) mark(at time.Time) {
	if !s.active || at.Before(s.row.Start) {
		s.row.Start = at
	}
	if !s.active || at.After(s.row.End) {
		s.row.End = at
	}
	s.active = true
}

func (c *calc) replay(key string, recs []status.Record) {
	s := &sess{key: key}
	var (
		cur      status.State
		lastAt   time.Time
		started  bool
		waitOn   bool
		waitAt   time.Time
		waitRec  status.Record
		idleAt   time.Time
		idleOpen bool
		// After a tool completed while the store still said "waiting" (the
		// user approved; Claude Code sends no event for that), further
		// "waiting" records that only repeat the store's state are ignored.
		suppress bool
	)
	endWait := func(at time.Time) {
		if !waitOn {
			return
		}
		waitOn = false
		c.endWait(s, waitRec, waitAt, at)
	}
	for _, r := range recs {
		next := r.State
		if next == "" {
			next = cur
		}
		if suppress {
			if r.State == status.StateWaiting && r.Prev == status.StateWaiting {
				next = cur
			} else {
				suppress = false
			}
		}
		// A tool finished while the session looked "waiting": the user has
		// answered. After a permission prompt the wait ended when the approved
		// tool started (completion minus its duration); after a question or a
		// plan the tool itself was the wait, so it ended now. A duration that
		// covers the whole wait can't be told apart, so then it ends now too.
		// Newer servers already switch the session to "working" on that record;
		// older archives still say "waiting".
		if started && cur == status.StateWaiting && (next == status.StateWaiting || next == status.StateWorking) &&
			(r.Kind == "PostToolUse" || r.Kind == "PostToolUseFailure") {
			t := r.At
			if waitOn && waitCause(waitRec) == CausePermission {
				if s := r.At.Add(-time.Duration(r.DurationMs) * time.Millisecond); s.Sub(waitAt) >= time.Second {
					t = s
				}
			}
			if t.Before(lastAt) {
				t = lastAt
			}
			c.interval(s, cur, lastAt, t, nil)
			endWait(t)
			cur, lastAt = status.StateWorking, t
			next, suppress = status.StateWorking, true
		}
		if started {
			c.interval(s, cur, lastAt, r.At, &r)
		}
		old := cur
		if !started {
			old = r.Prev
		}
		s.meta(r)
		in := !r.At.Before(c.from)
		if in {
			c.record(s, r)
		} else {
			c.lookback++
		}
		if old != next {
			if old == status.StateWaiting {
				endWait(r.At)
			}
			if next == status.StateWaiting {
				waitOn, waitAt, waitRec = true, r.At, r
			}
			turnEnd := old == status.StateWorking &&
				(next == status.StateIdle || next == status.StateError || (next == status.StateWaiting && r.Kind == "Stop"))
			if turnEnd && in {
				for _, g := range c.groups(r.At, s) {
					g.Turns++
				}
				s.row.Turns++
			}
			if next == status.StateError && in {
				c.errors[errorKey(r)]++
				for _, g := range c.groups(r.At, s) {
					g.Errors++
				}
				s.row.Errors++
			}
			if next == status.StateWorking && old == status.StateIdle && idleOpen {
				if gap := r.At.Sub(idleAt); gap < MaxNextPrompt && !idleAt.Before(c.from) {
					c.nextPrompt = append(c.nextPrompt, gap.Seconds())
				}
			}
			idleOpen = false
			if old == status.StateWorking && next == status.StateIdle {
				idleAt, idleOpen = r.At, true
			}
		}
		cur, lastAt, started = next, r.At, true
	}
	if started {
		c.interval(s, cur, lastAt, c.end, nil)
		if waitOn && !waitAt.Before(c.from) && waitAt.Before(c.end) {
			c.openAtEnd++
		}
	}
	s.flushRun(c)
	c.compactions(s)
	if s.active {
		s.row.Key, s.row.Agent, s.row.Host, s.row.Project = s.key, s.agent, s.host, s.project
		s.row.HostLabel = c.opt.HostNames[s.host]
		s.row.Model, s.row.Title, s.row.LastState = s.model, s.title, string(cur)
		c.sessions = append(c.sessions, s)
	}
}

func errorKey(r status.Record) string {
	if r.ErrorKind != "" {
		return r.ErrorKind
	}
	d := strings.TrimSpace(r.Detail)
	if d == "" {
		return "(bez opisu)"
	}
	if rs := []rune(d); len(rs) > 60 {
		d = string(rs[:59]) + "…"
	}
	return d
}

func (c *calc) endWait(s *sess, enter status.Record, at, end time.Time) {
	if at.Before(c.from) || !at.Before(c.end) {
		return
	}
	lat := end.Sub(at)
	if sec := int64(math.Round(lat.Seconds())); sec > s.row.LongestWaitSec {
		s.row.LongestWaitSec = sec
	}
	if lat > MaxLatency {
		c.over4h++
		return
	}
	sec := lat.Seconds()
	for _, g := range c.groups(at, s) {
		g.lat = append(g.lat, sec)
	}
	s.row.Waits++
	cause := waitCause(enter)
	c.byCause[cause] = append(c.byCause[cause], sec)
	if cause == CausePermission {
		_, pat := permTarget(enter.Detail)
		if p := c.patterns[pat]; p != nil {
			p.lat = append(p.lat, sec)
		}
	}
}

// interval counts [a, b) in state st, capped and clipped to the period.
// next is the record that ended the interval (nil: none yet).
func (c *calc) interval(s *sess, st status.State, a, b time.Time, next *status.Record) {
	if st == "" || st == status.StateEnded || !b.After(a) {
		return
	}
	limit := MaxInterval
	if st == status.StateWorking {
		limit = MaxWorkingSilence
		if next != nil && next.DurationMs > 0 {
			if d := time.Duration(next.DurationMs)*time.Millisecond + time.Minute; d > limit {
				limit = min(d, MaxInterval)
			}
		}
	}
	if b.Sub(a) > limit {
		b = a.Add(limit)
	}
	if a.Before(c.from) {
		a = c.from
	}
	if b.After(c.end) {
		b = c.end
	}
	if !b.After(a) {
		return
	}
	activeState := st == status.StateWorking || st == status.StateWaiting || st == status.StateError
	c.eachHour(a, b, func(at time.Time, d time.Duration) {
		sec := d.Seconds()
		for _, g := range c.groups(at, s) {
			switch st {
			case status.StateWorking:
				g.work += sec
			case status.StateWaiting:
				g.wait += sec
			case status.StateIdle:
				g.idle += sec
			case status.StateError:
				g.errSec += sec
			}
			if activeState {
				g.touch(s.key)
			}
		}
	})
	switch st {
	case status.StateWorking:
		s.row.work += b.Sub(a).Seconds()
	case status.StateWaiting:
		s.row.wait += b.Sub(a).Seconds()
	case status.StateIdle:
		s.row.idle += b.Sub(a).Seconds()
	}
	if activeState {
		s.mark(a)
		s.mark(b)
	}
	// Merge back-to-back intervals in the same state for the overlap sweep.
	if st == status.StateWorking || st == status.StateWaiting {
		if s.runState == st && s.runB.Equal(a) {
			s.runB = b
			return
		}
		s.flushRun(c)
		s.runState, s.runA, s.runB = st, a, b
	}
}

func (s *sess) flushRun(c *calc) {
	switch s.runState {
	case status.StateWorking:
		c.workRuns = append(c.workRuns, run{s.runA, s.runB})
	case status.StateWaiting:
		c.waitRuns = append(c.waitRuns, run{s.runA, s.runB})
	}
	s.runState = ""
}

// record counts what a single in-period record says.
func (c *calc) record(s *sess, r status.Record) {
	c.records++
	c.kinds[r.Kind]++
	if c.first.IsZero() || r.At.Before(c.first) {
		c.first = r.At
	}
	if r.At.After(c.last) {
		c.last = r.At
	}
	s.mark(r.At)
	gs := c.groups(r.At, s)
	for _, g := range gs {
		g.touch(s.key)
	}
	add := func(f func(g *Group)) {
		for _, g := range gs {
			f(g)
		}
	}
	if r.Prompt != "" || r.Kind == "UserPromptSubmit" {
		add(func(g *Group) { g.Prompts++ })
		s.row.Prompts++
	}
	if r.Tool != "" {
		add(func(g *Group) { g.ToolCalls++ })
		s.row.ToolCalls++
		c.tool(r.Tool).Calls++
	}
	if r.Permission {
		add(func(g *Group) { g.Permissions++ })
		s.row.Permissions++
		c.permTotal++
		tool, pat := permTarget(r.Detail)
		c.permTool[tool]++
		c.tool(tool).Permissions++
		c.permMode[orUnknown(r.Mode)]++
		c.permProject[orNone(s.project)]++
		p := c.patterns[pat]
		if p == nil {
			p = &PermPattern{Pattern: pat, Tool: tool, Example: r.Detail, keys: map[string]bool{}, projects: map[string]bool{}}
			c.patterns[pat] = p
		}
		p.Count++
		p.keys[s.key] = true
		p.projects[orNone(s.project)] = true
	}
	switch r.Kind {
	case "PostToolUse", "PostToolUseFailure":
		c.sawPost = true
		t := c.tool(firstNonEmpty(r.Reason, r.Tool, "(nieznane)"))
		t.Completed++
		if r.Failed || r.Kind == "PostToolUseFailure" {
			t.Failures++
		}
		if r.DurationMs > 0 {
			t.Timed++
			t.TotalMs += r.DurationMs
			t.MaxMs = max(t.MaxMs, r.DurationMs)
		}
	case "session.tool.failed":
		c.tool(firstNonEmpty(r.Tool, "(nieznane)")).Failures++
	case "PermissionDenied":
		c.denied[firstNonEmpty(r.Reason, "(bez powodu)")]++
	case "PreCompact", "session.compacted":
		s.compactPre = append(s.compactPre, compaction{r.At, r.Reason})
	case "PostCompact":
		s.compactPost = append(s.compactPost, compaction{r.At, r.Reason})
	case "SubagentStart":
		c.subagents[firstNonEmpty(r.Subagent, "(bez typu)")]++
	case "TaskCreated":
		c.todosCreated++
	case "TaskCompleted":
		c.todosDone++
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Detail)), "przerwane") {
		c.interrupts[r.Detail]++
		add(func(g *Group) { g.Interruptions++ })
		s.row.Interruptions++
	}
}

// compactions counts PreCompact, or PostCompact where a session has no
// PreCompact (whichever list is longer), so one compaction counts once.
func (c *calc) compactions(s *sess) {
	list := s.compactPre
	if len(s.compactPost) > len(list) {
		list = s.compactPost
	}
	for _, k := range list {
		c.compactTotal++
		c.compactTrig[firstNonEmpty(k.trigger, "(nieznany)")]++
		c.compactProj[orNone(s.project)]++
		for _, g := range c.groups(k.at, s) {
			g.Compactions++
		}
		s.row.Compactions++
	}
}

func (c *calc) tool(name string) *ToolStat {
	t := c.tools[name]
	if t == nil {
		t = &ToolStat{Tool: name}
		c.tools[name] = t
	}
	return t
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// sweep walks the merged working and waiting intervals of all sessions to
// find how many ran or waited at once.
func (c *calc) sweep(p *Parallelism) {
	type ev struct {
		t      time.Time
		dw, dq int
	}
	var evs []ev
	for _, r := range c.workRuns {
		evs = append(evs, ev{t: r.a, dw: 1}, ev{t: r.b, dw: -1})
	}
	for _, r := range c.waitRuns {
		evs = append(evs, ev{t: r.a, dq: 1}, ev{t: r.b, dq: -1})
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].t.Before(evs[j].t) })
	var nw, nq int
	var prev time.Time
	var anyWork, anyWait, active, multi float64
	for i := 0; i < len(evs); {
		t := evs[i].t
		if i > 0 && t.After(prev) && (nw > 0 || nq > 0) {
			d := t.Sub(prev).Seconds()
			if nw > 0 {
				anyWork += d
			}
			if nq > 0 {
				anyWait += d
			}
			if nq >= 2 {
				multi += d
			}
			active += d
			w, q := nw, nq
			c.eachHour(prev, t, func(at time.Time, d time.Duration) {
				for _, g := range c.timeGroups(at) {
					if w > 0 {
						g.anyWork += d.Seconds()
					}
					if q > 0 {
						g.anyWait += d.Seconds()
					}
					g.active += d.Seconds()
					if q >= 2 {
						g.multiWait += d.Seconds()
					}
				}
			})
		}
		before := nq
		for ; i < len(evs) && evs[i].t.Equal(t); i++ {
			nw += evs[i].dw
			nq += evs[i].dq
		}
		if before < 2 && nq >= 2 {
			p.MultiWaitingEvents++
		}
		if nw > p.MaxWorking {
			p.MaxWorking, p.MaxWorkingAt = nw, t
		}
		if nq > p.MaxWaiting {
			p.MaxWaiting, p.MaxWaitingAt = nq, t
		}
		prev = t
	}
	period := c.end.Sub(c.from).Seconds()
	p.PeriodSec = secs(period)
	p.ActiveSec, p.AnyWorkingSec, p.AnyWaitingSec, p.MultiWaitingSec = secs(active), secs(anyWork), secs(anyWait), secs(multi)
	if period > 0 {
		p.AvgWorking = ratio(c.totals.work, period)
		p.AnyWaitingShare = ratio(anyWait, period)
	}
	if anyWork > 0 {
		p.AvgWorkingWhenActive = ratio(c.totals.work, anyWork)
	}
	if active > 0 {
		p.AnyWaitingShareOfActive = ratio(anyWait, active)
	}
}

func secs(f float64) int64 { return int64(math.Round(f)) }

func ratio(a, b float64) float64 {
	if b <= 0 {
		return 0
	}
	return math.Round(a/b*1000) / 1000
}

func (g *Group) finish() {
	g.Sessions = len(g.keys)
	g.WorkingSec, g.WaitingSec, g.IdleSec, g.ErrorSec = secs(g.work), secs(g.wait), secs(g.idle), secs(g.errSec)
	g.WaitShare = ratio(g.wait, g.work+g.wait)
	g.Waits = g.lat.dist()
}

func (t *TimeGroup) finish() {
	t.Group.finish()
	t.SpanSec, t.ActiveSec, t.AnyWorkingSec, t.AnyWaitingSec, t.MultiWaitingSec = secs(t.span), secs(t.active), secs(t.anyWork), secs(t.anyWait), secs(t.multiWait)
	t.AvgWorking = ratio(t.work, t.span)
}

func (c *calc) finish() *Stats {
	st := &Stats{
		Period: Period{
			From: c.from, To: c.end, Days: math.Round(c.end.Sub(c.from).Hours()/24*100) / 100,
			TZ: zoneName(c.loc, c.end), GeneratedAt: c.opt.Now,
		},
		Caps: Caps{
			IntervalMaxSec: secs(MaxInterval.Seconds()), WorkingSilenceMaxSec: secs(MaxWorkingSilence.Seconds()),
			LatencyMaxSec: secs(MaxLatency.Seconds()), NextPromptMaxSec: secs(MaxNextPrompt.Seconds()),
			LookbackSec: secs(Lookback.Seconds()),
		},
	}
	st.loc = c.loc
	c.sweep(&st.Parallelism)

	c.totals.finish()
	st.Totals = *c.totals
	for _, h := range c.hours {
		h.finish()
		st.ByHour = append(st.ByHour, *h)
	}
	for _, d := range c.weekdays {
		d.finish()
		st.ByWeekday = append(st.ByWeekday, *d)
	}
	for _, d := range c.days {
		d.finish()
		st.ByDay = append(st.ByDay, *d)
	}
	sort.Slice(st.ByDay, func(i, j int) bool { return st.ByDay[i].Key < st.ByDay[j].Key })
	dimList := func(name string) []Group {
		out := []Group{}
		for _, g := range c.dims[name] {
			g.finish()
			if name == "host" {
				g.Label = c.opt.HostNames[g.Key]
			}
			out = append(out, *g)
		}
		sort.Slice(out, func(i, j int) bool {
			a, b := out[i].WorkingSec+out[i].WaitingSec, out[j].WorkingSec+out[j].WaitingSec
			if a != b {
				return a > b
			}
			return out[i].Key < out[j].Key
		})
		return out
	}
	st.ByHost, st.ByProject, st.ByAgent, st.ByModel = dimList("host"), dimList("project"), dimList("agent"), dimList("model")

	var all samples
	for _, cause := range causes {
		s := c.byCause[cause]
		all = append(all, s...)
		st.Latency.ByCause = append(st.Latency.ByCause, CauseDist{Cause: cause, Dist: s.dist()})
	}
	st.Latency.All = all.dist()
	st.Latency.Over4h, st.Latency.OpenAtEnd = c.over4h, c.openAtEnd
	st.NextPrompt = c.nextPrompt.dist()

	st.Permissions = Permissions{
		Total: c.permTotal, ByTool: counts(c.permTool), ByMode: counts(c.permMode),
		ByProject: counts(c.permProject), Denied: counts(c.denied), ByPattern: []PermPattern{},
	}
	for _, p := range c.patterns {
		p.Sessions, p.Projects, p.Waits = len(p.keys), len(p.projects), p.lat.dist()
		st.Permissions.ByPattern = append(st.Permissions.ByPattern, *p)
	}
	sort.Slice(st.Permissions.ByPattern, func(i, j int) bool {
		a, b := st.Permissions.ByPattern[i], st.Permissions.ByPattern[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Pattern < b.Pattern
	})

	st.Tools = []ToolStat{}
	for _, t := range c.tools {
		if n := max(t.Calls, t.Completed); n > 0 {
			t.FailureRate = ratio(float64(t.Failures), float64(n))
		}
		if t.Timed > 0 {
			t.AvgMs = t.TotalMs / int64(t.Timed)
		}
		st.Tools = append(st.Tools, *t)
	}
	sort.Slice(st.Tools, func(i, j int) bool {
		a, b := st.Tools[i], st.Tools[j]
		if a.Calls != b.Calls {
			return a.Calls > b.Calls
		}
		return a.Tool < b.Tool
	})

	st.Errors = counts(c.errors)
	st.Compactions = Compactions{Total: c.compactTotal, ByTrigger: counts(c.compactTrig), ByProject: counts(c.compactProj)}
	st.Subagents = counts(c.subagents)
	st.Todos = Todos{Created: c.todosCreated, Completed: c.todosDone}
	st.Interruptions = counts(c.interrupts)

	st.Coverage = Coverage{Records: c.records, LookbackRecs: c.lookback, First: c.first, Last: c.last, Kinds: counts(c.kinds)}
	if c.records > 0 {
		if !c.sawPost {
			st.Coverage.Missing = append(st.Coverage.Missing, "PostToolUse/PostToolUseFailure (czasy i błędy narzędzi)")
		}
		if c.kinds["PreToolUse"] == 0 && c.kinds["session.tool.called"] == 0 {
			st.Coverage.Missing = append(st.Coverage.Missing, "PreToolUse (liczba wywołań narzędzi)")
		}
		if c.kinds["PreCompact"]+c.kinds["PostCompact"] == 0 {
			st.Coverage.Missing = append(st.Coverage.Missing, "PreCompact/PostCompact (kompaktowania)")
		}
		if c.kinds["SubagentStart"] == 0 {
			st.Coverage.Missing = append(st.Coverage.Missing, "SubagentStart (podagenci)")
		}
		if c.kinds["TaskCreated"]+c.kinds["TaskCompleted"] == 0 {
			st.Coverage.Missing = append(st.Coverage.Missing, "TaskCreated/TaskCompleted (listy zadań)")
		}
	}

	st.Sessions = []SessionRow{}
	for _, s := range c.sessions {
		r := s.row
		r.WorkingSec, r.WaitingSec, r.IdleSec = secs(r.work), secs(r.wait), secs(r.idle)
		st.Sessions = append(st.Sessions, r)
	}
	sort.Slice(st.Sessions, func(i, j int) bool { return st.Sessions[i].Start.Before(st.Sessions[j].Start) })
	return st
}

func counts(m map[string]int) []Count {
	out := make([]Count, 0, len(m))
	for k, n := range m {
		out = append(out, Count{k, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	return out
}

type samples []float64

func (s samples) dist() Dist {
	if len(s) == 0 {
		return Dist{}
	}
	v := append([]float64(nil), s...)
	sort.Float64s(v)
	var total float64
	for _, x := range v {
		total += x
	}
	n := len(v)
	med := v[n/2]
	if n%2 == 0 {
		med = (v[n/2-1] + v[n/2]) / 2
	}
	p90 := v[int(math.Ceil(0.9*float64(n)))-1]
	return Dist{Count: n, MedianSec: secs(med), P90Sec: secs(p90), MaxSec: secs(v[n-1]), TotalSec: secs(total)}
}

// zoneName names the time zone with its offset, e.g. "Europe/Warsaw (CET, UTC+01:00)".
func zoneName(loc *time.Location, at time.Time) string {
	abbr, off := at.In(loc).Zone()
	sign := '+'
	if off < 0 {
		sign, off = '-', -off
	}
	return fmt.Sprintf("%s (%s, UTC%c%02d:%02d)", loc.String(), abbr, sign, off/3600, off%3600/60)
}
