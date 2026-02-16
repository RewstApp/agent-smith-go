package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

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
	Check() (Release, error)
	Update(updaterExecutablePath string) error
	SelectAsset(release Release) (Asset, error)
	Download(asset Asset) (string, error)
	Run() error
}

type RunCommandFunc = func(path string, args []string) error

type defaultUpdater struct {
	logger           hclog.Logger
	device           *Device
	latestReleaseUrl string
	runCommand       RunCommandFunc
}

func NewUpdater(logger hclog.Logger, device *Device, latestReleaseUrl string, runCommand RunCommandFunc) Updater {
	return &defaultUpdater{
		logger:           logger,
		device:           device,
		latestReleaseUrl: latestReleaseUrl,
		runCommand:       runCommand,
	}
}

func (u *defaultUpdater) Check() (Release, error) {
	release := Release{}
	u.logger.Info("Checking for updates")

	resp, err := http.Get(u.latestReleaseUrl)
	if err != nil {
		u.logger.Error("Failed to fetch latest release", "url", u.latestReleaseUrl, "error", err)
		return release, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		u.logger.Error("Failed to fetch latest release", "url", u.latestReleaseUrl, "status", resp.StatusCode)
		return release, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		u.logger.Error("Failed to parse release", "error", err)
		return release, err
	}

	return release, nil
}

func (u *defaultUpdater) Update(updaterExecutablePath string) error {
	args := []string{"--org-id", u.device.RewstOrgId, "--update", "--logging-level", string(u.device.LoggingLevel)}

	if u.device.UseSyslog {
		args = append(args, "--syslog")
	}

	if u.device.DisableAgentPostback {
		args = append(args, "--disable-agent-postback")
	}

	if u.device.DisableAutoUpdates {
		args = append(args, "--no-auto-updates")
	}

	u.logger.Debug("Running update commmand", "path", updaterExecutablePath, "args", args)

	return u.runCommand(updaterExecutablePath, args)
}

func (u *defaultUpdater) Download(asset Asset) (string, error) {
	req, err := http.NewRequest(http.MethodGet, asset.Url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Add("Accept", "application/octet-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status code: %d", resp.StatusCode)
	}

	file, err := os.CreateTemp("", "installer-*.bin")
	if err != nil {
		return "", err
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return "", err
	}

	return file.Name(), nil
}

func (u *defaultUpdater) Run() error {
	latestRelease, err := u.Check()
	if err != nil {
		return err
	}

	u.logger.Info("Latest release", "tag_name", latestRelease.TagName)

	if latestRelease.TagName == version.Version {
		u.logger.Info("No updates available")
		return nil
	}

	u.logger.Info("Updating agent", "version", latestRelease.TagName)

	applicableAsset, err := u.SelectAsset(latestRelease)
	if err != nil {
		return err
	}

	executablePath, err := u.Download(applicableAsset)
	if err != nil {
		return err
	}

	return u.Update(executablePath)
}

type AutoUpdateRunner struct {
	logger      hclog.Logger
	updater     Updater
	interval    time.Duration
	maxRetries  int
	baseBackoff time.Duration
	stop        chan struct{}
	done        chan struct{}
}

func NewAutoUpdateRunner(logger hclog.Logger, updater Updater, interval time.Duration, maxRetries int, baseBackoff time.Duration) *AutoUpdateRunner {
	return &AutoUpdateRunner{
		logger:      logger,
		updater:     updater,
		interval:    interval,
		maxRetries:  maxRetries,
		baseBackoff: baseBackoff,
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
				if err := r.updater.Run(); err != nil {
					r.logger.Error("Update failed, starting retry backoff", "error", err)
					if r.retryWithBackoff() {
						return
					}
				}
				timer.Reset(r.interval)
			}
		}
	}()
}

func (r *AutoUpdateRunner) Stop() {
	close(r.stop)
	<-r.done
}

func (r *AutoUpdateRunner) retryWithBackoff() bool {
	for attempt := range r.maxRetries {
		backoff := r.baseBackoff * (1 << attempt)
		r.logger.Info("Retrying update", "attempt", attempt+1, "backoff", backoff)

		select {
		case <-r.stop:
			return true
		case <-time.After(backoff):
		}

		if err := r.updater.Run(); err != nil {
			r.logger.Error("Retry failed", "attempt", attempt+1, "error", err)
			continue
		}

		r.logger.Info("Update succeeded on retry", "attempt", attempt+1)
		return false
	}

	r.logger.Error("All retries exhausted")
	return false
}
