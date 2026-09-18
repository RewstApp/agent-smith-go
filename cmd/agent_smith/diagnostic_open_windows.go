//go:build windows

package main

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// Open opens the log file for reading with FILE_SHARE_DELETE, which Go's
// os.Open omits (its share mode is FILE_SHARE_READ|FILE_SHARE_WRITE only). On
// Windows a file cannot be renamed while any handle lacking that flag is open
// on it, so a viewer opened with os.Open would block every log rotation the
// agent attempted for as long as the viewer ran - re-creating, for the length
// of a support session, exactly the unbounded growth rotation exists to stop.
// With the flag the agent's rename succeeds, the viewer's handle keeps reading
// the (now renamed) old file, and logRotated - which compares file identity by
// volume and file index, not by path or size - sees that the file at the log
// path is a different one and the viewer follows it.
func (o *osLogFileOpener) Open(name string) (io.ReadCloser, error) {
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
