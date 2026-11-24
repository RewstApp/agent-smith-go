package agent

import (
	"strings"
	"testing"
	"time"
)

func TestParseUpdateWindow(t *testing.T) {
	valid := map[string]string{
		"":               "",
		"   ":            "",
		"02:00-04:00":    "02:00-04:00",
		" 22:30 - 01:15": "22:30-01:15",
		"00:00-23:59":    "00:00-23:59",
	}
	for in, want := range valid {
		w, err := ParseUpdateWindow(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if w.String() != want || w.IsZero() != (want == "") {
			t.Errorf("%q parsed as %q (zero %t), want %q", in, w.String(), w.IsZero(), want)
		}
	}
	invalid := map[string]string{
		"02:00":        "want HH:MM-HH:MM",
		"2:00-4:00":    "is not HH:MM",
		"02:00-24:00":  "is not a time of day",
		"02:60-03:00":  "is not a time of day",
		"0a:00-03:00":  "is not HH:MM",
		"03:00-03:00":  "leave it empty for any time",
		"02:00-03:00-": "is not HH:MM",
	}
	for in, want := range invalid {
		_, err := ParseUpdateWindow(in)
		if err == nil || !strings.Contains(err.Error(), want) ||
			!strings.Contains(err.Error(), "invalid update-window") {
			t.Errorf("%q: want an error containing %q, got %v", in, want, err)
		}
		if ValidateUpdateWindow(in) == nil {
			t.Errorf("ValidateUpdateWindow accepted %q", in)
		}
	}
}

func at(h, m int) time.Time { return time.Date(2026, 11, 20, h, m, 30, 0, time.UTC) }

func TestUpdateWindow_Contains(t *testing.T) {
	day, _ := ParseUpdateWindow("02:00-04:00")
	night, _ := ParseUpdateWindow("22:00-02:00")
	cases := []struct {
		w    UpdateWindow
		t    time.Time
		want bool
	}{
		{day, at(1, 59), false},
		{day, at(2, 0), true},
		{day, at(3, 59), true},
		{day, at(4, 0), false},
		{night, at(21, 59), false},
		{night, at(22, 0), true},
		{night, at(23, 59), true},
		{night, at(0, 0), true},
		{night, at(1, 59), true},
		{night, at(2, 0), false},
		{UpdateWindow{}, at(12, 0), true},
	}
	for _, tc := range cases {
		if got := tc.w.Contains(tc.t); got != tc.want {
			t.Errorf("%q contains %s = %t, want %t", tc.w, tc.t.Format("15:04"), got, tc.want)
		}
	}
}

func TestUpdateWindow_NextOpen(t *testing.T) {
	day, _ := ParseUpdateWindow("02:00-04:00")
	night, _ := ParseUpdateWindow("22:00-02:00")
	cases := []struct {
		w    UpdateWindow
		t    time.Time
		want time.Time
	}{
		// Before the start today: today's start.
		{day, at(1, 0), time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC)},
		// Inside: now.
		{day, at(3, 0), at(3, 0)},
		// After the end: tomorrow's start, less than a day away.
		{day, at(4, 0), time.Date(2026, 11, 21, 2, 0, 0, 0, time.UTC)},
		// A window crossing midnight, closed in the afternoon: tonight.
		{night, at(15, 0), time.Date(2026, 11, 20, 22, 0, 0, 0, time.UTC)},
		{night, at(0, 30), at(0, 30)},
	}
	for _, tc := range cases {
		got := tc.w.NextOpen(tc.t)
		if !got.Equal(tc.want) {
			t.Errorf("%q next open after %s = %s, want %s", tc.w, tc.t, got, tc.want)
		}
		if got.Sub(tc.t) >= 24*time.Hour || got.Before(tc.t) {
			t.Errorf("%q next open after %s = %s is not within the next day", tc.w, tc.t, got)
		}
	}
}

// The opening is read in the time's own location: the endpoint's local time.
func TestUpdateWindow_ReadsTheLocalClock(t *testing.T) {
	w, _ := ParseUpdateWindow("02:00-04:00")
	manila := time.FixedZone("UTC+8", 8*3600)
	// 18:30 UTC is 02:30 in UTC+8.
	now := time.Date(2026, 11, 20, 18, 30, 0, 0, time.UTC).In(manila)
	if !w.Contains(now) {
		t.Errorf("02:30 local was read as outside 02:00-04:00")
	}
}

func TestDevice_ResolvedUpdateWindow(t *testing.T) {
	if !(Device{}).ResolvedUpdateWindow().IsZero() {
		t.Error("an empty update_window is not any time")
	}
	if !(Device{UpdateWindow: "nonsense"}).ResolvedUpdateWindow().IsZero() {
		t.Error("an invalid update_window is not read as any time")
	}
	got := (Device{UpdateWindow: "01:00-02:00"}).ResolvedUpdateWindow().String()
	if got != "01:00-02:00" {
		t.Errorf("resolved %q", got)
	}
}
