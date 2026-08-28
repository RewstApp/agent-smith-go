package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
	"github.com/hashicorp/go-hclog"
)

type Asset struct {
	Id   int    `json:"id"`
	Name string `json:"name"`
	Url  string `json:"url"`
}

type Release struct {
	Id      int     `json:"id"`
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

type Updater interface {
	Check(ctx context.Context) (Release, error)
	Update(updaterExecutablePath string) error
	SelectAsset(release Release) (Asset, error)
	Download(ctx context.Context, release Release, asset Asset) (string, error)
	Run(ctx context.Context) error
}

// updateIntervalStr is overridable via -ldflags for integration testing.
// Example: -ldflags "-X github.com/RewstApp/agent-smith-go/internal/agent.updateIntervalStr=30s"
var updateIntervalStr = ""

// baseBackoffStr is overridable via -ldflags for integration testing.
// Example: -ldflags "-X github.com/RewstApp/agent-smith-go/internal/agent.baseBackoffStr=5s"
var baseBackoffStr = ""

// maxRetriesStr is overridable via -ldflags for integration testing.
// Example: -ldflags "-X github.com/RewstApp/agent-smith-go/internal/agent.maxRetriesStr=3"
var maxRetriesStr = ""

// releaseUrlOverrideFileStr names a file inside the org's data directory whose
// contents replace the compiled-in release endpoint. It is overridable via
// -ldflags for integration testing, which points the auto-updater at a local
// stub release endpoint so the retry schedule can be observed against an
// endpoint that fails on demand (sc-106110); a real GitHub outage is not
// something CI can arrange.
// Example: -ldflags "-X github.com/RewstApp/agent-smith-go/internal/agent.releaseUrlOverrideFileStr=release_url_override"
//
// Released builds leave it empty and then never look for the file at all, so the
// update source of a shipped agent is fixed at build time and cannot be
// redirected by dropping a file on an endpoint.
var releaseUrlOverrideFileStr = ""

const (
	defaultUpdateInterval = 48 * time.Hour
	defaultBaseBackoff    = 5 * time.Minute
	defaultMaxRetries     = 5

	// DefaultUpdateMaxRetryBackoff caps the exponential-backoff delay between
	// auto-update retries regardless of how high the retry count is raised. The
	// schedule doubles the base delay on every retry (base * 2^n); without a
	// ceiling, the production base of 5 minutes reaches a 42-hour sleep by
	// attempt 10 — longer than the update check interval the retries are nested
	// inside — and a large injected maxRetries overflows the doubling into a
	// negative time.Duration that makes time.After fire immediately and the retry
	// loop busy-spin against the release endpoint. This mirrors
	// DefaultPostbackMaxRetryBackoff and maxTimeout in the reconnect backoff
	// generator (see internal/utils/time.go): the per-slot wait is bounded so the
	// total retry window still widens with more retries, but no single sleep can
	// overflow, outlive the check interval, or collapse into a tight loop.
	DefaultUpdateMaxRetryBackoff = 1 * time.Hour

	// updateBackoffIntervalDivisor bounds a single retry slot to this fraction of
	// the update check interval, so the retry schedule always stays meaningfully
	// nested inside the interval it belongs to rather than outliving it. It
	// matters most for short intervals (integration builds shorten the interval
	// via ldflags); at the production 48-hour interval the absolute
	// DefaultUpdateMaxRetryBackoff ceiling is far lower and wins.
	updateBackoffIntervalDivisor = 4
)

// updateMaxRetryBackoff returns the ceiling applied to every auto-update retry
// slot for the given check interval: the lower of DefaultUpdateMaxRetryBackoff
// and one updateBackoffIntervalDivisor-th of the interval. A non-positive
// interval (no meaningful schedule to nest inside) yields the absolute ceiling.
func updateMaxRetryBackoff(interval time.Duration) time.Duration {
	maxBackoff := DefaultUpdateMaxRetryBackoff
	if interval > 0 && interval/updateBackoffIntervalDivisor < maxBackoff {
		maxBackoff = interval / updateBackoffIntervalDivisor
	}
	if maxBackoff <= 0 {
		maxBackoff = DefaultUpdateMaxRetryBackoff
	}
	return maxBackoff
}

// DefaultUpdateInterval returns the auto-update check interval.
// Uses updateIntervalStr if set via ldflags, otherwise defaults to 48 hours.
func DefaultUpdateInterval() time.Duration {
	if updateIntervalStr != "" {
		if d, err := time.ParseDuration(updateIntervalStr); err == nil {
			return d
		}
	}
	return defaultUpdateInterval
}

// DefaultBaseBackoff returns the base backoff duration for update retries.
// Uses baseBackoffStr if set via ldflags, otherwise defaults to 5 minutes.
func DefaultBaseBackoff() time.Duration {
	if baseBackoffStr != "" {
		if d, err := time.ParseDuration(baseBackoffStr); err == nil {
			return d
		}
	}
	return defaultBaseBackoff
}

// DefaultMaxRetries returns the maximum number of update retry attempts.
// Uses maxRetriesStr if set via ldflags, otherwise defaults to 5.
func DefaultMaxRetries() int {
	if maxRetriesStr != "" {
		var n int
		if _, err := fmt.Sscanf(maxRetriesStr, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	return defaultMaxRetries
}

// ResolveLatestReleaseUrl returns the endpoint the auto-updater should query for
// the latest release: defaultUrl for released builds, and for an
// integration-test build the URL named by releaseUrlOverrideFileStr when that
// file exists in the org's data directory. A missing, unreadable, or empty
// override file falls back to defaultUrl, so a fixture that failed to write it
// leaves the agent updating normally rather than silently not updating at all.
func ResolveLatestReleaseUrl(logger hclog.Logger, orgId string, defaultUrl string) string {
	return resolveLatestReleaseUrl(logger, GetDataDirectory(orgId), defaultUrl)
}

func resolveLatestReleaseUrl(logger hclog.Logger, dataDir string, defaultUrl string) string {
	if releaseUrlOverrideFileStr == "" {
		return defaultUrl
	}

	path := filepath.Join(dataDir, releaseUrlOverrideFileStr)
	contents, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warn("Failed to read release url override", "path", path, "error", err)
		}
		return defaultUrl
	}

	url := strings.TrimSpace(string(contents))
	if url == "" {
		return defaultUrl
	}

	logger.Info("Using release url override", "path", path, "url", url)
	return url
}

type RunCommandFunc = func(path string, args []string) error

const (
	checkTimeout    = 30 * time.Second
	downloadTimeout = 5 * time.Minute

	// updatesDirMod is the mode of the directory downloaded installers are
	// written to. It is deliberately tighter than utils.DefaultDirMod: the agent
	// runs as root/SYSTEM and is the only thing that ever reads these files, so
	// nothing is gained by letting every local account traverse a directory that
	// holds executable agent binaries. Ignored on Windows, where the directory
	// inherits the ACL of the data directory it is created under.
	updatesDirMod os.FileMode = 0o700

	// maxInstallerDownloadSize bounds how many bytes Download will accept for the
	// installer binary. Agent Smith's compiled binaries are tens of megabytes;
	// this ceiling is generous headroom above that so a legitimate release is
	// never at risk, while a misbehaving or compromised release endpoint cannot
	// fill the updates directory (and the volume under it) by serving an
	// oversized or endless body — downloadTimeout bounds how long the request
	// runs but not how many bytes a slow-but-still-connected sender can push in
	// that window.
	maxInstallerDownloadSize int64 = 200 * 1024 * 1024

	// checksumAssetSuffix is appended to a binary asset's name to find its
	// published checksum sidecar in the same release, e.g.
	// "rewst_agent_config.linux.bin.sha256" alongside
	// "rewst_agent_config.linux.bin". See .github/workflows/sign.yml, which
	// computes and uploads this file for every release asset.
	checksumAssetSuffix = ".sha256"

	// maxChecksumFileSize bounds how many bytes Download will read for a
	// checksum sidecar file. The published files are a few lines of text; this
	// is generous headroom while still preventing an unbounded read.
	maxChecksumFileSize int64 = 4096
)

type defaultUpdater struct {
	logger           hclog.Logger
	device           *Device
	latestReleaseUrl string
	githubToken      string
	runCommand       RunCommandFunc
	checkClient      *http.Client
	downloadClient   *http.Client
	chmod            func(name string, mode os.FileMode) error
	// updatesDir is where Download writes the installer binary. It is resolved
	// once at construction from the device's org id so tests can point it at a
	// scratch directory instead of the real installation path.
	updatesDir string
}

func NewUpdater(
	logger hclog.Logger,
	device *Device,
	latestReleaseUrl string,
	githubToken string,
	runCommand RunCommandFunc,
) Updater {
	return &defaultUpdater{
		logger:           logger,
		device:           device,
		latestReleaseUrl: latestReleaseUrl,
		githubToken:      githubToken,
		runCommand:       runCommand,
		checkClient:      &http.Client{Timeout: checkTimeout},
		downloadClient:   &http.Client{Timeout: downloadTimeout},
		chmod:            os.Chmod,
		updatesDir:       GetUpdatesDirectory(device.RewstOrgId),
	}
}

func (u *defaultUpdater) Check(ctx context.Context) (Release, error) {
	release := Release{}
	u.logger.Info("Checking for updates")
	if u.githubToken != "" {
		u.logger.Info("GitHub token provided for update check")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.latestReleaseUrl, nil)
	if err != nil {
		return release, err
	}
	if u.githubToken != "" {
		req.Header.Set("Authorization", "Bearer "+u.githubToken)
	}

	resp, err := u.checkClient.Do(req)
	if err != nil {
		u.logger.Error("Failed to fetch latest release", "url", u.latestReleaseUrl, "error", err)
		return release, err
	}
	defer func() {
		err = resp.Body.Close()
		if err != nil {
			u.logger.Error("Failed to close response", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		u.logger.Error(
			"Failed to fetch latest release",
			"url",
			u.latestReleaseUrl,
			"status",
			resp.StatusCode,
		)
		return release, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		u.logger.Error("Failed to parse release", "error", err)
		return release, err
	}

	return release, nil
}

func (u *defaultUpdater) Update(updaterExecutablePath string) error {
	args := []string{
		"--org-id",
		u.device.RewstOrgId,
		"--update",
		"--logging-level",
		string(u.device.LoggingLevel),
	}

	if u.device.UseSyslog {
		args = append(args, "--syslog")
	}

	if u.device.DisableAgentPostback {
		args = append(args, "--disable-agent-postback")
	}

	if u.device.DisableAutoUpdates {
		args = append(args, "--no-auto-updates")
	}

	u.logger.Debug("Running update command", "path", updaterExecutablePath, "args", args)

	return u.runCommand(updaterExecutablePath, args)
}

func (u *defaultUpdater) Download(
	ctx context.Context,
	release Release,
	asset Asset,
) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.Url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Add("Accept", "application/octet-stream")
	if u.githubToken != "" {
		req.Header.Set("Authorization", "Bearer "+u.githubToken)
	}

	resp, err := u.downloadClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status code: %d", resp.StatusCode)
	}

	// The installer is written to a directory this agent owns rather than the
	// shared system temp directory, so the startup sweep that reclaims it can
	// never reach a file another program wrote and the binary is not left
	// executable in a world-readable location. See GetUpdatesDirectory. The
	// directory is created on demand because a fresh install has never
	// downloaded an update.
	if err := os.MkdirAll(u.updatesDir, updatesDirMod); err != nil {
		return "", fmt.Errorf("failed to create updates directory %s: %w", u.updatesDir, err)
	}

	file, err := os.CreateTemp(u.updatesDir, installerTempPattern)
	if err != nil {
		return "", err
	}

	success := false
	defer func() {
		_ = file.Close()
		if !success {
			if removeErr := os.Remove(file.Name()); removeErr != nil {
				u.logger.Error(
					"Failed to remove temp installer file",
					"path", file.Name(),
					"error", removeErr,
				)
			}
		}
	}()

	hasher := sha256.New()
	written, err := io.Copy(
		io.MultiWriter(file, hasher),
		io.LimitReader(resp.Body, maxInstallerDownloadSize+1),
	)
	if err != nil {
		return "", err
	}
	if written > maxInstallerDownloadSize {
		return "", fmt.Errorf(
			"installer download for %s exceeds maximum allowed size of %d bytes",
			asset.Name, maxInstallerDownloadSize,
		)
	}
	if err = u.chmod(file.Name(), utils.DefaultExecutableFileMod); err != nil {
		return "", fmt.Errorf("failed to set executable permission on installer: %w", err)
	}

	// The download is not trusted until its bytes are verified against the
	// checksum published alongside it in the same release: a corrupted-but-200
	// download, a MITM'd release asset, or a compromised release process would
	// otherwise be written to disk, marked executable, and handed straight to
	// Update() with nothing having looked at its contents. A missing checksum
	// asset fails closed rather than falling back to running the binary
	// unverified.
	checksumAsset, err := findChecksumAsset(release, asset)
	if err != nil {
		return "", fmt.Errorf("failed to verify installer for %s: %w", asset.Name, err)
	}
	expectedDigest, err := u.fetchChecksumDigest(ctx, checksumAsset)
	if err != nil {
		return "", fmt.Errorf("failed to fetch checksum for %s: %w", asset.Name, err)
	}
	actualDigest := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actualDigest, expectedDigest) {
		return "", fmt.Errorf(
			"checksum mismatch for %s: expected %s, got %s",
			asset.Name, expectedDigest, actualDigest,
		)
	}

	success = true
	return file.Name(), nil
}

