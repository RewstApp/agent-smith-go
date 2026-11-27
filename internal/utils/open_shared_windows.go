//go:build windows

package utils

import (
	"os"

	"golang.org/x/sys/windows"
)

// OpenSharedRead opens name for reading with FILE_SHARE_DELETE, which Go's
// os.Open omits. On Windows a file cannot be renamed while a handle without
// that flag is open on it, so the log compressor (sc-132996) reading a rotated
// copy would otherwise make the next rotation's shift of that copy fail.
func OpenSharedRead(name string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	h, err := windows.CreateFile(
		p,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return os.NewFile(uintptr(h), name), nil
}
