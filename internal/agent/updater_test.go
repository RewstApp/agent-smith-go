package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
	"github.com/hashicorp/go-hclog"
)

// digestOf renders the SHA-256 digest of content the way GitHub's Releases
// API formats an asset's digest field ("sha256:<64 lowercase hex chars>"),
// which is what parseAssetDigest expects.
func digestOf(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// releaseWithDigest builds a Release whose single binary asset is served by
// assetURL and carries the digest of content, the shape Download requires to
// succeed.
func releaseWithDigest(tagName, assetURL string, content []byte) Release {
	return Release{
		TagName: tagName,
		Assets: []Asset{
			{Id: 1, Name: testAssetFileName, Url: assetURL, Digest: digestOf(content)},
		},
	}
}

func newTestDevice() *Device {
	return &Device{
		RewstOrgId:           "test-org",
		LoggingLevel:         "info",
		UseSyslog:            false,
		DisableAgentPostback: false,
		DisableAutoUpdates:   false,
	}
}

// newTestUpdater builds an updater whose downloads land in a scratch directory
// instead of the org's real updates directory, which lives under the
// installation's data directory and is not writable by an unprivileged test
// process.
func newTestUpdater(
	t *testing.T,
	device *Device,
	latestReleaseUrl string,
	runCommand RunCommandFunc,
) *defaultUpdater {
	t.Helper()

	u := NewUpdater(hclog.NewNullLogger(), device, latestReleaseUrl, "", runCommand).(*defaultUpdater)
	u.updatesDir = t.TempDir()
	return u
}

func TestNewUpdater(t *testing.T) {
	logger := hclog.NewNullLogger()
	device := newTestDevice()
	runCmd := func(path string, args []string) error { return nil }

	updater := NewUpdater(logger, device, "http://example.com", "", runCmd)

	if updater == nil {
		t.Fatal("expected updater, got nil")
	}
}

func TestCheck_Success(t *testing.T) {
	release := Release{
		Id:      1,
		TagName: "v2.0.0",
		Assets:  []Asset{{Id: 1, Name: testAssetFileName, Url: "http://example.com"}},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := json.NewEncoder(w).Encode(release)
		if err != nil {
			t.Fatalf("exepcted no error, but got %v", err)
		}
	}))
	defer server.Close()

	logger := hclog.NewNullLogger()
	device := newTestDevice()
	updater := NewUpdater(logger, device, server.URL, "", nil)

	result, err := updater.Check(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result.TagName != release.TagName {
		t.Errorf("expected tag %s, got %s", release.TagName, result.TagName)
	}

	if len(result.Assets) != 1 {
		t.Errorf("expected 1 asset, got %d", len(result.Assets))
	}
}

func TestCheck_HttpError(t *testing.T) {
	logger := hclog.NewNullLogger()
	device := newTestDevice()
	updater := NewUpdater(logger, device, "http://invalid.invalid.invalid", "", nil)

	_, err := updater.Check(context.Background())

	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCheck_NonOkStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	logger := hclog.NewNullLogger()
	device := newTestDevice()
	updater := NewUpdater(logger, device, server.URL, "", nil)

	_, err := updater.Check(context.Background())

	if err == nil {
		t.Fatal("expected error for non-OK status")
	}
}

func TestCheck_InvalidJson(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write([]byte("not json"))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(err.Error()))
		}
	}))
	defer server.Close()

	logger := hclog.NewNullLogger()
	device := newTestDevice()
	updater := NewUpdater(logger, device, server.URL, "", nil)

	_, err := updater.Check(context.Background())

	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestUpdate_BuildsArgs(t *testing.T) {
	var capturedPath string
	var capturedArgs []string
	runCmd := func(path string, args []string) error {
		capturedPath = path
		capturedArgs = args
		return nil
	}

	device := &Device{
		RewstOrgId:           "org-123",
		LoggingLevel:         "debug",
		UseSyslog:            true,
		DisableAgentPostback: true,
		DisableAutoUpdates:   true,
	}

	logger := hclog.NewNullLogger()
	updater := NewUpdater(logger, device, "", "", runCmd)

	err := updater.Update("/path/to/binary")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if capturedPath != "/path/to/binary" {
		t.Errorf("expected path /path/to/binary, got %s", capturedPath)
	}

	expectedArgs := []string{
		"--org-id", "org-123",
		"--update",
		"--logging-level", "debug",
		"--syslog",
		"--disable-agent-postback",
		"--no-auto-updates",
	}

	if len(capturedArgs) != len(expectedArgs) {
		t.Fatalf("expected %d args, got %d: %v", len(expectedArgs), len(capturedArgs), capturedArgs)
	}

	for i, arg := range expectedArgs {
		if capturedArgs[i] != arg {
			t.Errorf("arg[%d]: expected %s, got %s", i, arg, capturedArgs[i])
		}
	}
}

