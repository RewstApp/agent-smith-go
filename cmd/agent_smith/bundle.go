package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/mqtt"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
)

// The diagnostic bundle (sc-129690): `--diagnostic --bundle` writes one zip
// that answers a support case's first round of questions - the configuration
// with its secrets redacted, the log tails, what the command journal and the
// postback spool hold (never a command, a result or a line of output), the
// service manager's view, the host, the agent version and the console output
// of every check.

const (
	// bundleLogTailMax is the most of each log file the bundle keeps.
	bundleLogTailMax = 2 << 20
	// bundleMaxBytes is the ceiling on the zip; log tails are trimmed to fit.
	bundleMaxBytes = 25 << 20
	// bundleRedacted replaces every secret value in every entry.
	bundleRedacted = "***"
	// bundleWithheld stands in for a running command's last output line in
	// health.json and the Fleet Health section: the bundle carries no output.
	bundleWithheld = "(withheld from the bundle)"
	// bundleMinSecretLength keeps a short value from being scrubbed out of
	// unrelated text; every secret the agent holds is far longer.
	bundleMinSecretLength = 6
	// bundleStdoutPath is the --bundle-path that streams the zip to stdout,
	// with every console section on stderr (sc-132995), so
	// `--diagnostic --bundle --bundle-path - | curl -T - <url>` works from an
	// RMM one-liner.
	bundleStdoutPath = "-"
)

// bundleEnv is what writing a bundle depends on, injectable for tests.
type bundleEnv struct {
	root     string
	fallback func(string) []agentInfo
	dialer   tlsDialer
	now      func() time.Time
	// serviceStatus runs a service-manager query; nil runs it for real.
	serviceStatus func(ctx context.Context, argv []string) string
	// stdout receives the zip for --bundle-path -; nil is the process's.
	stdout io.Writer
	// handOver gives the written file to the user who asked for it
	// (sc-132995) and describes what it did; nil is the platform's
	// defaultBundleHandOver. It runs after the atomic rename, so a failure
	// can never leave a partial file with the wrong owner.
	handOver func(path string) (string, error)
}

// bundleReport is where the bundle went, for the closing console line.
type bundleReport struct {
	// Path is the file written, or bundleStdoutPath.
	Path string
	Size int
	// HandOver describes how the file was handed to the invoking user, or
	// why it was not; empty when nothing had to be done.
	HandOver string
}

func (r bundleReport) String() string {
	if r.Path == bundleStdoutPath {
		return fmt.Sprintf("stdout (%d bytes)", r.Size)
	}
	if r.HandOver == "" {
		return fmt.Sprintf("%s (%d bytes)", r.Path, r.Size)
	}
	return fmt.Sprintf("%s (%d bytes, %s)", r.Path, r.Size, r.HandOver)
}

