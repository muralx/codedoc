package service

import (
	"time"
)

// Schedule says when a recurring job runs: every Every, and for daily or
// longer intervals with At set ("HH:MM", local time), at that time of
// day. A zero Every with At set means daily; zero Every without At
// disables the job.
type Schedule struct {
	Every time.Duration
	At    string
}

// day is the interval from which At applies.
const day = 24 * time.Hour

// Enabled reports whether the schedule ever runs.
func (s Schedule) Enabled() bool { return s.Every > 0 || s.At != "" }

// every returns the interval, daily when only At is set.
func (s Schedule) every() time.Duration {
	if s.Every <= 0 {
		return day
	}
	return s.Every
}

// aligned reports whether runs are pinned to the time of day At.
func (s Schedule) aligned() bool { return s.At != "" && s.every() >= day }

// First returns the first run after the daemon starts at now: the next
// At after now when aligned, else now plus the interval. Zero when
// disabled.
func (s Schedule) First(now time.Time) time.Time {
	switch {
	case !s.Enabled():
		return time.Time{}
	case s.aligned():
		return nextAt(now, s.At)
	default:
		return now.Add(s.every())
	}
}

// After returns the run following one due at due that started at now:
// one interval after due (at the At time of that day when aligned), and
// never at or before now, so a daemon that slept skips the runs it
// missed instead of running them back to back.
func (s Schedule) After(due, now time.Time) time.Time {
	if !s.Enabled() {
		return time.Time{}
	}
	if s.aligned() {
		// A minute early, so a run due exactly every interval at the
		// same time of day finds that time.
		return nextAt(later(due.Add(s.every()-time.Minute), now), s.At)
	}
	next := due.Add(s.every())
	if !next.After(now) {
		next = now.Add(s.every())
	}
	return next
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// nextAt returns the first time of day hhmm strictly after t, in t's
// location. hhmm is validated by the config loader; a malformed one
// counts as midnight.
func nextAt(t time.Time, hhmm string) time.Time {
	var h, m int
	if c, err := time.Parse("15:04", hhmm); err == nil {
		h, m = c.Hour(), c.Minute()
	}
	at := time.Date(t.Year(), t.Month(), t.Day(), h, m, 0, 0, t.Location())
	for !at.After(t) {
		at = time.Date(at.Year(), at.Month(), at.Day()+1, h, m, 0, 0, t.Location())
	}
	return at
}
