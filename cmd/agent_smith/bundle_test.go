package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/mqtt"
)

// The diagnostic bundle (sc-129690) carries the configuration with its secrets
// redacted, the log tails, journal and spool listings without their payloads,
// the service manager's view, the host and the console output of the checks -
// and none of the secrets' bytes, anywhere.

const (
	bundleOrg          = "43fb08b0-92cf-462a-924d-0b6be7a43a48"
	bundleSharedKey    = "SAK-VERY-SECRET-KEY-123=="
	bundleGithubToken  = "ghp_SECRETTOKEN456"
	bundleProxyPass    = "PROXYPASS789"
	bundlePayloadMark  = "PAYLOAD-MARKER-do-not-ship"
	bundleResultMark   = "RESULT-MARKER-do-not-ship"
	bundleOutputMark   = "OUTPUT-LINE-MARKER-do-not-ship"
	bundleJournalKeyID = "post:abc123"
)

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// newBundleRoot lays out one installed agent's data directory.
func newBundleRoot(t *testing.T) (root, dataDir string) {
	t.Helper()
	root = t.TempDir()
	dataDir = filepath.Join(root, bundleOrg)
	writeJSON(t, filepath.Join(dataDir, "config.json"), map[string]any{
		"device_id":          "dev-1",
		"rewst_org_id":       bundleOrg,
		"shared_access_key":  bundleSharedKey,
		"github_token":       bundleGithubToken,
		"proxy_url":          "http://agent:" + bundleProxyPass + "@proxy:3128",
		"azure_iot_hub_host": "127.0.0.1",
		"some_future_field":  "kept",
	})
	logName := filepath.Base(agent.GetLogFilePath(bundleOrg))
	active := filepath.Join(dataDir, logName)
	// Install writes the configuration whole into the log: the scrub must
	// catch it there too.
	logLine := `2026-11-03T10:00:00Z [INFO]  agent_smith: Received configuration: configuration="{\"shared_access_key\": \"` +
		bundleSharedKey + `\", \"github_token\": \"` + bundleGithubToken + `\"}"` + "\n"
	if err := os.WriteFile(
		active,
		[]byte(logLine+"[INFO]  agent_smith: Subscribed to messages\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"1", "2"} {
		if err := os.WriteFile(
			active+"."+n,
			[]byte("rotated copy "+n+"\n"+logLine),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	journal := filepath.Join(dataDir, "command_journal")
	writeJSON(t, filepath.Join(journal, "abc"+journalPendingSuffix), journalEntry{
		Key:        bundleJournalKeyID,
		Payload:    []byte(`{"commands":"` + bundlePayloadMark + `"}`),
		ReceivedAt: time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC),
		StartedAt:  time.Date(2026, 11, 3, 9, 0, 1, 0, time.UTC),
	})
	writeJSON(t, filepath.Join(journal, "def"+journalDoneSuffix), journalTombstone{
		Key:         "post:done1",
		CompletedAt: time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC),
	})
	writeJSON(t, filepath.Join(dataDir, "postback_spool", "e1.json"), spoolEntry{
		PostId:        "spooled-1",
		Result:        []byte(`{"output":"` + bundleResultMark + `"}`),
		CreatedAt:     time.Date(2026, 11, 3, 7, 0, 0, 0, time.UTC),
		Attempts:      2,
		LastError:     "HTTP 422: unprocessable\n(body withheld)",
		LastAttemptAt: time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC),
	})
	// The health file the service leaves beside the journal carries a running
	// command's last output line: the bundle must withhold it.
	writeJSON(t, filepath.Join(dataDir, healthFileName), mqtt.ReportedProperties{
		AgentVersion: "v1.7.0-test",
		Health:       &mqtt.Health{ReportedAt: "2026-11-03T11:59:00Z", UptimeSeconds: 42},
		Commands: &mqtt.CommandsProgress{InFlight: []mqtt.CommandProgress{{
			PostID:         "running-1",
			StartedAt:      "2026-11-03T11:58:00Z",
			ElapsedSeconds: 60,
			StdoutBytes:    10,
			LastOutputLine: "token: " + bundleOutputMark,
		}}},
	})
	return root, dataDir
}

