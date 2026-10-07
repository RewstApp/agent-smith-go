package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/interpreter"
	"github.com/RewstApp/agent-smith-go/internal/utils"
)

const (
	// defaultJournalMaxPending bounds how many received-but-unfinished commands
	// the journal holds. Azure IoT Hub's own per-device queue holds 50 and the
	// agent's in-memory queue defaults to 100, so a thousand is far above any
	// legitimate backlog; past it the journal is treated as full and new
	// messages are accepted without durability (see receiveMessage) rather
	// than refused, so a runaway backlog can never fill the disk or stop
	// commands from executing.
	defaultJournalMaxPending = 1000

	// defaultJournalMaxAge is how long a journaled command stays eligible for
	// replay. It matches IoT Hub's default cloud-to-device message TTL of one
	// hour: a command the agent *saw* age past that before it died is reported
	// as expired rather than executed. Age is measured on the clock evidence
	// (clockEvidence), not the wall clock at replay: time the device spent
	// powered off is unobserved and does not count, so a clock stepped forward
	// at boot cannot expire a command queued seconds before the reboot
	// (sc-119838). The same age bounds how long completion tombstones are kept
	// for de-duplication - IoT Hub cannot redeliver a message past its TTL, so
	// a tombstone older than that guards against nothing.
	defaultJournalMaxAge = time.Hour

	// journalHeartbeatFile is the clock evidence's heartbeat inside the journal
	// directory.
	journalHeartbeatFile = "heartbeat"

	journalPendingSuffix = ".json"
	journalDoneSuffix    = ".done"
)

// errJournalFull is returned by put when the pending count has reached the
// bound. The caller degrades to in-memory delivery; it does not refuse the
// command.
var errJournalFull = errors.New("command journal is full")

// journalEntry is the on-disk record of one received command. Payload is the
// raw MQTT message so a replay processes exactly what the broker delivered.
// StartedAt is zero until a worker begins executing the command; a non-zero
// StartedAt found on replay means the process died mid-execution.
type journalEntry struct {
	Key        string    `json:"key"`
	Payload    []byte    `json:"payload"`
	ReceivedAt time.Time `json:"received_at"`
	StartedAt  time.Time `json:"started_at,omitempty"`
}

// journalTombstone marks a completed command so a redelivery of the same
// message - IoT Hub retransmits an unacknowledged QoS 1 PUBLISH on reconnect,
// and the PUBACK for a message the agent has since finished can be lost in
// transit - is recognised and acknowledged without being executed again.
type journalTombstone struct {
	Key         string    `json:"key"`
	CompletedAt time.Time `json:"completed_at"`
}

// commandJournal is the durable record of commands the agent has accepted from
// the broker but not yet finished. It exists because the broker's
// acknowledgement cannot be held until execution completes: Azure IoT Hub
// re-enqueues an unacknowledged cloud-to-device message after a fixed,
// unchangeable one-minute lock and does not count that toward the delivery
// limit, so a command that runs longer than a minute (the default timeout is
// thirty) would be redelivered every minute for as long as it ran. The
// alternative Microsoft documents for long-running work is to persist the
// task, acknowledge, and report progress separately - which is what this is.
//
// The message is written here before it is acknowledged, so the
// acknowledgement means "durably accepted", not merely "buffered in memory".
// A process that dies with commands queued or executing leaves their entries
// behind, and the next start replays them: never-started entries execute,
// started ones are reported back as interrupted rather than run again.
//
// One JSON file per pending entry and one tombstone per completed one, named
// by a hash of the key so a redelivery can be looked up directly and so
// post_id's colons never reach a Windows filename. Writes go through
// utils.WriteFileAtomic like the postback spool's: temp-then-rename with an
// fsync of the file and of the directory, so an entry that has been written -
// and therefore a command that has been acknowledged - survives a power loss,
// not only a process crash (sc-119835).
type commandJournal struct {
	mu         sync.Mutex
	dir        string
	maxPending int
	maxAge     time.Duration
	now        func() time.Time
	fs         utils.FileSystem
	// clock is the evidence every age decision is made on; see clockEvidence.
	clock *clockEvidence
	// onClockStepBack is reported once per backward clock step the journal
	// notices; the service logs it at Error.
	onClockStepBack func(last, now time.Time)
}

func newCommandJournal(dir string, maxPending int, maxAge time.Duration) *commandJournal {
	if maxPending <= 0 {
		maxPending = defaultJournalMaxPending
	}
	if maxAge <= 0 {
		maxAge = defaultJournalMaxAge
	}
	j := &commandJournal{
		dir:        dir,
		maxPending: maxPending,
		maxAge:     maxAge,
		now:        time.Now,
		fs:         utils.NewFileSystem(),
	}
	j.clock = newClockEvidence(
		filepath.Join(dir, journalHeartbeatFile),
		j.fs,
		func() time.Time { return j.now() },
		func(last, now time.Time) {
			if j.onClockStepBack != nil {
				j.onClockStepBack(last, now)
			}
		},
	)
	return j
}

// observeClock records the current clock as evidence; every operation calls
// it so the heartbeat tracks the agent's activity, and the service's timer
// covers idle stretches.
func (j *commandJournal) observeClock() {
	j.clock.observe()
}

// journalKey derives the de-duplication key for a received payload: the
// message's post_id when it has one (that is what the engine correlates the
// result by, so two deliveries with the same post_id are the same command),
// otherwise a digest of the payload. A payload that does not parse still gets
// a key so it is journaled and its parse failure is logged on replay exactly
// as it would have been on first receipt.
func journalKey(payload []byte) string {
	var msg interpreter.Message
	if err := msg.Parse(payload); err == nil && msg.PostId != "" {
		return "post:" + msg.PostId
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (j *commandJournal) base(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16])
}