// findChecksumAsset returns the checksum sidecar asset published alongside
// asset in the same release (see checksumAssetSuffix). It fails closed —
// callers must not fall back to installing asset unverified — when a release
// omits the sidecar for whatever asset it does publish.
func findChecksumAsset(release Release, asset Asset) (Asset, error) {
	want := asset.Name + checksumAssetSuffix
	for _, a := range release.Assets {
		if a.Name == want {
			return a, nil
		}
	}
	return Asset{}, fmt.Errorf("no checksum asset named %s found in release", want)
}

// fetchChecksumDigest downloads and parses the SHA-256 checksum sidecar
// published by .github/workflows/sign.yml, whose content is the PowerShell
// `Get-FileHash | Format-List` rendering:
//
//	Algorithm : SHA256
//	Hash      : <64 hex chars>
//	Path      : <path on the runner that produced it>
//
// Only the Hash field is meaningful here; Algorithm and Path are not
// validated since the file name convention already ties the sidecar to a
// specific asset and algorithm.
func (u *defaultUpdater) fetchChecksumDigest(
	ctx context.Context,
	checksumAsset Asset,
) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumAsset.Url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Add("Accept", "application/octet-stream")
	if u.githubToken != "" {
		req.Header.Set("Authorization", "Bearer "+u.githubToken)
	}

	resp, err := u.downloadClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksum download failed with status code: %d", resp.StatusCode)
	}

	content, err := io.ReadAll(io.LimitReader(resp.Body, maxChecksumFileSize+1))
	if err != nil {
		return "", err
	}
	if int64(len(content)) > maxChecksumFileSize {
		return "", fmt.Errorf(
			"checksum file exceeds maximum allowed size of %d bytes", maxChecksumFileSize,
		)
	}

	return parseSha256ChecksumHash(content)
}

