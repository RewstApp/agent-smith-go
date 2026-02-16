//go:build linux

package agent

import (
	"fmt"
	"strings"
)

func (u *defaultUpdater) SelectAsset(release Release) (Asset, error) {
	for _, asset := range release.Assets {
		if strings.HasSuffix(asset.Name, ".linux.bin") {
			return asset, nil
		}
	}

	return Asset{}, fmt.Errorf("No asset found for linux")
}