func TestUpdate_MinimalArgs(t *testing.T) {
	var capturedArgs []string
	runCmd := func(path string, args []string) error {
		capturedArgs = args
		return nil
	}

	device := &Device{
		RewstOrgId:   "org-456",
		LoggingLevel: "info",
	}

	logger := hclog.NewNullLogger()
	updater := NewUpdater(logger, device, "", "", runCmd)

	err := updater.Update("/path/to/binary")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedArgs := []string{"--org-id", "org-456", "--update", "--logging-level", "info"}

	if len(capturedArgs) != len(expectedArgs) {
		t.Fatalf("expected %d args, got %d: %v", len(expectedArgs), len(capturedArgs), capturedArgs)
	}
}

func TestUpdate_RunCommandError(t *testing.T) {
	expectedErr := fmt.Errorf("command failed")
	runCmd := func(path string, args []string) error {
		return expectedErr
	}

	logger := hclog.NewNullLogger()
	device := newTestDevice()
	updater := NewUpdater(logger, device, "", "", runCmd)

	err := updater.Update("/path/to/binary")

	if err != expectedErr {
		t.Errorf("expected %v, got %v", expectedErr, err)
	}
}

func TestDownload_Success(t *testing.T) {
	fileContent := []byte("fake binary content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/octet-stream" {
			t.Errorf("expected Accept: application/octet-stream, got %s", r.Header.Get("Accept"))
		}

		_, _ = w.Write(fileContent)
	}))
	defer server.Close()

	updater := newTestUpdater(t, newTestDevice(), "", nil)

	release := releaseWithDigest("v99.0.0", server.URL, fileContent)
	asset := release.Assets[0]
	path, err := updater.Download(context.Background(), asset)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	defer func() {
		err = os.Remove(path)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	}()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected to read file, got %v", err)
	}

	if string(content) != string(fileContent) {
		t.Errorf("expected %s, got %s", fileContent, content)
	}

	// The download must land in the directory the agent owns and sweeps, under
	// the exact name pattern the sweep matches — the two halves of the fix only
	// work together.
	if dir := filepath.Dir(path); dir != updater.updatesDir {
		t.Errorf("expected the installer in %s, got %s", updater.updatesDir, dir)
	}
	if name := filepath.Base(path); !isInstallerFile(name) {
		t.Errorf("downloaded installer %q does not match the pattern the sweep reclaims", name)
	}
}

// The updates directory is created on demand, because a fresh install has never
// downloaded an update and nothing else creates it.
func TestDownload_CreatesMissingUpdatesDirectory(t *testing.T) {
	fileContent := []byte("fake binary content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fileContent)
	}))
	defer server.Close()

	u := newTestUpdater(t, newTestDevice(), "", nil)
	u.updatesDir = filepath.Join(u.updatesDir, "does-not-exist-yet", "updates")

	release := releaseWithDigest("v99.0.0", server.URL, fileContent)
	asset := release.Assets[0]
	path, err := u.Download(context.Background(), asset)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected the installer to exist at %s: %v", path, err)
	}
}

// A download that cannot create its directory must fail rather than silently
// falling back to a location nothing sweeps.
func TestDownload_UncreatableUpdatesDirectoryFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fake binary content"))
	}))
	defer server.Close()

	u := newTestUpdater(t, newTestDevice(), "", nil)

	// A regular file cannot also be a directory, on any platform.
	blocker := filepath.Join(u.updatesDir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), utils.DefaultFileMod); err != nil {
		t.Fatalf("failed to write blocker file: %v", err)
	}
	u.updatesDir = filepath.Join(blocker, "updates")

	path, err := u.Download(context.Background(), Asset{Url: server.URL})
	if err == nil {
		t.Fatal("expected an error when the updates directory cannot be created, got nil")
	}
	if path != "" {
		t.Errorf("expected empty path on error, got %s", path)
	}
}

