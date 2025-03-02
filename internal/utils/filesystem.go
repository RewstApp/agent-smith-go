package utils

import (
	"os"
)

var DefaultFileMod os.FileMode = 0644
var DefaultDirMod os.FileMode = 0755

func DirExists(path string) bool {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false
	}
	return info.IsDir()
}

func CreateFolderIfMissing(dir string) error {
	if !DirExists(dir) {
		err := os.MkdirAll(dir, DefaultDirMod)
		if err != nil {
			return err
		}
	}

	return nil
}