func (j *commandJournal) pendingPath(key string) string {
	return filepath.Join(j.dir, j.base(key)+journalPendingSuffix)
}

func (j *commandJournal) donePath(key string) string {
	return filepath.Join(j.dir, j.base(key)+journalDoneSuffix)
}

// put records a received payload under key. It reports existed=true, without
// writing, when the key is already pending or already completed - a
// redelivery the caller should acknowledge and not execute. It returns
// errJournalFull when the pending bound is reached.
func (j *commandJournal) put(key string, payload []byte) (existed bool, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if err := os.MkdirAll(j.dir, utils.DefaultDirMod); err != nil {
		return false, fmt.Errorf("create journal dir: %w", err)
	}
	j.observeClock()

	// Prune first: an expired tombstone must not de-duplicate a redelivery the
	// broker could no longer be making anyway, and a stale one would otherwise
	// block a legitimately new command with a reused key for as long as it sat
	// there.
	j.pruneLocked()

	if fileExists(j.pendingPath(key)) || fileExists(j.donePath(key)) {
		return true, nil
	}

	if j.countPendingLocked() >= j.maxPending {
		return false, errJournalFull
	}

	entry := journalEntry{Key: key, Payload: payload, ReceivedAt: j.now()}
	return false, j.writeLocked(j.pendingPath(key), entry)
}

// markStarted records that a worker has begun executing the command. From
// this point a replay reports the command as interrupted instead of running
// it again.
func (j *commandJournal) markStarted(key string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	entry, err := j.readLocked(j.pendingPath(key))
	if err != nil {
		return err
	}
	j.observeClock()
	entry.StartedAt = j.now()
	return j.writeLocked(j.pendingPath(key), entry)
}

// complete removes the pending entry and leaves a tombstone so a late
// redelivery is recognised.
func (j *commandJournal) complete(key string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	j.observeClock()
	tombstone := journalTombstone{Key: key, CompletedAt: j.now()}
	if err := j.writeLocked(j.donePath(key), tombstone); err != nil {
		return err
	}
	if err := os.Remove(j.pendingPath(key)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove journal entry: %w", err)
	}
	return nil
}

// discard removes a pending entry without a tombstone, for an entry that
// expired before it could run.
func (j *commandJournal) discard(key string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := os.Remove(j.pendingPath(key)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove journal entry: %w", err)
	}
	return nil
}

// pending returns the entries left behind by a previous run, oldest first,
// split into those still within maxAge (to replay) and those past it (to
// report as expired and discard). Expired tombstones are pruned on the way.
// Age is decided on the clock evidence: an entry is expired only if the
// previous process saw it age past maxAge before it died, never because the
// clock at replay says so (see clockEvidence). A cycle in which the evidence
// was missing or the clock had stepped back expires nothing.
func (j *commandJournal) pending() (fresh, expired []journalEntry, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	j.observeClock()
	defer j.clock.endCycle()
	j.pruneLocked()

	names, err := j.listLocked(journalPendingSuffix)
	if err != nil {
		return nil, nil, err
	}
	for _, name := range names {
		entry, readErr := j.readLocked(filepath.Join(j.dir, name))
		if readErr != nil {
			// An unreadable entry cannot be replayed and would otherwise be
			// re-reported on every start; remove it so the journal stays clean.
			_ = os.Remove(filepath.Join(j.dir, name))
			continue
		}
		if j.clock.aged(entry.ReceivedAt, j.maxAge) {
			expired = append(expired, entry)
		} else {
			fresh = append(fresh, entry)
		}
	}
	byReceipt := func(es []journalEntry) {
		sort.Slice(es, func(a, b int) bool { return es[a].ReceivedAt.Before(es[b].ReceivedAt) })
	}
	byReceipt(fresh)
	byReceipt(expired)
	return fresh, expired, nil
}

func (j *commandJournal) countPendingLocked() int {
	names, err := j.listLocked(journalPendingSuffix)
	if err != nil {
		return 0
	}
	return len(names)
}

// pruneLocked removes tombstones that have aged past maxAge on the clock
// evidence. Pending entries are never pruned here - an expired pending entry
// is reported by pending() so its loss is visible, not silently reclaimed.
func (j *commandJournal) pruneLocked() {
	names, err := j.listLocked(journalDoneSuffix)
	if err != nil {
		return
	}
	for _, name := range names {
		path := filepath.Join(j.dir, name)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		var t journalTombstone
		if json.Unmarshal(data, &t) != nil || j.clock.aged(t.CompletedAt, j.maxAge) {
			_ = os.Remove(path)
		}
	}
}

func (j *commandJournal) listLocked(suffix string) ([]string, error) {
	entries, err := os.ReadDir(j.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read journal dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), suffix) {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

func (j *commandJournal) readLocked(path string) (journalEntry, error) {
	var entry journalEntry
	data, err := os.ReadFile(path)
	if err != nil {
		return entry, fmt.Errorf("read journal entry: %w", err)
	}
	if err := json.Unmarshal(data, &entry); err != nil {
		return entry, fmt.Errorf("decode journal entry: %w", err)
	}
	return entry, nil
}

// writeLocked commits v to path atomically and durably: a crash between the
// temp write and the rename leaves either the previous file or nothing, never
// a torn entry, and the record is fsynced before the call returns so the
// PUBACK that follows a put still means "on disk". markStarted, complete (the
// tombstone) and put all come through here.
func (j *commandJournal) writeLocked(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal journal record: %w", err)
	}
	if err := utils.WriteFileAtomic(j.fs, path, data, utils.DefaultFileMod); err != nil {
		return fmt.Errorf("commit journal record: %w", err)
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
