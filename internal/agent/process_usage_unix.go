//go:build !windows

package agent

import (
	"context"
	"os"

	"github.com/shirou/gopsutil/v4/process"
)

const descriptorName = "open file"

// countDescriptors counts this process's open descriptors from /dev/fd, which
// Linux and macOS both serve for the calling process (gopsutil has no
// descriptor count on macOS). Listing the directory opens one descriptor of
// its own, which the listing includes and the count leaves out. The names are
// read without stat'ing them: os.ReadDir stats each entry on macOS, and the
// entry for the listing's own descriptor fails that stat once the listing has
// moved on ("lstat /dev/fd/4: bad file descriptor", the macos-latest runner).
func countDescriptors(_ context.Context, _ *process.Process) (int32, error) {
	dir, err := os.Open("/dev/fd")
	if err != nil {
		return 0, err
	}
	names, err := dir.Readdirnames(-1)
	_ = dir.Close()
	if err != nil {
		return 0, err
	}
	n := len(names) - 1
	if n < 0 {
		n = 0
	}
	return int32(n), nil // #nosec G115 - a descriptor count fits
}
