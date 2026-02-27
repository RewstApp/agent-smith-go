package utils

import (
	"os"
)

const DefaultFileMod os.FileMode = 0644
const DefaultExecutableFileMod os.FileMode = 0755
const DefaultDirMod os.FileMode = 0755

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

type FileSystem interface {
	Executable() (string, error)
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
}

type defaultFileSystem struct{}

func (*defaultFileSystem) Executable() (string, error) {
	return os.Executable()
}

func (*defaultFileSystem) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func (*defaultFileSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}

func NewFileSystem() FileSystem {
	return &defaultFileSystem{}
}