// runDiagnosticBundle writes the bundle for the agent the parameters name (or
// the only one installed) and reports where it went. The sections print to
// diagOut as they are captured; with --bundle-path - the caller has pointed
// diagOut at stderr, since the zip itself goes to stdout.
func runDiagnosticBundle(
	ctx context.Context,
	params *diagnosticContext,
	env bundleEnv,
) (bundleReport, error) {
	if env.now == nil {
		env.now = time.Now
	}
	if env.serviceStatus == nil {
		env.serviceStatus = runServiceManagerCommand
	}
	if env.stdout == nil {
		env.stdout = os.Stdout
	}
	if env.handOver == nil {
		env.handOver = defaultBundleHandOver
	}
	target, err := bundleTarget(params.OrgId, env)
	if err != nil {
		return bundleReport{}, err
	}
	dataDir := target.DataDir
	if _, err := os.ReadDir(dataDir); err != nil {
		return bundleReport{}, fmt.Errorf(
			"cannot read the agent's data directory %s (run --diagnostic --bundle elevated): %w",
			dataDir, err,
		)
	}
	configBytes, err := os.ReadFile(target.ConfigFile)
	if err != nil {
		return bundleReport{}, fmt.Errorf("cannot read the agent's configuration: %w", err)
	}
	redacted, secrets, err := redactConfig(configBytes)
	if err != nil {
		return bundleReport{}, fmt.Errorf(
			"the agent's configuration is not valid JSON: %w", err,
		)
	}
	// A config.json that is JSON but not the agent's configuration (a field of
	// the wrong type, say) is the one fact the bundle may have been asked for:
	// it is reported at the top of diagnostic.txt rather than surfacing only
	// as a connectivity check that found no host.
	var device agent.Device
	configErr := json.Unmarshal(configBytes, &device)
	if configErr == nil {
		target.Device = &device
	}
	now := env.now().UTC()

	fixed := map[string][]byte{
		"config.json": redacted,
		"version.txt": []byte(fmt.Sprintf(
			"agent version %s\nbundle tool %s %s/%s\nwritten %s\n",
			agentVersionFrom(dataDir), version.Version, runtime.GOOS, runtime.GOARCH,
			now.Format(time.RFC3339),
		)),
		"service.txt": serviceStatusText(ctx, target.ServiceName, env.serviceStatus),
		"journal.txt": journalListing(filepath.Join(dataDir, "command_journal")),
		"spool.txt":   spoolListing(filepath.Join(dataDir, "postback_spool"), now),
	}
	if health, err := os.ReadFile(filepath.Join(dataDir, healthFileName)); err == nil {
		fixed[healthFileName] = healthEntry(health)
	}
	fixed["host.txt"] = captureSections(func() { runHostInfo(ctx, params, target) })
	fixed["diagnostic.txt"] = captureSections(func() {
		if configErr != nil {
			printSection("Configuration")
			printResult(false, fmt.Sprintf(
				"config.json does not load as the agent's configuration: %v", configErr,
			))
		}
		runCheckServiceStatus(params, target)
		runCommandTest()
		runConnectivityTestWith(target, env.dialer)
		runTempDirTest(target)
		props, err := readHealthFileFrom(dataDir)
		printFleetHealth(props, err, false)
	})

	logs := logFiles(target.LogFile)
	data, err := buildBundle(fixed, logs, secrets, now)
	if err != nil {
		return bundleReport{}, err
	}
	if params.BundlePath == bundleStdoutPath {
		if _, err := env.stdout.Write(data); err != nil {
			return bundleReport{}, fmt.Errorf("failed to write the bundle to stdout: %w", err)
		}
		return bundleReport{Path: bundleStdoutPath, Size: len(data)}, nil
	}
	path, err := bundlePath(params.BundlePath, target.OrgId, now)
	if err != nil {
		return bundleReport{}, err
	}
	if err := utils.WriteFileAtomic(
		utils.NewFileSystem(),
		path,
		data,
		utils.SecureFileMode,
	); err != nil {
		return bundleReport{}, fmt.Errorf("failed to write the bundle to %s: %w", path, err)
	}
	report := bundleReport{Path: path, Size: len(data)}
	// The file is complete and in place; handing it over is best effort and
	// its failure is reported on the closing line, not as a failed bundle:
	// the archive is there for whoever can read it.
	if handed, err := env.handOver(path); err != nil {
		report.HandOver = fmt.Sprintf("still owned by this process: %v", err)
	} else {
		report.HandOver = handed
	}
	return report, nil
}

// bundlePath resolves --bundle-path: empty is the default file name in the
// working directory, a directory is that name inside it, anything else is the
// file itself.
func bundlePath(requested, orgId string, now time.Time) (string, error) {
	name := fmt.Sprintf("diagnostic-%s-%s.zip", orgId, now.Format("20060102T150405Z"))
	if requested == "" {
		// The working directory, not the agent's data directory: that one is
		// owner-only and never swept, so bundles would pile up there out of
		// the operator's reach.
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf(
				"cannot resolve the working directory to write the bundle: %w", err,
			)
		}
		return filepath.Join(wd, name), nil
	}
	if info, err := os.Stat(requested); err == nil && info.IsDir() {
		return filepath.Join(requested, name), nil
	}
	return requested, nil
}

