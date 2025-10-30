package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/version"
	"github.com/hashicorp/go-hclog"
)

const latestReleaseUrl = "https://api.github.com/repos/rewstapp/agent-smith-go/releases/latest"
const updateInterval = 48 * time.Hour

type asset struct {
	Id   int    `json:"id"`
	Name string `json:"name"`
	Url  string `json:"url"`
}

type release struct {
	Id      int     `json:"id"`
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

func autoUpdate(logger hclog.Logger, device Device) {
	logger.Info("Checking for updates")

	resp, err := http.Get(latestReleaseUrl)
	if err != nil {
		logger.Error("Failed to fetch latest release", "url", latestReleaseUrl, "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Error("Failed to fetch latest release", "url", latestReleaseUrl, "status", resp.StatusCode)
		return
	}

	var release release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		logger.Error("Failed to parse release", "error", err)
		return
	}

	logger.Info("Latest release", "tag_name", release.TagName)

	if release.TagName == version.Version {
		logger.Info("No updates available")
		return
	}

	applicableAsset := findAsset(release.Assets)
	if applicableAsset == nil {
		logger.Error("No applicable asset", "os", runtime.GOOS)
		return
	}

	logger.Info("Updating agent", "version", release.TagName)

	err = update(logger, *applicableAsset, device)
	if err != nil {
		logger.Error("Failed to update agent", "error", err)
		return
	}
}

func findAsset(assets []asset) *asset {
	for _, asset := range assets {
		if runtime.GOOS == "windows" && strings.HasSuffix(asset.Name, ".win.exe") {
			return &asset
		}

		if runtime.GOOS == "linux" && strings.HasSuffix(asset.Name, ".linux.bin") {
			return &asset
		}

		if runtime.GOOS == "darwin" && strings.HasSuffix(asset.Name, ".mac-os.bin") {
			return &asset
		}
	}

	return nil
}

func download(asset asset) (string, error) {
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

func update(logger hclog.Logger, asset asset, device Device) error {
	path, err := download(asset)
	if err != nil {
		return err
	}

	args := []string{"--org-id", device.RewstOrgId, "--update", "--logging-level", string(device.LoggingLevel)}

	if device.UseSyslog {
		args = append(args, "--syslog")
	}

	if device.DisableAgentPostback {
		args = append(args, "--disable-agent-postback")
	}

	if device.DisableAutoUpdates {
		args = append(args, "--no-auto-updates")
	}

	logger.Debug("Running update commmand", "path", path, "args", args)

	cmd := exec.Command(path, args...)
	err = cmd.Run()

	return err
}

func RunAutoUpdater(logger hclog.Logger, device Device) {
	logger.Info("Running auto updater", "version", version.Version)

	ticker := time.NewTicker(updateInterval)
	defer ticker.Stop()

	for range ticker.C {
		autoUpdate(logger, device)
	}
}
