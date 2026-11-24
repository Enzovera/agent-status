package stats

import (
	"sort"
	"strings"
	"testing"

	"agent-status/internal/status"
)

func cost(total, delta float64) recOpt {
	return func(r *status.Record) { r.CostUsd, r.CostDelta, r.ContextPct = total, delta, 42 }
}

func TestCostFromStatusline(t *testing.T) {
	plain := compute(t, fixture())

	sl := status.KindStatusline
	recs := append(fixture(),
		rec(m(-70), kD, sl, Q, Q, project("p1"), cost(1, 1)), // before the period: not counted
		rec(m(12), kA, sl, W, W, project("p1"), cost(0.3, 0.3)),
		rec(m(35), kA, sl, I, I, project("p1"), cost(0.5, 0.2)),
		// Inside B's silent "working" stretch: must not lift the 30 min cap.
		rec(m(182-60), kB, sl, W, W, project("p2"), cost(2, 2)),
	)
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].At.Before(recs[j].At) })
	st := compute(t, recs)

	if st.Totals.CostUsd != 2.5 {
		t.Errorf("total cost = %v, want 2.5", st.Totals.CostUsd)
	}
	tot, ptot := st.Totals, plain.Totals
	if tot.WorkingSec != ptot.WorkingSec || tot.WaitingSec != ptot.WaitingSec || tot.IdleSec != ptot.IdleSec ||
		tot.Sessions != ptot.Sessions || tot.Turns != ptot.Turns || tot.Prompts != ptot.Prompts {
		t.Errorf("statusline records changed the totals:\n%+v\n%+v", tot, ptot)
	}
	byKey := func(gs []Group) map[string]float64 {
		out := map[string]float64{}
		for _, g := range gs {
			out[g.Key] = g.CostUsd
		}
		return out
	}
	if p := byKey(st.ByProject); p["p1"] != 0.5 || p["p2"] != 2 {
		t.Errorf("cost by project = %v", p)
	}
	if h := byKey(st.ByHost); h["h1"] != 0.5 || h["h2"] != 2 {
		t.Errorf("cost by host = %v", h)
	}
	var days float64
	for _, d := range st.ByDay {
		days += d.CostUsd
	}
	if days != 2.5 {
		t.Errorf("cost by day sums to %v", days)
	}
	var b strings.Builder
	if err := Report(&b, st); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "Razem: $2.50 – z 2 z 2 sesji Claude Code") || !strings.Contains(b.String(), "p2 $2.00, p1 $0.50") {
		t.Errorf("report has no cost section:\n%s", b.String())
	}
	for _, s := range st.Sessions {
		want := map[string]float64{kA: 0.5, kB: 2}[s.Key]
		if s.CostUsd != want {
			t.Errorf("session %s cost = %v, want %v", s.Key, s.CostUsd, want)
		}
	}
}