func bundleTestEnv(root string) bundleEnv {
	return bundleEnv{
		root:   root,
		dialer: &mockTLSDialer{},
		now:    func() time.Time { return time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC) },
		serviceStatus: func(_ context.Context, argv []string) string {
			return "stub status for " + strings.Join(argv, " ")
		},
	}
}

func bundleTestParams(path string) *diagnosticContext {
	return &diagnosticContext{
		OrgId:          bundleOrg,
		Diagnostic:     true,
		Bundle:         true,
		BundlePath:     path,
		ServiceManager: &mockServiceManager{openService: &mockService{isActive: true}},
		Sys:            &mockSystemInfoProvider{hostname: "host", hostPlatform: "linux"},
		Domain:         &mockDomainInfoProvider{},
	}
}

func readBundle(t *testing.T, path string) map[string][]byte {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = data
	}
	return out
}

// quiet sends the sections' console output to a buffer for the test.
func quiet(t *testing.T) {
	t.Helper()
	previous := diagOut
	diagOut = io.Discard
	t.Cleanup(func() { diagOut = previous })
}

func TestRunDiagnosticBundle_ContentsAndRedaction(t *testing.T) {
	quiet(t)
	root, _ := newBundleRoot(t)
	path := filepath.Join(t.TempDir(), "bundle.zip")
	got, err := runDiagnosticBundle(
		context.Background(),
		bundleTestParams(path),
		bundleTestEnv(root),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Errorf("wrote %s, want %s", got, path)
	}
	entries := readBundle(t, path)

	logName := filepath.Base(agent.GetLogFilePath(bundleOrg))
	want := []string{
		"config.json", "diagnostic.txt", "host.txt", "journal.txt", "service.txt",
		"spool.txt", "version.txt", healthFileName,
		"logs/" + logName, "logs/" + logName + ".1", "logs/" + logName + ".2",
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	sort.Strings(want)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("entries %v, want %v", names, want)
	}

	// No secret, no command, no result and no command output reaches any
	// entry, in any form.
	forbidden := []string{
		bundleSharedKey, bundleGithubToken, bundleProxyPass, bundlePayloadMark, bundleResultMark,
		bundleOutputMark,
		base64.StdEncoding.EncodeToString([]byte(`{"commands":"` + bundlePayloadMark + `"}`)),
		base64.StdEncoding.EncodeToString([]byte(`{"output":"` + bundleResultMark + `"}`)),
	}
	for name, data := range entries {
		for _, secret := range forbidden {
			if bytes.Contains(data, []byte(secret)) {
				t.Errorf("%s contains %q", name, secret)
			}
		}
	}

	var config map[string]any
	if err := json.Unmarshal(entries["config.json"], &config); err != nil {
		t.Fatal(err)
	}
	if config["shared_access_key"] != "***" || config["github_token"] != "***" ||
		config["proxy_url"] != "http://agent:xxxxx@proxy:3128" || config["some_future_field"] != "kept" {
		t.Errorf("config.json %s", entries["config.json"])
	}
	if !bytes.Contains(entries["logs/"+logName], []byte("Subscribed to messages")) ||
		!bytes.Contains(entries["logs/"+logName], []byte("***")) {
		t.Errorf("active log tail %s", entries["logs/"+logName])
	}
	journal := string(entries["journal.txt"])
	for _, s := range []string{bundleJournalKeyID, "2026-11-03T09:00:00Z", "2026-11-03T09:00:01Z", "post:done1"} {
		if !strings.Contains(journal, s) {
			t.Errorf("journal.txt lacks %q:\n%s", s, journal)
		}
	}
	spool := string(entries["spool.txt"])
	for _, s := range []string{
		"spooled-1", "e1.json", "age", "5h0m0s", "last_error", "HTTP 422: unprocessable (body withheld)",
		"2026-11-03T11:00:00Z",
	} {
		if !strings.Contains(spool, s) {
			t.Errorf("spool.txt lacks %q:\n%s", s, spool)
		}
	}
	// health.json is carried with the output line withheld, and the Fleet
	// Health section and version.txt read the same file the bundle copies.
	var health mqtt.ReportedProperties
	if err := json.Unmarshal(entries[healthFileName], &health); err != nil {
		t.Fatalf("health.json: %v\n%s", err, entries[healthFileName])
	}
	if health.AgentVersion != "v1.7.0-test" || health.Commands == nil ||
		len(health.Commands.InFlight) != 1 ||
		health.Commands.InFlight[0].LastOutputLine != bundleWithheld ||
		health.Commands.InFlight[0].PostID != "running-1" {
		t.Errorf("health.json %s", entries[healthFileName])
	}
	if !bytes.Contains(entries["version.txt"], []byte("agent version v1.7.0-test")) {
		t.Errorf("version.txt %s", entries["version.txt"])
	}
	for _, s := range []string{"Reported at 2026-11-03T11:59:00Z", "in flight running-1", bundleWithheld} {
		if !bytes.Contains(entries["diagnostic.txt"], []byte(s)) {
			t.Errorf("diagnostic.txt lacks %q:\n%s", s, entries["diagnostic.txt"])
		}
	}
	if !bytes.Contains(entries["service.txt"], []byte("stub status for")) {
		t.Errorf("service.txt %s", entries["service.txt"])
	}
	if !bytes.Contains(entries["version.txt"], []byte(runtime.GOOS)) {
		t.Errorf("version.txt %s", entries["version.txt"])
	}
	for _, section := range []string{"Service Status", "MQTT/WebSocket Connectivity", "Fleet Health"} {
		if !bytes.Contains(entries["diagnostic.txt"], []byte(section)) {
			t.Errorf("diagnostic.txt lacks the %s section", section)
		}
	}
	if !bytes.Contains(entries["host.txt"], []byte("Host Information")) {
		t.Errorf("host.txt %s", entries["host.txt"])
	}
}

