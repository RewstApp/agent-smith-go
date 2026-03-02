//go:build windows

package agent

import (
	"fmt"
	"strings"
)

func (u *defaultUpdater) SelectAsset(release Release) (Asset, error) {
	for _, asset := range release.Assets {
		if strings.HasSuffix(asset.Name, ".win.exe") {
			return asset, nil
		}
	}

	return Asset{}, fmt.Errorf("no asset found for windows")
}
