package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
)

// fakeActivity is a CommandActivity whose executing count the test controls
// and which records every hold and release, in order, relative to the helper
// launch.
type fakeActivity struct {
	executing atomic.Int32
	polls     atomic.Int32
	mu        sync.Mutex
	events    []string
}

func (a *fakeActivity) Executing() int {
	a.polls.Add(1)
	return int(a.executing.Load())
}

func (a *fakeActivity) HoldNewCommands(d time.Duration) { a.record("hold " + d.String()) }
func (a *fakeActivity) ReleaseNewCommands()             { a.record("release") }

func (a *fakeActivity) record(e string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
}

func (a *fakeActivity) all() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.events...)
}

type deferFixture struct {
	updater  *defaultUpdater
	activity *fakeActivity
	log      *bytes.Buffer
	launched atomic.Int32
	// launchErr, when set, is what the helper launch returns.
	launchErr error
}

// newDeferFixture serves a newer release and its binary, and wires an updater
// whose deferral cadence is milliseconds rather than the production 30 s.
func newDeferFixture(t *testing.T) *deferFixture {
	t.Helper()
	f := &deferFixture{activity: &fakeActivity{}, log: &bytes.Buffer{}}
	binary := []byte("fake binary")
	download := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(binary)
	}))
	t.Cleanup(download.Close)
	release := releaseWithDigest("v99.0.0", download.URL, binary)
	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(release)
	}))
	t.Cleanup(releases.Close)

	runCmd := func(string, []string) error {
		f.launched.Add(1)
		f.activity.record("launch")
		return f.launchErr
	}
	f.updater = newTestUpdater(t, newTestDevice(), releases.URL, runCmd)
	f.updater.logger = hclog.New(&hclog.LoggerOptions{Output: f.log, Level: hclog.Debug})
	f.updater.activity = f.activity
	f.updater.idlePoll = time.Millisecond
	f.updater.idleMaxWait = 2 * time.Second
	f.updater.holdDuration = time.Minute
	return f
}

func TestRun_DefersWhileCommandsExecuteThenProceeds(t *testing.T) {
	f := newDeferFixture(t)
	f.activity.executing.Store(2)
	// The command finishes after the updater has polled a few times.
	go func() {
		for f.activity.polls.Load() < 4 {
			time.Sleep(time.Millisecond) // sleep-ok: poll interval
		}
		f.activity.executing.Store(0)
	}()

	if err := f.updater.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.launched.Load() != 1 {
		t.Fatalf("helper launched %d times, want 1", f.launched.Load())
	}
	out := f.log.String()
	for _, want := range []string{
		"Deferring update until in-flight commands finish",
		"executing=2",
		"In-flight commands finished; proceeding with update",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "although commands are still executing") {
		t.Error("the deadline warning fired although the commands finished in time")
	}
	// Held before the wait, re-armed to the short window, then launched.
	events := f.activity.all()
	if len(events) != 3 || !strings.HasPrefix(events[0], "hold ") ||
		events[1] != "hold 1m0s" || events[2] != "launch" {
		t.Errorf("events = %v, want [hold <wait+hold>, hold 1m0s, launch]", events)
	}
}

func TestRun_ProceedsAtTheDeadlineWithAWarning(t *testing.T) {
	f := newDeferFixture(t)
	f.activity.executing.Store(1) // never finishes
	f.updater.idleMaxWait = 30 * time.Millisecond

	start := time.Now()
	if err := f.updater.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.launched.Load() != 1 {
		t.Fatalf("helper launched %d times, want 1", f.launched.Load())
	}
	if waited := time.Since(start); waited < 30*time.Millisecond {
		t.Errorf("proceeded after %s, before the %s ceiling", waited, 30*time.Millisecond)
	}
	out := f.log.String()
	if !strings.Contains(out, "[WARN]") ||
		!strings.Contains(out, "Proceeding with update although commands are still executing") {
		t.Errorf("log lacks the Warn line:\n%s", out)
	}
}

func TestRun_HoldsNewCommandsBeforeLaunchingEvenWhenIdle(t *testing.T) {
	f := newDeferFixture(t)
	if err := f.updater.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := f.activity.all()
	if len(events) < 2 || !strings.HasPrefix(events[0], "hold ") ||
		events[len(events)-1] != "launch" {
		t.Errorf("events = %v, want a hold before the launch", events)
	}
	if strings.Contains(f.log.String(), "Deferring update") {
		t.Error("an idle agent was deferred")
	}
}

func TestRun_ReleasesTheHoldWhenTheHelperFailsToStart(t *testing.T) {
	f := newDeferFixture(t)
	f.launchErr = errors.New("exec format error")

	if err := f.updater.Run(context.Background()); err == nil {
		t.Fatal("expected the launch failure")
	}
	events := f.activity.all()
	if len(events) == 0 || events[len(events)-1] != "release" {
		t.Errorf("events = %v, want the hold released after the failed launch", events)
	}
}

func TestRun_DeferralAbandonedWhenTheUpdaterStops(t *testing.T) {
	f := newDeferFixture(t)
	f.activity.executing.Store(1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for f.activity.polls.Load() < 3 {
			time.Sleep(time.Millisecond) // sleep-ok: poll interval
		}
		cancel()
	}()

	err := f.updater.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if f.launched.Load() != 0 {
		t.Error("the helper was launched although the updater was stopped mid-wait")
	}
	events := f.activity.all()
	joined := strings.Join(events, ",")
	if !strings.Contains(joined, "release") || strings.Contains(joined, "launch") {
		t.Errorf("events = %v, want the hold released and no launch", events)
	}
	if !strings.Contains(f.log.String(), "Update deferral abandoned: updater stopping") {
		t.Errorf("log lacks the abandonment line:\n%s", f.log.String())
	}
}

// The production defaults: 30 s polls, a ceiling of the per-command timeout
// plus a minute, a ten-minute post-launch hold. Pinned so a change is a
// deliberate one.
func TestUpdateDeferralDefaults(t *testing.T) {
	if updateIdlePollInterval != 30*time.Second {
		t.Errorf("poll interval = %s", updateIdlePollInterval)
	}
	if updateIdleWaitMargin != time.Minute {
		t.Errorf("wait margin = %s", updateIdleWaitMargin)
	}
	if updateHoldDuration != 10*time.Minute {
		t.Errorf("hold = %s", updateHoldDuration)
	}
	d := newTestDevice()
	u := newTestUpdater(t, d, "", nil)
	u.activity = &fakeActivity{}
	// With no override the ceiling derives from the device's command timeout.
	if got, want := d.ResolvedCommandTimeout()+updateIdleWaitMargin, DefaultCommandTimeout+time.Minute; got != want {
		t.Errorf("derived ceiling = %s, want %s", got, want)
	}
}