func TestDownload_HttpError(t *testing.T) {
	updater := newTestUpdater(t, newTestDevice(), "", nil)

	_, err := updater.Download(
		context.Background(),
		Asset{Url: "http://invalid.invalid.invalid"},
	)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDownload_ChmodFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fake binary content"))
	}))
	defer server.Close()

	u := newTestUpdater(t, newTestDevice(), "", nil)

	var capturedTempPath string
	u.chmod = func(name string, mode os.FileMode) error {
		capturedTempPath = name
		return fmt.Errorf("chmod not supported on this filesystem")
	}

	path, err := u.Download(context.Background(), Asset{Url: server.URL})

	if err == nil {
		t.Fatal("expected error from chmod failure, got nil")
	}

	if path != "" {
		t.Errorf("expected empty path on error, got %s", path)
	}

	if capturedTempPath == "" {
		t.Fatal("chmod mock was never called; test setup is broken")
	}

	// The temp file must not exist after the error — core assertion of the bug fix
	if _, statErr := os.Stat(capturedTempPath); !os.IsNotExist(statErr) {
		t.Errorf(
			"expected temp file %s to be removed after chmod failure, but it still exists",
			capturedTempPath,
		)
		_ = os.Remove(capturedTempPath)
	}
}

func TestDownload_NonOkStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	updater := newTestUpdater(t, newTestDevice(), "", nil)

	_, err := updater.Download(context.Background(), Asset{Url: server.URL})

	if err == nil {
		t.Fatal("expected error for non-OK status")
	}
}

// A tampered or corrupted download must never be handed to Update — this is
// the core assertion of sc-108851.
func TestDownload_ChecksumMismatchRejectsInstaller(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("tampered binary content"))
	}))
	defer server.Close()

	u := newTestUpdater(t, newTestDevice(), "", nil)

	// The asset's digest is computed over different bytes than the server
	// actually returns, simulating a corrupted-but-200-OK download or a
	// tampered release asset.
	asset := Asset{
		Name:   testAssetFileName,
		Url:    server.URL,
		Digest: digestOf([]byte("original binary content")),
	}
	path, err := u.Download(context.Background(), asset)

	if err == nil {
		t.Fatal("expected checksum mismatch error, got nil")
	}
	if path != "" {
		t.Errorf("expected empty path on checksum mismatch, got %s", path)
	}

	entries, readErr := os.ReadDir(u.updatesDir)
	if readErr != nil {
		t.Fatalf("failed to read updates dir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Errorf("expected the rejected installer to be cleaned up, found %d entries", len(entries))
	}
}

// An asset with no digest at all must fail closed rather than installing the
// binary unverified.
func TestDownload_MissingDigestFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fake binary content"))
	}))
	defer server.Close()

	u := newTestUpdater(t, newTestDevice(), "", nil)

	asset := Asset{Name: testAssetFileName, Url: server.URL}
	path, err := u.Download(context.Background(), asset)

	if err == nil {
		t.Fatal("expected error for missing digest, got nil")
	}
	if path != "" {
		t.Errorf("expected empty path on error, got %s", path)
	}
}

// A malformed digest (wrong algorithm prefix or non-hex/wrong-length hash)
// must also fail closed rather than skip verification.
func TestDownload_MalformedDigestFails(t *testing.T) {
	tests := []struct {
		name   string
		digest string
	}{
		{name: "wrong algorithm", digest: "sha1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{name: "no prefix", digest: strings.Repeat("a", 64)},
		{name: "not hex", digest: "sha256:" + strings.Repeat("z", 64)},
		{name: "wrong length", digest: "sha256:abcd"},
		{name: "empty", digest: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = w.Write([]byte("fake binary content"))
				}),
			)
			defer server.Close()

			u := newTestUpdater(t, newTestDevice(), "", nil)

			asset := Asset{Name: testAssetFileName, Url: server.URL, Digest: tt.digest}
			path, err := u.Download(context.Background(), asset)

			if err == nil {
				t.Fatal("expected error for malformed digest, got nil")
			}
			if path != "" {
				t.Errorf("expected empty path on error, got %s", path)
			}
		})
	}
}