// parseSha256ChecksumHash extracts the Hash field from a
// `Get-FileHash | Format-List` rendering (see fetchChecksumDigest) and
// returns it lowercased for a case-insensitive comparison against the
// lowercase hex hex.EncodeToString produces.
func parseSha256ChecksumHash(content []byte) (string, error) {
	for _, line := range strings.Split(string(content), "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(key) != "Hash" {
			continue
		}

		hash := strings.ToLower(strings.TrimSpace(value))
		if len(hash) != sha256.Size*2 {
			continue
		}
		if _, err := hex.DecodeString(hash); err != nil {
			continue
		}

		return hash, nil
	}

	return "", fmt.Errorf("no valid SHA-256 hash found in checksum file")
}

func (u *defaultUpdater) Run(ctx context.Context) error {
	latestRelease, err := u.Check(ctx)
	if err != nil {
		return err
	}

	u.logger.Info("Latest release", "tag_name", latestRelease.TagName)

	isNewer, err := isNewerVersion(version.Version, latestRelease.TagName)
	if err != nil {
		return fmt.Errorf("failed to compare current version %q against latest release %q: %w",
			version.Version, latestRelease.TagName, err)
	}
	if !isNewer {
		u.logger.Info("No updates available")
		return nil
	}

	u.logger.Info("Updating agent", "version", latestRelease.TagName)

	applicableAsset, err := u.SelectAsset(latestRelease)
	if err != nil {
		return err
	}

	executablePath, err := u.Download(ctx, latestRelease, applicableAsset)
	if err != nil {
		return err
	}

	return u.Update(executablePath)
}

