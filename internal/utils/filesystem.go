package utils

import (
	"os"
	"path/filepath"
)

func BaseDirectory() (string, error) {
	// Get the path of the current executable
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}

	// Get the directory from the executable path
	return filepath.Dir(exePath), nil
}

func DirExists(path string) bool {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false
	}
	return info.IsDir()
}