// An installer larger than maxInstallerDownloadSize must be rejected rather
// than filling the updates directory (and the volume under it).
func TestDownload_OversizedInstallerFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 64*1024)
		var written int64
		for written <= maxInstallerDownloadSize {
			n, err := w.Write(buf)
			written += int64(n)
			if err != nil {
				return
			}
		}
	}))
	defer server.Close()

	u := newTestUpdater(t, newTestDevice(), "", nil)

	asset := Asset{Name: testAssetFileName, Url: server.URL}
	path, err := u.Download(context.Background(), asset)

	if err == nil {
		t.Fatal("expected error for oversized download, got nil")
	}
	if path != "" {
		t.Errorf("expected empty path on error, got %s", path)
	}

	entries, readErr := os.ReadDir(u.updatesDir)
	if readErr != nil {
		t.Fatalf("failed to read updates dir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Errorf("expected the oversized download to be cleaned up, found %d entries", len(entries))
	}
}

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
		wantErr bool
	}{
		{name: "patch newer", current: "v1.2.3", latest: "v1.2.4", want: true},
		{name: "minor newer", current: "v1.2.3", latest: "v1.3.0", want: true},
		{name: "major newer", current: "v1.2.3", latest: "v2.0.0", want: true},
		{name: "equal versions", current: "v1.2.3", latest: "v1.2.3", want: false},
		// The bug this guards against: a release feed pointed at (or
		// mistakenly republishing) an older tag must never be treated as an
		// update to install.
		{name: "older patch", current: "v1.2.4", latest: "v1.2.3", want: false},
		{name: "older major", current: "v2.0.0", latest: "v1.9.9", want: false},
		{name: "missing v prefix tolerated", current: "1.2.3", latest: "1.2.4", want: true},
		{name: "prerelease suffix ignored", current: "v1.2.3", latest: "v1.2.4-rc.1", want: true},
		{name: "malformed current", current: "not-a-version", latest: "v1.2.3", wantErr: true},
		{name: "malformed latest", current: "v1.2.3", latest: "not-a-version", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := isNewerVersion(tt.current, tt.latest)
			if tt.wantErr {
				if err == nil {
					t.Fatalf(
						"expected error for current=%s latest=%s, got nil",
						tt.current, tt.latest,
					)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if got != tt.want {
				t.Errorf(
					"isNewerVersion(%s, %s) = %v, want %v",
					tt.current, tt.latest, got, tt.want,
				)
			}
		})
	}
}

// A release tag that fails to parse must abort the update rather than
// silently skip it or silently treat it as newer.
func TestRun_UnparsableLatestVersionFails(t *testing.T) {
	release := Release{TagName: "not-a-version"}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := json.NewEncoder(w).Encode(release)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	}))
	defer server.Close()

	logger := hclog.NewNullLogger()
	device := newTestDevice()
	updater := NewUpdater(logger, device, server.URL, "", nil)

	err := updater.Run(context.Background())

	if err == nil {
		t.Fatal("expected error for unparsable latest version")
	}
}

// A release tag older than the running version must not trigger a downgrade.
func TestRun_OlderTagDoesNotDowngrade(t *testing.T) {
	var downloadCalled bool
	downloadServer := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			downloadCalled = true
			_, _ = w.Write([]byte("fake binary"))
		},
	))
	defer downloadServer.Close()

	// Point an "older" release at a version guaranteed to be below whatever
	// version.Version resolves to in this build (the unset placeholder
	// "0.0.0" in a plain `go test` run, or a real semver in a built binary).
	release := Release{
		TagName: "v0.0.0",
		Assets:  []Asset{{Id: 1, Name: testAssetFileName, Url: downloadServer.URL}},
	}

	releaseServer := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			err := json.NewEncoder(w).Encode(release)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		},
	))
	defer releaseServer.Close()

	logger := hclog.NewNullLogger()
	device := newTestDevice()
	updater := NewUpdater(logger, device, releaseServer.URL, "", nil)

	isNewer, err := isNewerVersion(version.Version, release.TagName)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if isNewer {
		t.Skip("v0.0.0 is not older than the version this build resolves to; nothing to assert")
	}

	err = updater.Run(context.Background())
	if err != nil {
		t.Fatalf("expected no error (no update available), got %v", err)
	}
	if downloadCalled {
		t.Error("expected Download not to be called for a non-newer release tag")
	}
}