// Without --bundle-path the zip lands in the working directory, not in the
// owner-only data directory where nothing would ever sweep it.
func TestRunDiagnosticBundle_DefaultPathIsInTheWorkingDirectory(t *testing.T) {
	quiet(t)
	root, dataDir := newBundleRoot(t)
	wd := t.TempDir()
	t.Chdir(wd)
	got, err := runDiagnosticBundle(context.Background(), bundleTestParams(""), bundleTestEnv(root))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(wd, "diagnostic-"+bundleOrg+"-20261103T120000Z.zip")
	if got != want {
		t.Errorf("default path %s, want %s", got, want)
	}
	if entries, err := os.ReadDir(dataDir); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".zip") {
				t.Errorf("the bundle was written into the data directory: %s", e.Name())
			}
		}
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatal(err)
	}
	// The single installed agent is chosen without --org-id.
	params := bundleTestParams(filepath.Join(t.TempDir(), "b.zip"))
	params.OrgId = ""
	if _, err := runDiagnosticBundle(
		context.Background(),
		params,
		bundleTestEnv(root),
	); err != nil {
		t.Errorf("the only installed agent was not chosen: %v", err)
	}
}

// Every log tail is at most 2 MiB, and the zip never exceeds 25 MiB: tails are
// trimmed until it fits.
func TestRunDiagnosticBundle_TailsAndTheSizeCeiling(t *testing.T) {
	quiet(t)
	root, dataDir := newBundleRoot(t)
	active := filepath.Join(dataDir, filepath.Base(agent.GetLogFilePath(bundleOrg)))
	noise := make([]byte, 3<<20)
	for i := 0; i <= 15; i++ {
		if _, err := rand.Read(noise); err != nil {
			t.Fatal(err)
		}
		// Incompressible lines, so the zip really is as large as its content.
		text := []byte(strings.ReplaceAll(base64.StdEncoding.EncodeToString(noise), "A", "\n"))
		name := active
		if i > 0 {
			name = active + "." + strconv.Itoa(i)
		}
		if err := os.WriteFile(name, text, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "big.zip")
	if _, err := runDiagnosticBundle(
		context.Background(),
		bundleTestParams(path),
		bundleTestEnv(root),
	); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > bundleMaxBytes {
		t.Errorf("bundle is %d bytes, past the %d ceiling", info.Size(), bundleMaxBytes)
	}
	entries := readBundle(t, path)
	logs := 0
	for name, data := range entries {
		if !strings.HasPrefix(name, "logs/") {
			continue
		}
		logs++
		if len(data) > bundleLogTailMax {
			t.Errorf("%s is %d bytes, past the %d tail", name, len(data), bundleLogTailMax)
		}
	}
	if logs != 16 {
		t.Errorf("%d log entries, want 16", logs)
	}
}

func TestRunDiagnosticBundle_Errors(t *testing.T) {
	quiet(t)
	empty := t.TempDir()
	path := filepath.Join(t.TempDir(), "none.zip")
	params := bundleTestParams(path)
	params.OrgId = ""
	if _, err := runDiagnosticBundle(
		context.Background(),
		params,
		bundleTestEnv(empty),
	); err == nil ||
		!strings.Contains(err.Error(), "no installed agent") {
		t.Errorf("no agent: %v", err)
	}
	params.OrgId = bundleOrg
	if _, err := runDiagnosticBundle(
		context.Background(),
		params,
		bundleTestEnv(empty),
	); err == nil ||
		!strings.Contains(err.Error(), "nothing to bundle") {
		t.Errorf("unknown org: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a failed bundle left a file behind")
	}

	// Several agents and no --org-id.
	root, _ := newBundleRoot(t)
	other := filepath.Join(root, "53f5b9c3-a168-4fa4-b155-e2dca1d1d40a")
	writeJSON(t, filepath.Join(other, "config.json"), map[string]string{"device_id": "dev-2"})
	params.OrgId = ""
	if _, err := runDiagnosticBundle(
		context.Background(),
		params,
		bundleTestEnv(root),
	); err == nil ||
		!strings.Contains(err.Error(), "pass --org-id") {
		t.Errorf("several agents: %v", err)
	}
}

func TestRunDiagnosticBundle_UnreadableDataDirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	quiet(t)
	root, dataDir := newBundleRoot(t)
	if err := os.Chmod(dataDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dataDir, 0o755) })
	path := filepath.Join(t.TempDir(), "x.zip")
	_, err := runDiagnosticBundle(context.Background(), bundleTestParams(path), bundleTestEnv(root))
	if err == nil || !strings.Contains(err.Error(), "cannot read the agent's data directory") {
		t.Errorf("unreadable: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a failed bundle left a file behind")
	}
}

