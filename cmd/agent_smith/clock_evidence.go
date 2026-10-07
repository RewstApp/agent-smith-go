package main

import (
	"os"
	"sync"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
)

// clockHeartbeatInterval is how often the observed wall clock is persisted
// while the agent runs. The gap between the last heartbeat and the process's
// death is time the next process cannot see; it is at most this long, and it
// only ever makes the next process more conservative.
const clockHeartbeatInterval = time.Minute

// clockEvidence bounds every age-based decision in the journal and the spool
// by time the agent has actually observed, rather than by whatever the wall
// clock says at the moment of the decision (sc-119838).
//
// The problem it solves: expiry used to compare a record's timestamp with
// time.Now() at replay, and replay happens at the start of a connection cycle,
// which on a fresh boot is exactly when the clock is least trustworthy. A
// device without a battery-backed RTC (or a VM restored from a snapshot) boots
// with its clock in the past and steps forward by hours or days when NTP
// syncs: if the agent started after the step, every command queued seconds
// before the reboot looked a day old and was reported expired and discarded.
// A clock stepped backward made stale records look fresh, so a tombstone
// could be pruned early and a redelivered command run twice.
//
// The evidence is a heartbeat file holding the latest wall clock this agent
// has seen, rewritten at most once a minute while it runs. Two readings come
// from it:
//
//   - baseline: the value loaded at startup - the last clock the *previous*
//     process saw. A record that process left behind has aged, as far as
//     anyone can prove, by baseline minus its timestamp; the downtime between
//     that heartbeat and now is unobserved and is not counted. A forward step
//     at boot therefore cannot expire anything the agent did not see age. The
//     price is that a command which aged past its limit entirely while the
//     device was off is replayed rather than expired - the agent has no
//     evidence that the time passed - and that is the side this design errs
//     on, because discarding a command is the silent, confident outcome the
//     journal exists to prevent.
//   - observed: the latest clock seen by *this* process, which advances as it
//     runs and is what records this process created are aged against.
//
// A record was left by a previous process when it is stamped before the
// clock this process first read (loadedAt); anything stamped at or after that
// was created by this process and is aged against observed. A forward step
// at boot moves loadedAt forward with it, so every record from before the
// reboot is correctly classed as previous and measured against the baseline.
//
// When the clock is found earlier than the evidence - at startup earlier than
// the baseline, or while running earlier than observed - time has gone
// backwards. The call that notices it reports once through onStepBack, the
// cycle that is in progress expires and prunes nothing from the previous
// timeline, and the evidence is reset to the new clock so the agent carries on
// in the new timeline; records from the old one are "in the future" relative
// to it and simply never age until the clock catches up. An absent heartbeat
// (first run after installing this version) is treated the same way: nothing
// from before is expired in that cycle, and the baseline is established for
// the next.
type clockEvidence struct {
	path       string
	fs         utils.FileSystem
	now        func() time.Time
	interval   time.Duration
	onStepBack func(last, now time.Time)

	mu       sync.Mutex
	loaded   bool
	loadedAt time.Time
	baseline time.Time
	observed time.Time
	written  time.Time
	// suspended is set when the evidence had to be reset (absent heartbeat or a
	// backward step) and cleared when the cycle that found it ends; while set,
	// no record from a previous process is reported aged.
	suspended bool
	stepped   bool
}

func newClockEvidence(
	path string,
	fs utils.FileSystem,
	now func() time.Time,
	onStepBack func(last, now time.Time),
) *clockEvidence {
	return &clockEvidence{
		path:       path,
		fs:         fs,
		now:        now,
		interval:   clockHeartbeatInterval,
		onStepBack: onStepBack,
	}
}

// observe records the current clock as seen, loading the heartbeat on the
// first call, detecting a backward step, and persisting the heartbeat at
// most once per interval. The journal and the spool call it on every
// operation, and the service calls it on a timer so an idle agent still
// records time passing.
func (c *clockEvidence) observe() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.observeLocked()
}

func (c *clockEvidence) observeLocked() {
	now := c.now()
	if !c.loaded {
		c.loaded = true
		c.loadedAt = now
		if last, ok := c.readHeartbeat(); ok {
			c.baseline = last
			c.observed = last
			c.written = last
			if now.Before(last) {
				c.stepBackLocked(last, now)
				return
			}
		} else {
			// First run: no evidence of anything having aged.
			c.suspended = true
			c.baseline = now
			c.observed = now
			c.persistLocked(now)
			return
		}
	}
	if now.Before(c.observed) {
		c.stepBackLocked(c.observed, now)
		return
	}
	c.observed = now
	if now.Sub(c.written) >= c.interval {
		c.persistLocked(now)
	}
}

// stepBackLocked resets the evidence to the new clock, reporting the step
// once per occurrence.
func (c *clockEvidence) stepBackLocked(last, now time.Time) {
	c.suspended = true
	if !c.stepped {
		c.stepped = true
		if c.onStepBack != nil {
			c.onStepBack(last, now)
		}
	}
	c.baseline = now
	c.observed = now
	c.persistLocked(now)
}

func (c *clockEvidence) persistLocked(t time.Time) {
	c.written = t
	// Best effort: the heartbeat is advisory. A failed write leaves the
	// previous value, which only makes the next process more conservative.
	_ = utils.WriteFileAtomic(
		c.fs,
		c.path,
		[]byte(t.UTC().Format(time.RFC3339Nano)),
		utils.DefaultFileMod,
	)
}

func (c *clockEvidence) readHeartbeat() (time.Time, bool) {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, string(data))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// previousTimelineLocked reports whether a record stamped t was left by a
// previous process: it is stamped before the clock this process first read.
func (c *clockEvidence) previousTimelineLocked(t time.Time) bool {
	return t.Before(c.loadedAt)
}

// aged reports whether a record stamped t has, on the evidence, aged past
// maxAge: against the baseline for a record a previous process left behind
// (and never while the evidence is suspended), against observed for one this
// process created.
func (c *clockEvidence) aged(t time.Time, maxAge time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loaded {
		c.observeLocked()
	}
	if c.previousTimelineLocked(t) {
		if c.suspended {
			return false
		}
		return c.baseline.Sub(t) >= maxAge
	}
	return c.observed.Sub(t) >= maxAge
}

// endCycle clears a suspension: the cycle that found the evidence missing or
// the clock stepped back has finished without expiring anything, and the
// evidence established since then is trusted from here on.
func (c *clockEvidence) endCycle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.suspended = false
	c.stepped = false
}