// mockUpdater implements Updater for testing AutoUpdateRunner
type mockUpdater struct {
	runErr     error
	runFn      func(ctx context.Context) error
	runCount   int
	checkFn    func() (Release, error)
	updateFn   func(string) error
	selectFn   func(Release) (Asset, error)
	downloadFn func(Asset) (string, error)
}

func (m *mockUpdater) Run(ctx context.Context) error {
	m.runCount++
	if m.runFn != nil {
		return m.runFn(ctx)
	}
	return m.runErr
}

func (m *mockUpdater) Check(ctx context.Context) (Release, error) {
	if m.checkFn != nil {
		return m.checkFn()
	}
	return Release{}, nil
}

func (m *mockUpdater) Update(path string) error {
	if m.updateFn != nil {
		return m.updateFn(path)
	}
	return nil
}

func (m *mockUpdater) SelectAsset(release Release) (Asset, error) {
	if m.selectFn != nil {
		return m.selectFn(release)
	}
	return Asset{}, nil
}

func (m *mockUpdater) Download(ctx context.Context, asset Asset) (string, error) {
	if m.downloadFn != nil {
		return m.downloadFn(asset)
	}
	return "", nil
}

func TestNewAutoUpdateRunner(t *testing.T) {
	logger := hclog.NewNullLogger()
	mock := &mockUpdater{}

	runner := NewAutoUpdateRunner(logger, mock, time.Hour, 3, time.Second)

	if runner == nil {
		t.Fatal("expected runner, got nil")
	}

	if runner.interval != time.Hour {
		t.Errorf("expected interval 1h, got %v", runner.interval)
	}

	if runner.maxRetries != 3 {
		t.Errorf("expected maxRetries 3, got %d", runner.maxRetries)
	}

	if runner.baseBackoff != time.Second {
		t.Errorf("expected baseBackoff 1s, got %v", runner.baseBackoff)
	}
}

func TestAutoUpdateRunner_StartAndStop(t *testing.T) {
	logger := hclog.NewNullLogger()
	mock := &mockUpdater{}

	runner := NewAutoUpdateRunner(logger, mock, 10*time.Millisecond, 3, time.Millisecond)

	done := make(chan struct{})
	go func() {
		runner.Start()
		close(done)
	}()

	// Wait for at least one run
	time.Sleep(50 * time.Millisecond)
	runner.Stop()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop in time")
	}

	if mock.runCount == 0 {
		t.Error("expected at least one run")
	}
}

func TestAutoUpdateRunner_StopBeforeFirstRun(t *testing.T) {
	logger := hclog.NewNullLogger()
	mock := &mockUpdater{}

	runner := NewAutoUpdateRunner(logger, mock, time.Hour, 3, time.Millisecond)

	done := make(chan struct{})
	go func() {
		runner.Start()
		close(done)
	}()

	runner.Stop()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop in time")
	}

	if mock.runCount != 0 {
		t.Errorf("expected 0 runs, got %d", mock.runCount)
	}
}

func TestAutoUpdateRunner_RetryOnFailure(t *testing.T) {
	logger := hclog.NewNullLogger()
	mock := &mockUpdater{
		runErr: fmt.Errorf("update failed"),
	}

	runner := NewAutoUpdateRunner(logger, mock, 10*time.Millisecond, 3, time.Millisecond)

	done := make(chan struct{})
	go func() {
		runner.Start()
		close(done)
	}()

	// Wait for retries to happen
	time.Sleep(100 * time.Millisecond)
	runner.Stop()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop in time")
	}

	// Should have run at least once (initial) + retries
	if mock.runCount < 2 {
		t.Errorf("expected at least 2 runs for retry, got %d", mock.runCount)
	}
}

func TestAutoUpdateRunner_RetriesExhausted(t *testing.T) {
	logger := hclog.NewNullLogger()
	mock := &mockUpdater{
		runErr: fmt.Errorf("always fails"),
	}

	maxRetries := 2
	runner := NewAutoUpdateRunner(logger, mock, 10*time.Millisecond, maxRetries, time.Millisecond)

	done := make(chan struct{})
	go func() {
		runner.Start()
		close(done)
	}()

	// Wait for initial run + retries + next cycle
	time.Sleep(200 * time.Millisecond)
	runner.Stop()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop in time")
	}

	// Initial run + maxRetries per cycle, possibly multiple cycles
	// At minimum: 1 initial + 2 retries = 3
	if mock.runCount < 1+maxRetries {
		t.Errorf("expected at least %d runs, got %d", 1+maxRetries, mock.runCount)
	}
}