func TestNewDiagnosticContext_BundleFlags(t *testing.T) {
	ctx, err := newDiagnosticContext(
		[]string{"--diagnostic", "--bundle", "--bundle-path", "/tmp/x.zip"}, nil, nil, nil, nil,
	)
	if err != nil || !ctx.Bundle || ctx.BundlePath != "/tmp/x.zip" {
		t.Errorf("%+v %v", ctx, err)
	}
	if _, err := newDiagnosticContext(
		[]string{"--diagnostic", "--bundle-path", "/tmp/x.zip"}, nil, nil, nil, nil,
	); err == nil {
		t.Error("--bundle-path without --bundle was accepted")
	}
}

// A config.json that is JSON but not the agent's configuration is reported at
// the top of diagnostic.txt, not just as a connectivity check with no host.
func TestRunDiagnosticBundle_ReportsAConfigThatDoesNotLoad(t *testing.T) {
	quiet(t)
	root, dataDir := newBundleRoot(t)
	writeJSON(t, filepath.Join(dataDir, "config.json"), map[string]any{
		"device_id":          "dev-1",
		"shared_access_key":  bundleSharedKey,
		"azure_iot_hub_host": "127.0.0.1",
		"worker_count":       "4",
	})
	path := filepath.Join(t.TempDir(), "bad.zip")
	if _, err := runDiagnosticBundle(
		context.Background(),
		bundleTestParams(path),
		bundleTestEnv(root),
	); err != nil {
		t.Fatal(err)
	}
	entries := readBundle(t, path)
	diag := string(entries["diagnostic.txt"])
	if !strings.Contains(diag, "config.json does not load as the agent's configuration") ||
		!strings.Contains(diag, "worker_count") {
		t.Errorf("diagnostic.txt does not name the parse error:\n%s", diag)
	}
	if bytes.Contains(entries["config.json"], []byte(bundleSharedKey)) {
		t.Error("config.json was not redacted")
	}
}

