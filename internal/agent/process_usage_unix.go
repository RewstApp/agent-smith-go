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
// its own, which the listing includes and the count leaves out.
func countDescriptors(_ context.Context, _ *process.Process) (int32, error) {
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		return 0, err
	}
	n := len(entries) - 1
	if n < 0 {
		n = 0
	}
	return int32(n), nil // #nosec G115 - a descriptor count fits
}