// isNewerVersion reports whether latest is a semantically newer version than
// current. Both are expected in the tag_format .cz.toml produces
// ("vMAJOR.MINOR.PATCH", optionally with a prerelease/build suffix that is
// ignored for comparison purposes). Comparing tags for inequality rather than
// "newer than" let a release process mistake — republishing an older tag as
// the one the check endpoint returns, or a test/staging feed pointed at a
// stale release — silently downgrade the whole fleet; comparing for equality
// with no ordering at all would do the same for a genuinely older tag. An
// unparseable version is reported as an error instead of guessed at, so a
// malformed tag can neither be silently skipped nor silently treated as newer.
func isNewerVersion(current, latest string) (bool, error) {
	c, err := parseSemver(current)
	if err != nil {
		return false, fmt.Errorf("invalid current version %q: %w", current, err)
	}
	l, err := parseSemver(latest)
	if err != nil {
		return false, fmt.Errorf("invalid latest version %q: %w", latest, err)
	}

	for i := range c {
		if l[i] != c[i] {
			return l[i] > c[i], nil
		}
	}
	return false, nil
}

// parseSemver parses the numeric MAJOR.MINOR.PATCH components of a version
// string, tolerating an optional leading "v" and discarding any
// prerelease/build metadata suffix (introduced by "-" or "+").
func parseSemver(v string) ([3]int, error) {
	var parts [3]int

	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}

	segments := strings.Split(v, ".")
	if len(segments) != 3 {
		return parts, fmt.Errorf("expected MAJOR.MINOR.PATCH, got %q", v)
	}

	for i, segment := range segments {
		n, err := strconv.Atoi(segment)
		if err != nil || n < 0 {
			return parts, fmt.Errorf("invalid numeric component %q", segment)
		}
		parts[i] = n
	}

	return parts, nil
}

