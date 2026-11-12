package utils

import (
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		value  string
		want   time.Duration
		wantOK bool
	}{
		{name: "absent", value: "", want: 0, wantOK: false},
		{name: "blank", value: "   ", want: 0, wantOK: false},
		{name: "seconds", value: "120", want: 2 * time.Minute, wantOK: true},
		{name: "seconds with padding", value: " 7 ", want: 7 * time.Second, wantOK: true},
		{name: "zero seconds", value: "0", want: 0, wantOK: true},
		{name: "negative seconds are malformed", value: "-5", want: 0, wantOK: false},
		{name: "fraction is malformed", value: "1.5", want: 0, wantOK: false},
		{name: "garbage", value: "soon", want: 0, wantOK: false},
		{
			name:   "http date in the future",
			value:  now.Add(90 * time.Second).Format(http.TimeFormat),
			want:   90 * time.Second,
			wantOK: true,
		},
		{
			name:   "http date already past is zero, not negative",
			value:  now.Add(-time.Hour).Format(http.TimeFormat),
			want:   0,
			wantOK: true,
		},
		{
			name:   "absurd second count is clamped rather than overflowed",
			value:  "99999999999999999",
			want:   maxRetryAfter,
			wantOK: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseRetryAfter(tc.value, now)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Errorf("duration = %s, want %s", got, tc.want)
			}
			if got < 0 {
				t.Errorf("duration %s is negative", got)
			}
		})
	}
}

func TestBoundRetryAfter(t *testing.T) {
	cases := []struct {
		name                  string
		after, floor, ceiling time.Duration
		want                  time.Duration
	}{
		{
			name:    "between the bounds passes through",
			after:   20 * time.Second,
			floor:   time.Second,
			ceiling: time.Minute,
			want:    20 * time.Second,
		},
		{
			name:    "below the floor is the floor",
			after:   0,
			floor:   3 * time.Second,
			ceiling: time.Minute,
			want:    3 * time.Second,
		},
		{
			name:    "above the ceiling is the ceiling",
			after:   time.Hour,
			floor:   time.Second,
			ceiling: time.Minute,
			want:    time.Minute,
		},
		{
			name:    "ceiling under the floor yields the floor",
			after:   time.Hour,
			floor:   2 * time.Minute,
			ceiling: time.Minute,
			want:    2 * time.Minute,
		},
		{name: "no ceiling", after: time.Hour, floor: time.Second, ceiling: 0, want: time.Hour},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BoundRetryAfter(tc.after, tc.floor, tc.ceiling); got != tc.want {
				t.Errorf(
					"BoundRetryAfter(%s, %s, %s) = %s, want %s",
					tc.after,
					tc.floor,
					tc.ceiling,
					got,
					tc.want,
				)
			}
		})
	}
}
