package agent

import (
	"fmt"
	"strings"
	"time"
)

// UpdateWindow is the time of day auto-updates may restart the agent
// (sc-132993): from Start up to, not including, End, in the endpoint's local
// time, both in minutes since midnight. A window whose End is earlier than its
// Start crosses midnight ("22:00-02:00"). The zero value - what an empty
// update_window means - allows any time.
type UpdateWindow struct {
	start, end int
	set        bool
}

// ParseUpdateWindow reads "HH:MM-HH:MM" (24-hour clock, two digits each).
// Empty, or only spaces, is the zero window: any time. A window that starts
// and ends at the same minute is refused rather than read as "never" or
// "always", since either reading would surprise someone; empty is how to say
// "any time".
func ParseUpdateWindow(s string) (UpdateWindow, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return UpdateWindow{}, nil
	}
	from, to, ok := strings.Cut(s, "-")
	if !ok {
		return UpdateWindow{}, invalidUpdateWindow(s, "want HH:MM-HH:MM")
	}
	start, err := parseClock(strings.TrimSpace(from))
	if err != nil {
		return UpdateWindow{}, invalidUpdateWindow(s, err.Error())
	}
	end, err := parseClock(strings.TrimSpace(to))
	if err != nil {
		return UpdateWindow{}, invalidUpdateWindow(s, err.Error())
	}
	if start == end {
		return UpdateWindow{}, invalidUpdateWindow(
			s, "the window starts and ends at the same minute; leave it empty for any time",
		)
	}
	return UpdateWindow{start: start, end: end, set: true}, nil
}

// ValidateUpdateWindow accepts what ParseUpdateWindow accepts, for the flag,
// the twin and the saved value.
func ValidateUpdateWindow(s string) error {
	_, err := ParseUpdateWindow(s)
	return err
}

func invalidUpdateWindow(s, why string) error {
	return fmt.Errorf("invalid update-window %q: %s", s, why)
}

// parseClock reads "HH:MM" into minutes since midnight.
func parseClock(s string) (int, error) {
	if len(s) != 5 || s[2] != ':' {
		return 0, fmt.Errorf("%q is not HH:MM", s)
	}
	for i, c := range s {
		if i != 2 && (c < '0' || c > '9') {
			return 0, fmt.Errorf("%q is not HH:MM", s)
		}
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	m := int(s[3]-'0')*10 + int(s[4]-'0')
	if h > 23 || m > 59 {
		return 0, fmt.Errorf("%q is not a time of day", s)
	}
	return h*60 + m, nil
}

// IsZero reports whether the window allows any time.
func (w UpdateWindow) IsZero() bool { return !w.set }

// String renders the window as it is written, "" for any time.
func (w UpdateWindow) String() string {
	if !w.set {
		return ""
	}
	return fmt.Sprintf("%02d:%02d-%02d:%02d", w.start/60, w.start%60, w.end/60, w.end%60)
}

// Contains reports whether t, read in its own location, is inside the window.
// The zero window contains every time.
func (w UpdateWindow) Contains(t time.Time) bool {
	if !w.set {
		return true
	}
	m := t.Hour()*60 + t.Minute()
	if w.start < w.end {
		return m >= w.start && m < w.end
	}
	return m >= w.start || m < w.end
}

// NextOpen is the first moment at or after t inside the window: t itself when
// it already is, otherwise the next time the window starts, in t's location,
// at most a day away. A day that skips the start minute (a daylight-saving
// jump) yields the normalised wall time time.Date gives, which is still at or
// after the start.
func (w UpdateWindow) NextOpen(t time.Time) time.Time {
	if w.Contains(t) {
		return t
	}
	y, mo, d := t.Date()
	open := time.Date(y, mo, d, w.start/60, w.start%60, 0, 0, t.Location())
	if !open.After(t) {
		open = time.Date(y, mo, d+1, w.start/60, w.start%60, 0, 0, t.Location())
	}
	return open
}
