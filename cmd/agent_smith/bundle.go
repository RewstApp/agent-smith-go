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
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
)

// The diagnostic bundle (sc-129690): `--diagnostic --bundle` writes one zip
// that answers a support case's first round of questions - the configuration
// with its secrets redacted, the log tails, what the command journal and the
// postback spool hold (never a command or a result), the service manager's
// view, the host, the agent version and the console output of every check.

const (
	// bundleLogTailMax is the most of each log file the bundle keeps.
	bundleLogTailMax = 2 << 20
	// bundleMaxBytes is the ceiling on the zip; log tails are trimmed to fit.
	bundleMaxBytes = 25 << 20
	// bundleRedacted replaces every secret value in every entry.
	bundleRedacted = "***"
	// bundleCommandTimeout bounds each service-manager query.
	bundleCommandTimeout = 30 * time.Second
	// bundleMinSecretLength keeps a short value from being scrubbed out of
	// unrelated text; every secret the agent holds is far longer.
	bundleMinSecretLength = 6
)

// bundleSecretKeys are the config keys whose values the bundle redacts.
var bundleSecretKeys = []string{"shared_access_key", "config_secret", "github_token"}

// bundleEnv is what writing a bundle depends on, injectable for tests.
type bundleEnv struct {
	root     string
	fallback func(string) []agentInfo
	dialer   tlsDialer
	now      func() time.Time
	// serviceStatus runs a service-manager query; nil runs it for real.
	serviceStatus func(ctx context.Context, argv []string) string
}

