//go:build !windows

package main

import "os"

// defaultBundleHandOver on Linux and macOS gives the bundle to the user behind
// sudo (sc-132995); see sudoHandOver.
func defaultBundleHandOver(path string) (string, error) {
	return sudoHandOver(os.Getenv, os.Chown)(path)
}
