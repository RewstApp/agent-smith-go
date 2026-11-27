package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/hashicorp/go-hclog"
)

// log_compress (sc-132996): the flag at install and --update, the twin
// setting, and the bundle and live viewer reading compressed copies.

func gzipBytes(t *testing.T, data string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// --log-compress at install is off by default and saved when given.
func TestNewConfigContext_LogCompress(t *testing.T) {
	args := []string{
		"--org-id", "43fb08b0-92cf-462a-924d-0b6be7a43a48",
		"--config-url", "https://example.com/config", "--config-secret", "s",
	}
	ctx, err := newConfigContext(args, nil, nil, nil, nil)
	if err != nil || ctx.LogCompress {
		t.Errorf("default: %+v %v", ctx, err)
	}
	ctx, err = newConfigContext(append(args, "--log-compress"), nil, nil, nil, nil)
	if err != nil || !ctx.LogCompress {
		t.Errorf("given: %+v %v", ctx, err)
	}
}

// On --update the saved value changes only when the flag is given, so an
// update that leaves it out never turns compression off.
func TestNewUpdateContext_LogCompress(t *testing.T) {
	base := []string{"--org-id", "43fb08b0-92cf-462a-924d-0b6be7a43a48", "--update"}
	for _, c := range []struct {
		args          []string
		provided, set bool
	}{
		{nil, false, false},
		{[]string{"--log-compress"}, true, true},
		{[]string{"--log-compress=false"}, true, false},
	} {
		ctx, err := newUpdateContext(append(base, c.args...), nil, nil, nil, nil)
		if err != nil || ctx.LogCompressProvided != c.provided || ctx.LogCompress != c.set {
			t.Errorf("%v: %+v %v", c.args, ctx, err)
		}
	}
}

// The twin turns compression on and off at once, persists it, and rejects a
// value that is not a boolean with the one in effect kept.
func TestApplyDesired_LogCompressAppliesToTheLogWriter(t *testing.T) {
	svc, _ := newSettingsTestService(t, settingsTestConfig)
	writer, err := utils.NewRotatingFile(
		filepath.Join(t.TempDir(), "agent.log"), 1<<20, 3, utils.DefaultFileMod, "agent_smith",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	svc.logWriter = writer
	logger := hclog.NewNullLogger()

	acks := svc.applyDesired([]byte(`{"$version": 1, "log_compress": true}`), false, logger)
	if acks["log_compress"].Status != settingApplied || !writer.CompressEnabled() ||
		!svc.currentDevice().LogCompress {
		t.Fatalf("on: %+v, writer compressing %v", acks["log_compress"], writer.CompressEnabled())
	}
	cfg, _ := os.ReadFile(svc.ConfigFile)
	if !strings.Contains(string(cfg), `"log_compress": true`) {
		t.Errorf("not persisted:\n%s", cfg)
	}

	acks = svc.applyDesired([]byte(`{"$version": 2, "log_compress": "yes"}`), false, logger)
	if acks["log_compress"].Status != settingRejected || !writer.CompressEnabled() ||
		!strings.Contains(acks["log_compress"].Detail, "log_compress") {
		t.Errorf("a string: %+v", acks["log_compress"])
	}

	acks = svc.applyDesired([]byte(`{"$version": 3, "log_compress": false}`), false, logger)
	if acks["log_compress"].Status != settingApplied || writer.CompressEnabled() {
		t.Errorf("off: %+v", acks["log_compress"])
	}
}

func TestLogFiles_RecognisesCompressedCopies(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, "rewst_agent.log")
	for _, name := range []string{
		"rewst_agent.log", "rewst_agent.log.1", "rewst_agent.log.1.gz",
		"rewst_agent.log.2.gz", "rewst_agent.log.10", "rewst_agent.log.3.gz.tmp",
		"rewst_agent.log.x.gz",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := logFiles(active)
	want := []string{
		active, active + ".1", active + ".2.gz", active + ".10",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("\n got %v\nwant %v", got, want)
	}
}

// A compressed copy's tail is the decompressed text's, cut to a line boundary
// the way a plain copy's is; a corrupt one is an error.
func TestReadTail_DecompressesACompressedCopy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rewst_agent.log.1.gz")
	text := strings.Repeat("0123456789\n", 1000) + "last line\n"
	if err := os.WriteFile(path, gzipBytes(t, text), 0o600); err != nil {
		t.Fatal(err)
	}
	whole, err := readTail(path, len(text))
	if err != nil || string(whole) != text {
		t.Errorf("whole: %d bytes, %v", len(whole), err)
	}
	tail, err := readTail(path, 25)
	if err != nil || string(tail) != "0123456789\nlast line\n" {
		t.Errorf("tail %q, %v", tail, err)
	}

	corrupt := filepath.Join(dir, "rewst_agent.log.2.gz")
	if err := os.WriteFile(corrupt, gzipBytes(t, text)[:40], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readTail(corrupt, 1<<20); err == nil {
		t.Error("a truncated copy read without an error")
	}
}

// The bundle carries a compressed copy decompressed, named as its plain form
// would be and scrubbed like every entry; a corrupt one is reported in its
// entry rather than failing the bundle.
func TestRunDiagnosticBundle_CarriesCompressedCopiesAsPlainText(t *testing.T) {
	quiet(t)
	root, dataDir := newBundleRoot(t)
	logName := filepath.Base(agent.GetLogFilePath(bundleOrg))
	active := filepath.Join(dataDir, logName)
	second, err := os.ReadFile(active + ".2")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(active+".2.gz", gzipBytes(t, string(second)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(active + ".2"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(active+".3.gz", []byte("not gzip"), 0o600); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "bundle.zip")
	if _, err := runDiagnosticBundle(
		context.Background(), bundleTestParams(path), bundleTestEnv(root),
	); err != nil {
		t.Fatal(err)
	}
	entries := readBundle(t, path)
	for name := range entries {
		if strings.HasSuffix(name, ".gz") {
			t.Errorf("entry %s keeps the .gz name", name)
		}
	}
	got := string(entries["logs/"+logName+".2"])
	if !strings.HasPrefix(got, "rotated copy 2\n") {
		t.Errorf("logs/%s.2 is not the decompressed copy: %q", logName, got)
	}
	for _, secret := range []string{bundleSharedKey, bundleGithubToken} {
		if strings.Contains(got, secret) {
			t.Errorf("the decompressed copy carries %q", secret)
		}
	}
	broken := string(entries["logs/"+logName+".3"])
	if !strings.HasPrefix(broken, "could not read ") {
		t.Errorf("the corrupt copy's entry: %q", broken)
	}
}

// The live viewer follows a rotation whose old copy is then compressed and
// removed, printing every line once: it reads the old file through the handle
// it holds, which compression's removal does not disturb.
func TestRunLiveLogs_FollowsARotationWhoseCopyIsCompressed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	if err := os.WriteFile(path, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out syncBuffer
	prev := liveLogOutput
	liveLogOutput = &out
	t.Cleanup(func() { liveLogOutput = prev })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runLiveLogsWith(ctx, agentInfo{OrgId: "org-1", LogFile: path}, &osLogFileOpener{})
	}()

	waitForOutput(t, &out, "one")
	appendToFile(t, path, "two\n")
	rotateLog(t, path, "three\n")
	old, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".1.gz", gzipBytes(t, string(old)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path + ".1"); err != nil {
		t.Fatalf("removing the compressed copy's plain form under the viewer: %v", err)
	}
	waitForOutput(t, &out, "three")
	appendToFile(t, path, "four\n")
	waitForOutput(t, &out, "four")

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("viewer did not stop after cancel")
	}
	got := out.String()
	for _, line := range []string{"one", "two", "three", "four"} {
		if n := strings.Count(got, line); n != 1 {
			t.Errorf("line %q printed %d times:\n%s", line, n, got)
		}
	}
}