// sudoHandOver is the Unix hand-over (sc-132995): under sudo the bundle is
// written by root, and SUDO_UID/SUDO_GID name the user who typed the command,
// who could otherwise not attach the file to the support case without a
// second chown. Outside sudo there is nothing to do. getenv and chown are
// injected for the tests.
func sudoHandOver(
	getenv func(string) string,
	chown func(path string, uid, gid int) error,
) func(path string) (string, error) {
	return func(path string) (string, error) {
		uid, gid, ok := sudoInvoker(getenv)
		if !ok {
			return "", nil
		}
		if err := chown(path, uid, gid); err != nil {
			return "", fmt.Errorf("chown to uid %d gid %d failed: %w", uid, gid, err)
		}
		return fmt.Sprintf("owned by uid %d gid %d, the user behind sudo", uid, gid), nil
	}
}

// sudoInvoker reads SUDO_UID and SUDO_GID. A missing or malformed SUDO_UID
// means not under sudo; a missing SUDO_GID leaves the group as it is (-1).
func sudoInvoker(getenv func(string) string) (uid, gid int, ok bool) {
	uid, err := strconv.Atoi(strings.TrimSpace(getenv("SUDO_UID")))
	if err != nil || uid < 0 {
		return 0, 0, false
	}
	gid = -1
	if g, err := strconv.Atoi(strings.TrimSpace(getenv("SUDO_GID"))); err == nil && g >= 0 {
		gid = g
	}
	return uid, gid, true
}

// bundleTarget is the agent to bundle: the one --org-id names, or the only one
// installed.
func bundleTarget(orgId string, env bundleEnv) (agentInfo, error) {
	// A directory this process cannot read is reported as that, not as "no
	// agent": the data root and each org's directory are owner-only, so a
	// bundle run without elevation would otherwise be told nothing is there.
	if _, err := os.ReadDir(env.root); errors.Is(err, os.ErrPermission) {
		return agentInfo{}, fmt.Errorf(
			"cannot read the agents' data root %s (run --diagnostic --bundle elevated): %w",
			env.root, err,
		)
	}
	agents := scanAgentsFrom(env.root)
	if len(agents) == 0 && env.fallback != nil {
		agents = env.fallback(env.root)
	}
	if orgId != "" {
		for _, a := range agents {
			if a.OrgId == orgId {
				return a, nil
			}
		}
		info := agentInfoIn(env.root, orgId)
		_, err := os.Stat(info.ConfigFile)
		if err == nil || errors.Is(err, os.ErrPermission) {
			// Readable or not, the org's directory is there; an unreadable one
			// is reported by the caller's own read of it.
			return info, nil
		}
		return agentInfo{}, fmt.Errorf(
			"no agent for org %s is installed under %s; nothing to bundle", orgId, env.root,
		)
	}
	switch len(agents) {
	case 0:
		return agentInfo{}, fmt.Errorf(
			"no installed agent found under %s; nothing to bundle",
			env.root,
		)
	case 1:
		return agents[0], nil
	}
	ids := make([]string, 0, len(agents))
	for _, a := range agents {
		ids = append(ids, a.OrgId)
	}
	return agentInfo{}, fmt.Errorf(
		"%d agents are installed (%s); pass --org-id to choose one",
		len(agents),
		strings.Join(ids, ", "),
	)
}

