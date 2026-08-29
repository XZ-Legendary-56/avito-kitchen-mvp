package catalog

import "time"

// ScheduleEntry is one weekday's opening window (PROMPT.md 9:
// venue_schedules). MVP simplification, matching the
// venue_schedules_order CHECK in migrations/00003_venues.sql: one interval
// per weekday, no shift crossing midnight.
type ScheduleEntry struct {
	Weekday  time.Weekday
	OpensAt  time.Duration // time-of-day offset from midnight
	ClosesAt time.Duration
}

// IsOpenAt reports whether entries contains a window covering at, using
// at's weekday and time-of-day in at's own location. The window is
// half-open [OpensAt, ClosesAt): opening time counts as open, closing time
// does not.
func IsOpenAt(entries []ScheduleEntry, at time.Time) bool {
	weekday := at.Weekday()
	sinceMidnight := timeOfDay(at)
	for _, e := range entries {
		if e.Weekday != weekday {
			continue
		}
		if sinceMidnight >= e.OpensAt && sinceMidnight < e.ClosesAt {
			return true
		}
	}
	return false
}

func timeOfDay(t time.Time) time.Duration {
	h, m, s := t.Clock()
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second
}
