//go:build !windows

package utils

import "os"

// OpenSharedRead opens name for reading. On Unix renaming a file another
// descriptor has open is always permitted, so this is os.Open.
func OpenSharedRead(name string) (*os.File, error) {
	return os.Open(name) // #nosec G304 - the agent's own log copies
}