// redactConfig returns the configuration with its secrets replaced and the
// secret values themselves, which are then scrubbed from every other entry -
// the log in particular, where an agent installed before the configuration
// line was masked (see config.go) has written the configuration whole. The
// keys are agent.SecretConfigKeys, the one list of credential fields.
func redactConfig(raw []byte) ([]byte, []string, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, err
	}
	var secrets []string
	for _, key := range agent.SecretConfigKeys {
		if v, ok := doc[key].(string); ok && v != "" {
			secrets = append(secrets, v)
			doc[key] = bundleRedacted
		}
	}
	if raw, ok := doc["proxy_url"].(string); ok && strings.TrimSpace(raw) != "" {
		if u, err := url.Parse(strings.TrimSpace(raw)); err == nil && u.User != nil {
			if password, ok := u.User.Password(); ok && password != "" {
				secrets = append(secrets, password)
			} else if u.User.Username() != "" {
				// A user name with no password may be a token, so it is a
				// secret too - but only in its URL form ("name@"), since a
				// bare user name is as likely to be the host name or the
				// service account and would be scrubbed out of every entry.
				secrets = append(secrets, u.User.Username()+"@")
			}
		}
		doc["proxy_url"] = utils.RedactURL(raw)
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return append(out, '\n'), secrets, nil
}

// scrub replaces every secret value in data, raw and in its JSON-escaped form.
func scrub(data []byte, secrets []string) []byte {
	for _, secret := range secrets {
		if len(secret) < bundleMinSecretLength {
			continue
		}
		data = bytes.ReplaceAll(data, []byte(secret), []byte(bundleRedacted))
		if escaped, err := json.Marshal(secret); err == nil {
			quoted := escaped[1 : len(escaped)-1]
			if !bytes.Equal(quoted, []byte(secret)) {
				data = bytes.ReplaceAll(data, quoted, []byte(bundleRedacted))
			}
		}
	}
	return data
}

// healthEntry is the fleet-health file as the bundle carries it: the health
// object and the running commands' progress, with each command's last output
// line withheld. A file that is not a report is described, not copied.
func healthEntry(raw []byte) []byte {
	var props mqtt.ReportedProperties
	if err := json.Unmarshal(raw, &props); err != nil {
		return []byte(fmt.Sprintf(
			"%s is not a fleet-health report and is withheld: %v\n", healthFileName, err,
		))
	}
	if props.Commands != nil {
		for i := range props.Commands.InFlight {
			if props.Commands.InFlight[i].LastOutputLine != "" {
				props.Commands.InFlight[i].LastOutputLine = bundleWithheld
			}
		}
	}
	out, err := json.MarshalIndent(props, "", "  ")
	if err != nil {
		return []byte(fmt.Sprintf(
			"%s could not be re-encoded and is withheld: %v\n", healthFileName, err,
		))
	}
	return append(out, '\n')
}

// bundleEntry is one zip entry, already scrubbed. A log tail is trimmed from
// its start when the zip must shrink; the fixed entries never are.
type bundleEntry struct {
	name string
	data []byte
	log  bool
}

// buildBundle zips the fixed entries and the log tails, scrubbed of every
// secret. Every file is read and scrubbed once; the tails start at
// bundleLogTailMax and are halved until the zip fits bundleMaxBytes.
func buildBundle(
	fixed map[string][]byte,
	logs []string,
	secrets []string,
	now time.Time,
) ([]byte, error) {
	names := make([]string, 0, len(fixed))
	for name := range fixed {
		names = append(names, name)
	}
	sort.Strings(names)
	var entries []bundleEntry
	for _, name := range names {
		entries = append(entries, bundleEntry{name: name, data: scrub(fixed[name], secrets)})
	}
	for _, path := range logs {
		data, err := readTail(path, bundleLogTailMax)
		if err != nil {
			data = []byte(fmt.Sprintf("could not read %s: %v\n", path, err))
		}
		entries = append(entries, bundleEntry{
			name: "logs/" + filepath.Base(path),
			data: scrub(data, secrets),
			log:  true,
		})
	}
	tail := bundleLogTailMax
	for {
		data, err := zipEntries(entries, tail, now)
		if err != nil {
			return nil, err
		}
		if len(data) <= bundleMaxBytes {
			return data, nil
		}
		if tail < 4<<10 {
			return nil, fmt.Errorf(
				"the bundle does not fit %d bytes even without log tails", bundleMaxBytes,
			)
		}
		tail /= 2
	}
}

