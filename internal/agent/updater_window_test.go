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

// windowFixture serves a newer release and its binary, counts both, and
// wires an updater whose clock, live settings and window re-read cadence the
// test controls (sc-132993).
type windowFixture struct {
	updater   *defaultUpdater
	activity  *fakeActivity
	log       *safeBuffer
	checks    atomic.Int32
	downloads atomic.Int32
	launched  atomic.Int32
	wake      chan struct{}

	mu       sync.Mutex
	now      time.Time
	device   Device
	statuses []UpdateStatus
}

// safeBuffer is a log sink the updater goroutine writes while the test reads.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newWindowFixture(t *testing.T, window string, now time.Time) *windowFixture {
	t.Helper()
	f := &windowFixture{
		activity: &fakeActivity{},
		log:      &safeBuffer{},
		wake:     make(chan struct{}, 1),
		now:      now,
	}
	binary := []byte("fake binary")
	download := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.downloads.Add(1)
		_, _ = w.Write(binary)
	}))
	t.Cleanup(download.Close)
	release := releaseWithDigest("v99.0.0", download.URL, binary)
	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.checks.Add(1)
		_ = json.NewEncoder(w).Encode(release)
	}))
	t.Cleanup(releases.Close)

	device := newTestDevice()
	device.UpdateWindow = window
	f.device = *device
	runCmd := func(string, []string) error {
		f.launched.Add(1)
		f.activity.record("launch")
		return nil
	}
	f.updater = newTestUpdater(t, device, releases.URL, runCmd)
	f.updater.logger = hclog.New(&hclog.LoggerOptions{Output: f.log, Level: hclog.Debug})
	f.updater.activity = f.activity
	f.updater.idlePoll = time.Millisecond
	f.updater.idleMaxWait = 2 * time.Second
	f.updater.holdDuration = time.Minute
	f.updater.liveDevice = func() Device {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.device
	}
	f.updater.now = func() time.Time {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.now
	}
	f.updater.windowRecheck = 5 * time.Millisecond
	f.updater.wake = f.wake
	f.updater.reportStatus = func(s UpdateStatus) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.statuses = append(f.statuses, s)
	}
	return f
}

func (f *windowFixture) setNow(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t
}

func (f *windowFixture) setWindow(w string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.device.UpdateWindow = w
}

func (f *windowFixture) reported() []UpdateStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]UpdateStatus(nil), f.statuses...)
}

// run starts Run and returns a channel with its result.
func (f *windowFixture) run(ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() { done <- f.updater.Run(ctx) }()
	return done
}

// waitForLog waits for a log line, failing the test after a few seconds.
func (f *windowFixture) waitForLog(t *testing.T, needle string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(f.log.String(), needle) {
		if time.Now().After(deadline) {
			t.Fatalf("log never carried %q:\n%s", needle, f.log.String())
		}
		time.Sleep(2 * time.Millisecond) // sleep-ok: poll interval inside a bounded wait
	}
}

func waitResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
		return nil
	}
}

func utcAt(h, m, s int) time.Time { return time.Date(2026, 11, 20, h, m, s, 0, time.UTC) }

// Outside the window a newer release is reported and waited for: nothing is
// downloaded and no command is held until the window opens; then the release
// is checked again and the existing hold runs before the helper launches.
func TestRun_WaitsForTheUpdateWindowThenUpdates(t *testing.T) {
	f := newWindowFixture(t, "02:00-03:00", utcAt(1, 59, 0))
	done := f.run(context.Background())

	f.waitForLog(t, "Update available; waiting for the update window")
	time.Sleep(30 * time.Millisecond) // sleep-ok: several re-reads of a closed window
	if f.downloads.Load() != 0 || f.launched.Load() != 0 || len(f.activity.all()) != 0 {
		t.Fatalf("outside the window: %d download(s), %d launch(es), events %v",
			f.downloads.Load(), f.launched.Load(), f.activity.all())
	}
	out := f.log.String()
	for _, want := range []string{
		"version=v99.0.0", "window=02:00-03:00", "opens_at=2026-11-20T02:00:00Z", "in=1m0s",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the waiting line lacks %q:\n%s", want, out)
		}
	}

	f.setNow(utcAt(2, 0, 5))
	if err := waitResult(t, done); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(f.log.String(), "waiting for the update window"); n != 1 {
		t.Errorf("the wait was logged %d times, want once", n)
	}
	if f.checks.Load() != 2 {
		t.Errorf("checked %d times, want once before the wait and once when the window opened",
			f.checks.Load())
	}
	if f.downloads.Load() != 1 || f.launched.Load() != 1 {
		t.Errorf(
			"%d download(s), %d launch(es), want one each", f.downloads.Load(), f.launched.Load(),
		)
	}
	events := f.activity.all()
	if len(events) != 3 || !strings.HasPrefix(events[0], "hold ") || events[2] != "launch" {
		t.Errorf("the command hold did not run inside the window before the launch: %v", events)
	}
	statuses := f.reported()
	if len(statuses) != 2 {
		t.Fatalf("reported %+v, want the wait then the update", statuses)
	}
	if !statuses[0].Available || statuses[0].Version != "v99.0.0" ||
		!statuses[0].WindowOpensAt.Equal(utcAt(2, 0, 0)) {
		t.Errorf("the wait reported %+v", statuses[0])
	}
	if !statuses[1].Available || !statuses[1].WindowOpensAt.IsZero() {
		t.Errorf("the update reported %+v", statuses[1])
	}
}