// The console is written best effort: a console that fails (a closed pipe)
// must not empty the archive entry.
func TestCaptureSections_KeepsTheEntryWhenTheConsoleFails(t *testing.T) {
	previous := diagOut
	diagOut = failingWriter{}
	t.Cleanup(func() { diagOut = previous })
	got := captureSections(func() { dprintf("line %d\n", 1); dprintln("line 2") })
	if string(got) != "line 1\nline 2\n" {
		t.Errorf("captured %q", got)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// A proxy user name with no password is scrubbed only in its URL form, so a
// user name that is also the host name or the service account survives.
func TestRedactConfig_ProxyUserNameIsScrubbedOnlyAsUserinfo(t *testing.T) {
	raw := []byte(`{"proxy_url": "http://administrator@proxy:3128", ` +
		`"shared_access_key": "` + bundleSharedKey + `"}`)
	redacted, secrets, err := redactConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(secrets)
	if strings.Join(secrets, ",") != bundleSharedKey+",administrator@" {
		t.Errorf("secrets %q", secrets)
	}
	if !bytes.Contains(redacted, []byte(`"http://xxxxx@proxy:3128"`)) {
		t.Errorf("config.json %s", redacted)
	}
	host := scrub(
		[]byte("Hostname: administrator\nproxy http://administrator@proxy:3128 x"),
		secrets,
	)
	if string(host) != "Hostname: administrator\nproxy http://***proxy:3128 x" {
		t.Errorf("scrubbed %q", host)
	}
}

// Tails start at a line boundary: a cut inside a line drops that partial
// line, a cut right after a newline keeps the first full line, and a tail
// with no newline at all is kept whole.
func TestReadTail_CutsAtALineBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	write := func(s string) {
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(max int) string {
		got, err := readTail(path, max)
		if err != nil {
			t.Fatal(err)
		}
		return string(got)
	}
	write("first\nsecond\nthird\n")
	if got := read(100); got != "first\nsecond\nthird\n" {
		t.Errorf("whole file: %q", got)
	}
	if got := read(10); got != "third\n" { // cut inside "second"
		t.Errorf("cut inside a line: %q", got)
	}
	if got := read(13); got != "second\nthird\n" { // cut right after "first\n"
		t.Errorf("cut on a boundary: %q", got)
	}
	write("one very long line without a newline")
	if got := read(10); got != " a newline" {
		t.Errorf("no newline: %q", got)
	}
	write(strings.Repeat("x", 20) + "\n")
	if got := read(10); got != "" {
		t.Errorf("only a trailing newline: %q", got)
	}
	if got := string(trimTail([]byte("first\nsecond\nthird\n"), 13)); got != "second\nthird\n" {
		t.Errorf("trimTail: %q", got)
	}
}

func TestHealthEntry_DescribesAFileThatIsNotAReport(t *testing.T) {
	got := string(healthEntry([]byte("not json")))
	if !strings.Contains(got, "is not a fleet-health report") || strings.Contains(got, "not json") {
		t.Errorf("%q", got)
	}
}

func TestScrub_ReplacesRawAndEscapedForms(t *testing.T) {
	secret := `a"b\secret-value`
	escaped, _ := json.Marshal(secret)
	data := []byte("raw " + secret + " escaped " + string(escaped[1:len(escaped)-1]))
	out := scrub(data, []string{secret, "short"})
	if bytes.Contains(out, []byte("secret-value")) {
		t.Errorf("scrubbed %q", out)
	}
	if !bytes.Contains(scrub([]byte("a short word"), []string{"short"}), []byte("short")) {
		t.Error("a value below the minimum length was scrubbed from unrelated text")
	}
}