func zipEntries(entries []bundleEntry, tail int, now time.Time) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:     e.name,
			Method:   zip.Deflate,
			Modified: now,
		})
		if err != nil {
			return nil, err
		}
		data := e.data
		if e.log {
			data = trimTail(data, tail)
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// readTail returns the last max bytes of path. When the file was cut, the
// tail starts at the first line boundary inside it (see cutToLine), and it is
// opened the way the live log viewer opens the log (openSharedRead): with
// FILE_SHARE_DELETE on Windows, so the service's log rotation is never blocked
// by the bundle reading the file.
func readTail(path string, max int) ([]byte, error) {
	f, err := openSharedRead(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() <= int64(max) {
		return io.ReadAll(f)
	}
	// One byte before the cut tells whether the cut itself sits on a line
	// boundary; cutToLine expects it.
	if _, err := f.Seek(info.Size()-int64(max)-1, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return nil, err
	}
	return cutToLine(data), nil
}

// trimTail keeps the last max bytes of data, cut to a line boundary.
func trimTail(data []byte, max int) []byte {
	if len(data) <= max {
		return data
	}
	return cutToLine(data[len(data)-max-1:])
}

// cutToLine drops the partial first line of a tail. data begins one byte
// before the cut, so a leading newline means the cut was on a line boundary
// and the whole tail is kept; otherwise the tail starts after its first
// newline. A tail with no newline at all is one line longer than the tail
// itself, which is kept rather than dropped - an empty entry would say less.
func cutToLine(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return data[i+1:]
	}
	return data[1:]
}

// logFiles is the active log and every rotated copy that exists, newest first.
func logFiles(active string) []string {
	files := []string{}
	if _, err := os.Stat(active); err == nil {
		files = append(files, active)
	}
	matches, _ := filepath.Glob(active + ".*")
	type rotated struct {
		path string
		n    int
	}
	var copies []rotated
	for _, m := range matches {
		if n, err := strconv.Atoi(strings.TrimPrefix(m, active+".")); err == nil {
			copies = append(copies, rotated{m, n})
		}
	}
	sort.Slice(copies, func(i, j int) bool { return copies[i].n < copies[j].n })
	for _, c := range copies {
		files = append(files, c.path)
	}
	return files
}

// journalListing describes the command journal: every file with its size and
// time, and for an entry its key and timestamps - never the command. The
// entries are decoded through projections without the payload field, so the
// command bytes are never decoded.
func journalListing(dir string) []byte {
	return listing(
		dir,
		"key\treceived_at\tstarted_at\tcompleted_at",
		func(name string, raw []byte) string {
			switch {
			case strings.HasSuffix(name, journalPendingSuffix):
				var e struct {
					Key        string    `json:"key"`
					ReceivedAt time.Time `json:"received_at"`
					StartedAt  time.Time `json:"started_at"`
				}
				if json.Unmarshal(raw, &e) != nil {
					return "(unreadable entry)"
				}
				return strings.Join(
					[]string{e.Key, stamp(e.ReceivedAt), stamp(e.StartedAt), "-"},
					"\t",
				)
			case strings.HasSuffix(name, journalDoneSuffix):
				var d journalTombstone
				if json.Unmarshal(raw, &d) != nil {
					return "(unreadable tombstone)"
				}
				return strings.Join([]string{d.Key, "-", "-", stamp(d.CompletedAt)}, "\t")
			}
			return ""
		},
	)
}

// spoolListing describes the postback spool: every file with its size and
// time, and for an entry its post id, when it was spooled and how old it is,
// its rejection count, the last rejection's time and reason and the time the
// engine's last throttle allowed the next attempt (sc-132997) - never the
// result, whose field the projection leaves undecoded.
func spoolListing(dir string, now time.Time) []byte {
	return listing(
		dir,
		"post_id\tcreated_at\tage\tattempts\tlast_attempt_at\tnext_attempt_at\tlast_error",
		func(_ string, raw []byte) string {
			var e struct {
				PostId        string    `json:"post_id"`
				CreatedAt     time.Time `json:"created_at"`
				Attempts      int       `json:"attempts"`
				LastError     string    `json:"last_error"`
				LastAttemptAt time.Time `json:"last_attempt_at"`
				NextAttemptAt time.Time `json:"next_attempt_at"`
			}
			if json.Unmarshal(raw, &e) != nil || e.PostId == "" {
				return ""
			}
			return strings.Join([]string{
				e.PostId,
				stamp(e.CreatedAt),
				age(now, e.CreatedAt),
				strconv.Itoa(e.Attempts),
				stamp(e.LastAttemptAt),
				stamp(e.NextAttemptAt),
				strings.Join(strings.Fields(e.LastError), " "),
			}, "\t")
		},
	)
}

func listing(dir, detailHeader string, detail func(name string, raw []byte) string) []byte {
	var buf bytes.Buffer
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintf(&buf, "%s: not present\n", dir)
		} else {
			_, _ = fmt.Fprintf(&buf, "%s: %v\n", dir, err)
		}
		return buf.Bytes()
	}
	_, _ = fmt.Fprintf(&buf, "%s: %d file(s)\n\n", dir, len(entries))
	tw := tabwriter.NewWriter(&buf, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "name\tsize\tmodified\t%s\n", detailHeader)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		extra := ""
		if !entry.IsDir() && info.Size() < 1<<20 {
			if raw, err := os.ReadFile(filepath.Join(dir, entry.Name())); err == nil {
				extra = detail(entry.Name(), raw)
			}
		}
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n",
			entry.Name(), info.Size(), info.ModTime().UTC().Format(time.RFC3339), extra)
	}
	_ = tw.Flush()
	return buf.Bytes()
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

