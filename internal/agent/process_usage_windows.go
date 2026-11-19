//go:build windows

package agent

import (
	"context"

	"github.com/shirou/gopsutil/v4/process"
)

const descriptorName = "handle"

// countDescriptors is this process's handle count (GetProcessHandleCount,
// which gopsutil reports as its descriptor count on Windows).
func countDescriptors(ctx context.Context, self *process.Process) (int32, error) {
	return self.NumFDsWithContext(ctx)
}