func TestAutoUpdateRunner_RetrySucceedsAfterFailures(t *testing.T) {
	logger := hclog.NewNullLogger()
	failsBeforeSuccess := 2
	mock := &mockUpdater{}
	mock.runFn = func(ctx context.Context) error {
		if mock.runCount <= failsBeforeSuccess {
			return fmt.Errorf("temporary failure")
		}
		return nil
	}

	runner := NewAutoUpdateRunner(logger, mock, 10*time.Millisecond, 5, time.Millisecond)

	done := make(chan struct{})
	go func() {
		runner.Start()
		close(done)
	}()

	// Wait for initial failure + retries + resumed normal interval
	time.Sleep(100 * time.Millisecond)
	runner.Stop()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop in time")
	}

	// Should have run: 1 initial fail + 1 retry fail + 1 retry success + at least 1 normal cycle
	if mock.runCount < failsBeforeSuccess+1 {
		t.Errorf("expected at least %d runs, got %d", failsBeforeSuccess+1, mock.runCount)
	}
}

func TestAutoUpdateRunner_StopDuringBackoff(t *testing.T) {
	logger := hclog.NewNullLogger()
	mock := &mockUpdater{
		runErr: fmt.Errorf("fails"),
	}

	// Use a long backoff so we can stop during it. The cap is normally derived
	// from the (short) check interval, so raise it here to keep the retry slot
	// long enough that the stop, not the timer, is what ends the wait.
	runner := NewAutoUpdateRunner(logger, mock, 10*time.Millisecond, 5, time.Hour)
	runner.maxBackoff = time.Hour

	done := make(chan struct{})
	go func() {
		runner.Start()
		close(done)
	}()

	// Wait for initial failure to trigger backoff
	time.Sleep(50 * time.Millisecond)
	runner.Stop()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop during backoff")
	}
}

func TestRun_FullUpdateFlow(t *testing.T) {
	var updatedPath string
	runCmd := func(path string, args []string) error {
		updatedPath = path
		return nil
	}

	binaryContent := []byte("fake binary")

	// Serve the binary download endpoint
	downloadServer := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(binaryContent)
		}),
	)
	defer downloadServer.Close()

	// Serve the release check endpoint
	release := releaseWithDigest("v99.0.0", downloadServer.URL, binaryContent)

	releaseServer := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			err := json.NewEncoder(w).Encode(release)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		}),
	)
	defer releaseServer.Close()

	updater := newTestUpdater(t, newTestDevice(), releaseServer.URL, runCmd)

	err := updater.Run(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if updatedPath == "" {
		t.Fatal("expected Update to be called with a path")
	}

	// Verify the downloaded file exists
	_, statErr := os.Stat(updatedPath)
	if statErr != nil {
		t.Errorf("expected downloaded file to exist at %s, got %v", updatedPath, statErr)
	}
	err = os.Remove(updatedPath)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestRun_SelectAssetError(t *testing.T) {
	// Release with no matching asset for the current platform
	release := Release{
		TagName: "v99.0.0",
		Assets:  []Asset{{Id: 1, Name: "agent.unknown.pkg", Url: "http://example.com"}},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := json.NewEncoder(w).Encode(release)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(err.Error()))
		}
	}))
	defer server.Close()

	logger := hclog.NewNullLogger()
	device := newTestDevice()
	updater := NewUpdater(logger, device, server.URL, "", nil)

	err := updater.Run(context.Background())

	if err == nil {
		t.Fatal("expected error for no matching asset")
	}
}

func TestRun_DownloadError(t *testing.T) {
	// Use an unreachable URL so the HTTP request fails
	release := Release{
		TagName: "v99.0.0",
		Assets:  []Asset{{Id: 1, Name: testAssetFileName, Url: "http://invalid.invalid.invalid"}},
	}

	releaseServer := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			err := json.NewEncoder(w).Encode(release)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		}),
	)
	defer releaseServer.Close()

	logger := hclog.NewNullLogger()
	device := newTestDevice()
	updater := NewUpdater(logger, device, releaseServer.URL, "", nil)

	err := updater.Run(context.Background())

	if err == nil {
		t.Fatal("expected error for download failure")
	}
}

