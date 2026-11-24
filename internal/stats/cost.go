package stats

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"agent-status/internal/status"
)

// cost counts a statusline record (Claude Code's own cost estimate, see
// status.ApplyStatusline). Each record carries how much the session's cost
// grew since its previous one, so the cost of any period is the sum of the
// deltas in it, attributed to the hour, day, machine, project and model of
// the record. Statusline records say nothing about the session's state and
// don't count as activity.
func (c *calc) cost(s *sess, r status.Record) {
	s.meta(r)
	if r.At.Before(c.from) {
		c.lookback++
		return
	}
	c.records++
	c.kinds[r.Kind]++
	s.hasCost = true
	for _, g := range c.groups(r.At, s) {
		g.cost += r.CostDelta
	}
	s.row.cost += r.CostDelta
	s.row.ContextPct = r.ContextPct
}

func roundCost(f float64) float64 { return math.Round(f*1e4) / 1e4 }

// costReport adds the cost section to the Markdown report (only when the
// archive has statusline records with a cost).
func costReport(p func(string, ...any), st *Stats) {
	if st.Totals.CostUsd <= 0 {
		return
	}
	p("## Koszt (szacunek Claude Code)\n\n")
	p("Koszt sesji Claude Code według ich własnego szacunku (`cost.total_cost_usd` ze statusLine, ceny katalogowe; może różnić się od rachunku). Liczony z przyrostów w okresie; sesje OpenCode i maszyny bez skryptu statusLine nie są wliczone.\n\n")
	cv := st.Coverage
	p("Razem: $%.2f – z %d z %d sesji Claude Code (pozostałe nie wysłały statusLine; ich koszt jest nieznany, nie zerowy).\n\n", st.Totals.CostUsd, cv.ClaudeWithCost, cv.ClaudeSessions)
	list := func(title string, gs []Group) {
		gs = append([]Group(nil), gs...)
		sort.SliceStable(gs, func(i, j int) bool { return gs[i].CostUsd > gs[j].CostUsd })
		var parts []string
		for _, g := range gs {
			if g.CostUsd <= 0 || len(parts) == 15 {
				break
			}
			parts = append(parts, fmt.Sprintf("%s $%.2f", firstNonEmpty(g.Label, g.Key), g.CostUsd))
		}
		if len(parts) > 0 {
			p("- %s: %s.\n", title, strings.Join(parts, ", "))
		}
	}
	list("Według projektu", st.ByProject)
	list("Według maszyny", st.ByHost)
	list("Według modelu", st.ByModel)
	days := make([]Group, 0, len(st.ByDay))
	for _, d := range st.ByDay {
		days = append(days, d.Group)
	}
	list("Według dnia", days)
	p("\n")
}