// age is how long before now t was, to the second, or "-" for a zero time.
func age(now, t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return now.Sub(t).Truncate(time.Second).String()
}

// agentVersionFrom is the version the running service last reported, from its
// health file, or "unknown".
func agentVersionFrom(dataDir string) string {
	props, err := readHealthFileFrom(dataDir)
	if err != nil || props.AgentVersion == "" {
		return "unknown (no health report yet)"
	}
	return props.AgentVersion
}

// serviceStatusText runs each service-manager query and records its output.
func serviceStatusText(
	ctx context.Context,
	name string,
	run func(ctx context.Context, argv []string) string,
) []byte {
	var buf bytes.Buffer
	for _, argv := range serviceManagerCommands(name) {
		_, _ = fmt.Fprintf(&buf, "$ %s\n%s\n", strings.Join(argv, " "), run(ctx, argv))
	}
	return buf.Bytes()
}

// runServiceManagerCommand runs one service-manager query under the
// diagnostic's shell-out bound (diagnosticCommandTimeout).
func runServiceManagerCommand(ctx context.Context, argv []string) string {
	ctx, cancel := context.WithTimeout(ctx, diagnosticCommandTimeout)
	defer cancel()
	// #nosec G204 - fixed service-manager binaries; the name comes from the
	// agent's own service naming.
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.WaitDelay = diagnosticCommandWaitDelay
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("%s(exit: %v)", out, err)
	}
	return string(out)
}

// captureSections runs the diagnostic sections with their output going to the
// returned buffer and, best effort, to the console: the buffer is written
// first, so a console that is gone (a closed pipe, a vanished terminal) costs
// the operator the echo and never the archive entry.
func captureSections(run func()) []byte {
	var buf bytes.Buffer
	previous := diagOut
	diagOut = io.MultiWriter(&buf, previous)
	defer func() { diagOut = previous }()
	run()
	return buf.Bytes()
}