type AutoUpdateRunner struct {
	logger      hclog.Logger
	updater     Updater
	interval    time.Duration
	maxRetries  int
	baseBackoff time.Duration
	// maxBackoff caps every retry slot; derived from the check interval at
	// construction (see updateMaxRetryBackoff).
	maxBackoff time.Duration
	ctx        context.Context
	cancel     context.CancelFunc
	stop       chan struct{}
	done       chan struct{}
}

func NewAutoUpdateRunner(
	logger hclog.Logger,
	updater Updater,
	interval time.Duration,
	maxRetries int,
	baseBackoff time.Duration,
) *AutoUpdateRunner {
	ctx, cancel := context.WithCancel(context.Background())
	return &AutoUpdateRunner{
		logger:      logger,
		updater:     updater,
		interval:    interval,
		maxRetries:  maxRetries,
		baseBackoff: baseBackoff,
		maxBackoff:  updateMaxRetryBackoff(interval),
		ctx:         ctx,
		cancel:      cancel,
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
}

func (r *AutoUpdateRunner) Start() {
	r.logger.Info("Starting auto updater", "version", version.Version, "interval", r.interval)

	go func() {
		defer close(r.done)

		timer := time.NewTimer(r.interval)
		defer timer.Stop()

		for {
			select {
			case <-r.stop:
				r.logger.Info("Auto updater stopped")
				return
			case <-timer.C:
				if err := r.runUpdate(); err != nil {
					r.logger.Error("Update failed, starting retry backoff", "error", err)
					if r.retryWithBackoff() {
						// A stop observed inside the backoff wait exits here rather
						// than through the select above, so it is logged here too:
						// otherwise the one path where a stop has to interrupt a
						// pending wait - the path the bounded, jittered schedule
						// exists for - would be the only one that leaves no record
						// of the updater shutting down.
						r.logger.Info("Auto updater stopped")
						return
					}
				}
				timer.Reset(r.interval)
			}
		}
	}()
}

// runUpdate invokes the updater's Run with panic recovery so a fault on the
// update path (malformed release JSON, a library or RPC panic, an unexpected
// nil) is recovered and logged with a stack trace instead of crashing the
// agent. A recovered panic is surfaced as an error so the normal failure path
// (retry backoff, then resume on schedule) continues unchanged.
func (r *AutoUpdateRunner) runUpdate() (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = utils.LogRecoveredPanic(r.logger, rec, "scope", "auto_update_tick")
		}
	}()

	return r.updater.Run(r.ctx)
}

