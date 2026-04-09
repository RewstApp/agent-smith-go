package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/version"
	"github.com/hashicorp/go-hclog"
)

func newTestDevice() *Device {
	return &Device{
		RewstOrgId:           "test-org",
		LoggingLevel:         "info",
		UseSyslog:            false,
		DisableAgentPostback: false,
		DisableAutoUpdates:   false,
	}
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

	result, err := updater.Check()
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

	_, err := updater.Check()

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

	_, err := updater.Check()

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

	_, err := updater.Check()

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

	logger := hclog.NewNullLogger()
	device := newTestDevice()
	updater := NewUpdater(logger, device, "", "", nil)

	path, err := updater.Download(Asset{Url: server.URL})
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
}

func TestDownload_HttpError(t *testing.T) {
	logger := hclog.NewNullLogger()
	device := newTestDevice()
	updater := NewUpdater(logger, device, "", "", nil)

	_, err := updater.Download(Asset{Url: "http://invalid.invalid.invalid"})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDownload_ChmodFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fake binary content"))
	}))
	defer server.Close()

	logger := hclog.NewNullLogger()
	device := newTestDevice()
	u := NewUpdater(logger, device, "", "", nil).(*defaultUpdater)

	var capturedTempPath string
	u.chmod = func(name string, mode os.FileMode) error {
		capturedTempPath = name
		return fmt.Errorf("chmod not supported on this filesystem")
	}

	path, err := u.Download(Asset{Url: server.URL})

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

	logger := hclog.NewNullLogger()
	device := newTestDevice()
	updater := NewUpdater(logger, device, "", "", nil)

	_, err := updater.Download(Asset{Url: server.URL})

	if err == nil {
		t.Fatal("expected error for non-OK status")
	}
}

// mockUpdater implements Updater for testing AutoUpdateRunner
type mockUpdater struct {
	runErr     error
	runFn      func() error
	runCount   int
	checkFn    func() (Release, error)
	updateFn   func(string) error
	selectFn   func(Release) (Asset, error)
	downloadFn func(Asset) (string, error)
}

func (m *mockUpdater) Run() error {
	m.runCount++
	if m.runFn != nil {
		return m.runFn()
	}
	return m.runErr
}

func (m *mockUpdater) Check() (Release, error) {
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

func (m *mockUpdater) Download(asset Asset) (string, error) {
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
	mock.runFn = func() error {
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

	// Use a long backoff so we can stop during it
	runner := NewAutoUpdateRunner(logger, mock, 10*time.Millisecond, 5, time.Hour)

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

	// Serve the release check endpoint
	release := Release{
		TagName: "v99.0.0",
		Assets:  []Asset{{Id: 1, Name: testAssetFileName, Url: "PLACEHOLDER"}},
	}

	// Serve the binary download endpoint
	downloadServer := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("fake binary"))
		}),
	)
	defer downloadServer.Close()

	release.Assets[0].Url = downloadServer.URL

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
	updater := NewUpdater(logger, device, releaseServer.URL, "", runCmd)

	err := updater.Run()
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

	err := updater.Run()

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

	err := updater.Run()

	if err == nil {
		t.Fatal("expected error for download failure")
	}
}

func TestRun_UpdateCommandError(t *testing.T) {
	runCmd := func(path string, args []string) error {
		return fmt.Errorf("command failed")
	}

	downloadServer := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("fake binary"))
		}),
	)
	defer downloadServer.Close()

	release := Release{
		TagName: "v99.0.0",
		Assets:  []Asset{{Id: 1, Name: testAssetFileName, Url: downloadServer.URL}},
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
	updater := NewUpdater(logger, device, releaseServer.URL, "", runCmd)

	err := updater.Run()

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

	err := updater.Run()

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

	err := updater.Run()
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

	_, err := u.Check()

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

	logger := hclog.NewNullLogger()
	device := newTestDevice()
	u := NewUpdater(logger, device, "", "", nil).(*defaultUpdater)
	u.downloadClient = &http.Client{Timeout: 50 * time.Millisecond}

	_, err := u.Download(Asset{Url: server.URL})

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}
