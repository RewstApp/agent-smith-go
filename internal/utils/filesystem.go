package utils

import (
	"os"
)

const DefaultFileMod os.FileMode = 0644
const DefaultExecutableFileMod os.FileMode = 0755
const DefaultDirMod os.FileMode = 0755

type FileSystem interface {
	Executable() (string, error)
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	MkdirAll(path string) error
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

func (*defaultFileSystem) MkdirAll(path string) error {
	return os.MkdirAll(path, DefaultDirMod)
}

func NewFileSystem() FileSystem {
	return &defaultFileSystem{}
}
