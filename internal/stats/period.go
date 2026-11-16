package stats

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultDays = 7
	MaxDays     = 400
)

// ParsePeriod reads the period from query parameters:
//
//	days=N           the last N×24 h up to now (default 7)
//	from=…&to=…      RFC3339 timestamps or local dates (YYYY-MM-DD); a date in
//	                 "to" includes that whole day; a missing "to" means now,
//	                 a missing "from" means 7 days before "to"
func ParsePeriod(q url.Values, now time.Time, loc *time.Location) (from, to time.Time, err error) {
	if loc == nil {
		loc = time.Local
	}
	fs, ts := strings.TrimSpace(q.Get("from")), strings.TrimSpace(q.Get("to"))
	if fs == "" && ts == "" {
		days := DefaultDays
		if d := strings.TrimSpace(q.Get("days")); d != "" {
			days, err = strconv.Atoi(d)
			if err != nil || days < 1 || days > MaxDays {
				return time.Time{}, time.Time{}, fmt.Errorf("days must be 1..%d", MaxDays)
			}
		}
		return now.Add(-time.Duration(days) * 24 * time.Hour), now, nil
	}
	to = now
	if ts != "" {
		if to, err = parseTime(ts, loc, true); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("to: %w", err)
		}
	}
	from = to.Add(-DefaultDays * 24 * time.Hour)
	if fs != "" {
		if from, err = parseTime(fs, loc, false); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("from: %w", err)
		}
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, errors.New("from must be before to")
	}
	if to.Sub(from) > MaxDays*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("period longer than %d days", MaxDays)
	}
	return from, to, nil
}

func parseTime(s string, loc *time.Location, endOfDay bool) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	d, err := time.ParseInLocation("2006-01-02", s, loc)
	if err != nil {
		return time.Time{}, errors.New("want RFC3339 or YYYY-MM-DD")
	}
	if endOfDay {
		d = d.AddDate(0, 0, 1)
	}
	return d, nil
}
