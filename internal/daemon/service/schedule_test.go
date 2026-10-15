package service

import (
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		panic(err)
	}
	return t
}

func TestScheduleInterval(t *testing.T) {
	s := Schedule{Every: 6 * time.Hour}
	now := at("2026-10-09 10:00")
	if got := s.First(now); !got.Equal(at("2026-10-09 16:00")) {
		t.Error(got)
	}
	if got := s.After(at("2026-10-09 16:00"), at("2026-10-09 16:00")); !got.Equal(at("2026-10-09 22:00")) {
		t.Error(got)
	}
	// Overslept: the next run is an interval after now, not in the past.
	if got := s.After(at("2026-10-09 16:00"), at("2026-10-10 09:00")); !got.Equal(at("2026-10-10 15:00")) {
		t.Error(got)
	}
	// At is ignored under a day.
	if got := (Schedule{Every: time.Hour, At: "02:00"}).First(now); !got.Equal(at("2026-10-09 11:00")) {
		t.Error(got)
	}
}

func TestScheduleAligned(t *testing.T) {
	s := Schedule{Every: day, At: "02:00"}
	if got := s.First(at("2026-10-09 10:00")); !got.Equal(at("2026-10-10 02:00")) {
		t.Error(got)
	}
	if got := s.First(at("2026-10-09 01:00")); !got.Equal(at("2026-10-09 02:00")) {
		t.Error(got)
	}
	if got := s.After(at("2026-10-10 02:00"), at("2026-10-10 02:00")); !got.Equal(at("2026-10-11 02:00")) {
		t.Error(got)
	}
	if got := (Schedule{Every: 2 * day, At: "02:00"}).After(at("2026-10-10 02:00"), at("2026-10-10 02:30")); !got.Equal(at("2026-10-12 02:00")) {
		t.Error(got)
	}
	if got := (Schedule{At: "23:30"}).After(at("2026-10-10 23:30"), at("2026-10-13 08:00")); !got.Equal(at("2026-10-13 23:30")) {
		t.Error(got)
	}
	if got := nextAt(at("2026-10-09 10:00"), "bad"); !got.Equal(at("2026-10-10 00:00")) {
		t.Error(got)
	}
}

func TestScheduleDisabled(t *testing.T) {
	var s Schedule
	if s.Enabled() || !s.First(time.Now()).IsZero() || !s.After(time.Now(), time.Now()).IsZero() {
		t.Error("disabled schedule runs")
	}
}