func TestRun_UpdateCommandError(t *testing.T) {
	runCmd := func(path string, args []string) error {
		return fmt.Errorf("command failed")
	}

	binaryContent := []byte("fake binary")
	downloadServer := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(binaryContent)
		}),
	)
	defer downloadServer.Close()

	release := releaseWithDigest("v99.0.0", downloadServer.URL, binaryContent)

	releaseServer := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			err := json.NewEncoder(w).Encode(release)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		}),
	)
	defer releaseServer.Close()

	updater := newTestUpdater(t, newTestDevice(), releaseServer.URL, runCmd)

	err := updater.Run(context.Background())

	if err == nil {
		t.Fatal("expected error for command failure")
	}

	if err.Error() != "command failed" {
		t.Errorf("expected 'command failed', got %v", err)
	}
}

func TestRun_CheckError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	logger := hclog.NewNullLogger()
	device := newTestDevice()
	updater := NewUpdater(logger, device, server.URL, "", nil)

	err := updater.Run(context.Background())

	if err == nil {
		t.Fatal("expected error for check failure")
	}
}

func TestRun_NoUpdateAvailable(t *testing.T) {
	release := Release{
		TagName: version.Version,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := json.NewEncoder(w).Encode(release)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	}))
	defer server.Close()

	logger := hclog.NewNullLogger()
	device := newTestDevice()
	updater := NewUpdater(logger, device, server.URL, "", nil)

	err := updater.Run(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestCheck_Timeout(t *testing.T) {
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-done:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(done) // unblocks handler before server.Close() drains connections

	logger := hclog.NewNullLogger()
	device := newTestDevice()
	u := NewUpdater(logger, device, server.URL, "", nil).(*defaultUpdater)
	u.checkClient = &http.Client{Timeout: 50 * time.Millisecond}

	_, err := u.Check(context.Background())

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestDownload_Timeout(t *testing.T) {
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-done:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(done) // unblocks handler before server.Close() drains connections

	u := newTestUpdater(t, newTestDevice(), "", nil)
	u.downloadClient = &http.Client{Timeout: 50 * time.Millisecond}

	_, err := u.Download(context.Background(), Asset{Url: server.URL})

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

// TestCheck_ContextCancelled verifies that cancelling the context aborts an
// in-flight update check promptly rather than waiting for the client Timeout.
func TestCheck_ContextCancelled(t *testing.T) {
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-done:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(done) // unblocks handler before server.Close() drains connections

	logger := hclog.NewNullLogger()
	device := newTestDevice()
	// Retain the production check timeout as the upper bound; cancellation must
	// abort well before it elapses.
	u := NewUpdater(logger, device, server.URL, "", nil).(*defaultUpdater)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := u.Check(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if elapsed >= 2*time.Second {
		t.Errorf("expected cancellation to abort within 2s, took %v", elapsed)
	}
}

// TestDownload_ContextCancelled verifies that cancelling the context aborts an
// in-flight download promptly rather than waiting for the client Timeout.
func TestDownload_ContextCancelled(t *testing.T) {
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-done:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(done) // unblocks handler before server.Close() drains connections

	u := newTestUpdater(t, newTestDevice(), "", nil)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := u.Download(ctx, Asset{Url: server.URL})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if elapsed >= 2*time.Second {
		t.Errorf("expected cancellation to abort within 2s, took %v", elapsed)
	}
}

// TestAutoUpdateRunner_StopCancelsInFlightRun verifies that Stop cancels the
// context handed to an in-flight Run, so a stop/restart issued during an update
// download is honored promptly instead of blocking on the client Timeout.
func TestAutoUpdateRunner_StopCancelsInFlightRun(t *testing.T) {
	logger := hclog.NewNullLogger()

	runStarted := make(chan struct{})
	mock := &mockUpdater{}
	mock.runFn = func(ctx context.Context) error {
		close(runStarted)
		<-ctx.Done() // block until the runner cancels us
		return ctx.Err()
	}

	runner := NewAutoUpdateRunner(logger, mock, time.Millisecond, 3, time.Millisecond)

	done := make(chan struct{})
	go func() {
		runner.Start()
		close(done)
	}()

	// Wait until Run is in flight, then stop and confirm it unblocks promptly.
	<-runStarted

	stopReturned := make(chan struct{})
	go func() {
		runner.Stop()
		close(stopReturned)
	}()

	select {
	case <-stopReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not cancel in-flight Run within 2s")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop in time")
	}
}