// runDiagnosticBundle writes the bundle for the agent the parameters name (or
// the only one installed) and returns its path.
func runDiagnosticBundle(
	ctx context.Context,
	params *diagnosticContext,
	env bundleEnv,
) (string, error) {
	if env.now == nil {
		env.now = time.Now
	}
	if env.serviceStatus == nil {
		env.serviceStatus = runServiceManagerCommand
	}
	target, err := bundleTarget(params.OrgId, env)
	if err != nil {
		return "", err
	}
	dataDir := filepath.Join(env.root, target.OrgId)
	if _, err := os.ReadDir(dataDir); err != nil {
		return "", fmt.Errorf(
			"cannot read the agent's data directory %s (run --diagnostic --bundle elevated): %w",
			dataDir, err,
		)
	}
	configBytes, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	if err != nil {
		return "", fmt.Errorf("cannot read the agent's configuration: %w", err)
	}
	redacted, secrets, err := redactConfig(configBytes)
	if err != nil {
		return "", fmt.Errorf("the agent's configuration is not valid JSON: %w", err)
	}
	var device agent.Device
	_ = json.Unmarshal(configBytes, &device)
	target.Device = &device

	fixed := map[string][]byte{
		"config.json": redacted,
		"version.txt": []byte(fmt.Sprintf(
			"agent version %s\nbundle tool %s %s/%s\nwritten %s\n",
			agentVersionFrom(dataDir), version.Version, runtime.GOOS, runtime.GOARCH,
			env.now().UTC().Format(time.RFC3339),
		)),
		"service.txt": serviceStatusText(ctx, target.ServiceName, env.serviceStatus),
		"journal.txt": journalListing(filepath.Join(dataDir, "command_journal")),
		"spool.txt":   spoolListing(filepath.Join(dataDir, "postback_spool")),
	}
	if health, err := os.ReadFile(filepath.Join(dataDir, healthFileName)); err == nil {
		fixed[healthFileName] = health
	}
	fixed["host.txt"] = captureSections(func() { runHostInfo(ctx, params, target) })
	fixed["diagnostic.txt"] = captureSections(func() {
		runCheckServiceStatus(params, target)
		runCommandTest()
		runConnectivityTestWith(target, env.dialer)
		runTempDirTest(target)
		runFleetHealth(target)
	})

	logs := logFiles(filepath.Join(dataDir, filepath.Base(agent.GetLogFilePath(target.OrgId))))
	data, err := buildBundle(fixed, logs, secrets)
	if err != nil {
		return "", err
	}
	path := params.BundlePath
	if path == "" {
		path = filepath.Join(dataDir, fmt.Sprintf(
			"diagnostic-%s-%s.zip", target.OrgId, env.now().UTC().Format("20060102T150405Z"),
		))
	}
	if err := utils.WriteFileAtomic(
		utils.NewFileSystem(),
		path,
		data,
		utils.SecureFileMode,
	); err != nil {
		return "", fmt.Errorf("failed to write the bundle to %s: %w", path, err)
	}
	return path, nil
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
		_, err := os.Stat(filepath.Join(env.root, orgId, "config.json"))
		if err == nil || errors.Is(err, os.ErrPermission) {
			// Readable or not, the org's directory is there; an unreadable one
			// is reported by the caller's own read of it.
			return agentInfo{OrgId: orgId, ServiceName: formatServiceName(orgId)}, nil
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
// the log in particular, where install has written the configuration whole.
func redactConfig(raw []byte) ([]byte, []string, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, err
	}
	var secrets []string
	for _, key := range bundleSecretKeys {
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
				secrets = append(secrets, u.User.Username())
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

// buildBundle zips the fixed entries and the log tails, scrubbed of every
// secret. The tails start at bundleLogTailMax and are halved until the zip
// fits bundleMaxBytes.
func buildBundle(fixed map[string][]byte, logs []string, secrets []string) ([]byte, error) {
	tail := bundleLogTailMax
	for {
		data, err := zipEntries(fixed, logs, secrets, tail)
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

func zipEntries(
	fixed map[string][]byte,
	logs []string,
	secrets []string,
	tail int,
) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(fixed))
	for name := range fixed {
		names = append(names, name)
	}
	sort.Strings(names)
	add := func(name string, data []byte) error {
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:     name,
			Method:   zip.Deflate,
			Modified: time.Now(),
		})
		if err != nil {
			return err
		}
		_, err = w.Write(scrub(data, secrets))
		return err
	}
	for _, name := range names {
		if err := add(name, fixed[name]); err != nil {
			return nil, err
		}
	}
	for _, path := range logs {
		data, err := readTail(path, tail)
		if err != nil {
			data = []byte(fmt.Sprintf("could not read %s: %v\n", path, err))
		}
		if err := add("logs/"+filepath.Base(path), data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// readTail returns the last max bytes of path, starting at a line boundary
// when the file was cut.
func readTail(path string, max int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	start := info.Size() - int64(max)
	if start <= 0 {
		return io.ReadAll(f)
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(max)))
	if err != nil {
		return nil, err
	}
	if i := bytes.IndexByte(data, '\n'); i >= 0 && i+1 < len(data) {
		data = data[i+1:]
	}
	return data, nil
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
// time, and for an entry its key and timestamps - never the command.
func journalListing(dir string) []byte {
	return listing(
		dir,
		"key\treceived_at\tstarted_at\tcompleted_at",
		func(name string, raw []byte) string {
			switch {
			case strings.HasSuffix(name, journalPendingSuffix):
				var e journalEntry
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
// time, and for an entry its post id, age and attempts - never the result.
func spoolListing(dir string) []byte {
	return listing(dir, "post_id\tcreated_at\tattempts", func(_ string, raw []byte) string {
		var e spoolEntry
		if json.Unmarshal(raw, &e) != nil || e.PostId == "" {
			return ""
		}
		return strings.Join([]string{e.PostId, stamp(e.CreatedAt), strconv.Itoa(e.Attempts)}, "\t")
	})
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

func runServiceManagerCommand(ctx context.Context, argv []string) string {
	ctx, cancel := context.WithTimeout(ctx, bundleCommandTimeout)
	defer cancel()
	// #nosec G204 - fixed service-manager binaries; the name comes from the
	// agent's own service naming.
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("%s(exit: %v)", out, err)
	}
	return string(out)
}

// captureSections runs the diagnostic sections with their output going to the
// console and to the returned buffer.
func captureSections(run func()) []byte {
	var buf bytes.Buffer
	previous := diagOut
	diagOut = io.MultiWriter(previous, &buf)
	defer func() { diagOut = previous }()
	run()
	return buf.Bytes()
}