func (r *AutoUpdateRunner) Stop() {
	// Cancel the context first so any in-flight update check or download HTTP
	// request is aborted promptly instead of blocking on the client Timeout,
	// then signal the run loop to exit and wait for it to finish.
	r.cancel()
	close(r.stop)
	<-r.done
}

// retryBackoff returns the delay to wait before the given zero-based retry
// attempt: the exponential schedule base * 2^attempt, clamped to the runner's
// cap (see DefaultUpdateMaxRetryBackoff) and spread with up to ±25% jitter. The
// result is always strictly positive and never exceeds the cap, for any attempt
// number, so the wait can never fire immediately or outlive the check interval.
func (r *AutoUpdateRunner) retryBackoff(attempt int) time.Duration {
	base := r.baseBackoff
	if base <= 0 {
		base = defaultBaseBackoff
	}
	maxBackoff := r.maxBackoff
	if maxBackoff <= 0 {
		maxBackoff = updateMaxRetryBackoff(r.interval)
	}

	return utils.JitteredBackoff(base, maxBackoff, attempt)
}

// retryWithBackoff re-runs a failed update on an exponential schedule, returning
// true when the runner was stopped mid-backoff so the caller exits its loop.
//
// Each slot is computed by utils.JitteredBackoff and is therefore bounded by
// r.maxBackoff (see DefaultUpdateMaxRetryBackoff) and spread with up to ±25%
// jitter. The bound keeps the doubling from overflowing into a negative duration
// that would make the wait return immediately and busy-spin against the release
// endpoint; the jitter keeps a whole fleet that failed against the same
// unavailable or rate-limiting endpoint from retrying in lockstep and sustaining
// the outage it is recovering from. The wait remains interruptible by r.stop, so
// a service stop is never delayed by a long backoff.
func (r *AutoUpdateRunner) retryWithBackoff() bool {
	for attempt := range r.maxRetries {
		backoff := r.retryBackoff(attempt)
		r.logger.Info("Retrying update", "attempt", attempt+1, "backoff", backoff)

		select {
		case <-r.stop:
			return true
		case <-time.After(backoff):
		}

		if err := r.runUpdate(); err != nil {
			r.logger.Error("Retry failed", "attempt", attempt+1, "error", err)
			continue
		}

		r.logger.Info("Update succeeded on retry", "attempt", attempt+1)
		return false
	}

	r.logger.Error("All retries exhausted")
	return false
}
