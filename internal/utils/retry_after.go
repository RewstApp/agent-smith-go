package utils

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ParseRetryAfter reads a Retry-After header value (RFC 9110 section 10.2.3):
// either a non-negative integer number of seconds or an HTTP-date. The second
// result is false for an absent or malformed value, which a caller treats as
// no header at all. A date already in the past yields zero, which a caller
// then floors at its own schedule (see BoundRetryAfter), so a stale date
// cannot turn into an immediate retry.
func ParseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}

	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 0 {
			return 0, false
		}
		// Clamp before converting: a huge second count would overflow the
		// nanosecond Duration into a negative value.
		if seconds > int64(maxRetryAfter/time.Second) {
			return maxRetryAfter, true
		}
		return time.Duration(seconds) * time.Second, true
	}

	if at, err := http.ParseTime(value); err == nil {
		d := at.Sub(now)
		if d < 0 {
			d = 0
		}
		if d > maxRetryAfter {
			d = maxRetryAfter
		}
		return d, true
	}

	return 0, false
}

// maxRetryAfter is the largest value ParseRetryAfter returns before a caller
// applies its own ceiling. It only exists so an absurd header cannot overflow
// the Duration; every caller bounds the wait far below it.
const maxRetryAfter = 365 * 24 * time.Hour

// BoundRetryAfter returns the wait a retry schedule honours for a Retry-After
// of after: never below floor, the delay the schedule would have waited
// anyway, so the header can only ever delay a retry, and never above ceiling,
// the schedule's own cap, so a wrong or hostile header cannot park a worker
// for hours. A ceiling below the floor yields the floor.
func BoundRetryAfter(after, floor, ceiling time.Duration) time.Duration {
	if ceiling > 0 && after > ceiling {
		after = ceiling
	}
	if after < floor {
		return floor
	}
	return after
}