// A twin change to the window ends the wait at once and the release is
// checked against the window now in effect.
func TestRun_SettingsChangeEndsTheWindowWait(t *testing.T) {
	f := newWindowFixture(t, "02:00-03:00", utcAt(12, 0, 0))
	f.updater.windowRecheck = time.Hour // only the wake can end this wait
	done := f.run(context.Background())
	f.waitForLog(t, "waiting for the update window")

	f.setWindow("11:00-13:00")
	f.wake <- struct{}{}
	if err := waitResult(t, done); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.log.String(), "Update settings changed while waiting for the window") {
		t.Errorf("the wake was not logged:\n%s", f.log.String())
	}
	if f.checks.Load() != 2 || f.launched.Load() != 1 {
		t.Errorf("%d check(s), %d launch(es) after the window moved over now",
			f.checks.Load(), f.launched.Load())
	}
}

// Without a wake the wait still sees a changed window at its next re-read:
// the sleep is never longer than the re-read cadence.
func TestRun_WindowWaitRereadsTheSettingsWithinItsCadence(t *testing.T) {
	f := newWindowFixture(t, "02:00-03:00", utcAt(12, 0, 0))
	f.updater.wake = nil
	done := f.run(context.Background())
	f.waitForLog(t, "waiting for the update window")

	f.setWindow("")
	if err := waitResult(t, done); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.log.String(), "Update window changed while waiting; checking again") {
		t.Errorf("the re-read did not notice the window change:\n%s", f.log.String())
	}
	if f.launched.Load() != 1 {
		t.Errorf("%d launch(es) once the window was cleared to any time", f.launched.Load())
	}
}

// A stop during the wait returns promptly with nothing downloaded or held.
func TestRun_StopDuringTheWindowWait(t *testing.T) {
	f := newWindowFixture(t, "02:00-03:00", utcAt(12, 0, 0))
	f.updater.windowRecheck = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := f.run(ctx)
	f.waitForLog(t, "waiting for the update window")

	cancel()
	err := waitResult(t, done)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("want the cancellation, got %v", err)
	}
	if !strings.Contains(f.log.String(), "Update window wait abandoned: updater stopping") {
		t.Errorf("the stop was not logged:\n%s", f.log.String())
	}
	if f.downloads.Load() != 0 || len(f.activity.all()) != 0 {
		t.Errorf(
			"a stopped wait downloaded %d time(s) and held %v",
			f.downloads.Load(), f.activity.all(),
		)
	}
}

// A wake delivered while nothing was waiting does not end the next wait.
func TestRun_StaleWakeDoesNotEndTheWait(t *testing.T) {
	f := newWindowFixture(t, "02:00-03:00", utcAt(12, 0, 0))
	f.updater.windowRecheck = time.Hour
	f.wake <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	done := f.run(ctx)
	f.waitForLog(t, "waiting for the update window")
	time.Sleep(30 * time.Millisecond) // sleep-ok: room for a stale wake to misfire
	if f.checks.Load() != 1 {
		t.Errorf("a stale wake ended the wait: %d checks", f.checks.Load())
	}
	cancel()
	_ = waitResult(t, done)
}

// Inside the window, or with no window, nothing changes: no wait, one check.
func TestRun_InsideTheWindowUpdatesAtOnce(t *testing.T) {
	for _, window := range []string{"", "11:00-13:00", "22:00-13:00"} {
		f := newWindowFixture(t, window, utcAt(12, 0, 0))
		if err := waitResult(t, f.run(context.Background())); err != nil {
			t.Fatalf("%q: %v", window, err)
		}
		if strings.Contains(f.log.String(), "waiting for the update window") ||
			f.checks.Load() != 1 || f.launched.Load() != 1 {
			t.Errorf("%q: %d check(s), %d launch(es):\n%s",
				window, f.checks.Load(), f.launched.Load(), f.log.String())
		}
		if s := f.reported(); len(s) != 1 || !s[0].Available || s[0].Version != "v99.0.0" {
			t.Errorf("%q: reported %+v", window, s)
		}
	}
}
