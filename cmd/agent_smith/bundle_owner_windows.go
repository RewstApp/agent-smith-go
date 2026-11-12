//go:build windows

package main

import (
	"fmt"
	"os/user"
	"unsafe"

	"github.com/RewstApp/agent-smith-go/internal/utils"
	"golang.org/x/sys/windows"
)

// The bundle is written by an elevated process, so it is readable by
// administrators; the technician who approved the elevation may be sitting in
// a standard-user console session that cannot open it (sc-132995). The
// Windows hand-over grants the console session's user read access on the
// finished file through icacls (utils.GrantReadAccess, the file-level sibling
// of the install path's SecureDataDirectoryACL). With no console session - a
// headless RMM run as SYSTEM - the process's own account is named instead,
// which is a no-op grant that keeps the closing line truthful.

var (
	modwtsapi32                     = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSQuerySessionInformationW = modwtsapi32.NewProc("WTSQuerySessionInformationW")
)

const (
	// wtsCurrentServerHandle is WTS_CURRENT_SERVER_HANDLE: this machine.
	wtsCurrentServerHandle = 0
	// wtsUserName and wtsDomainName are the WTS_INFO_CLASS values.
	wtsUserName   = 5
	wtsDomainName = 7
	// noConsoleSession is what WTSGetActiveConsoleSessionId returns when no
	// one is logged on at the console.
	noConsoleSession = 0xFFFFFFFF
)

func defaultBundleHandOver(path string) (string, error) {
	account, err := interactiveUser()
	if err != nil {
		return "", err
	}
	source := "the console session's user"
	if account == "" {
		current, err := user.Current()
		if err != nil {
			return "", fmt.Errorf("no console session and the current user is unknown: %w", err)
		}
		account, source = current.Username, "this process's user, no one is at the console"
	}
	if err := utils.GrantReadAccess(path, account); err != nil {
		return "", err
	}
	return fmt.Sprintf("read granted to %s, %s", account, source), nil
}

// interactiveUser is DOMAIN\\user for the active console session, or empty
// when there is no session or no one is logged on to it.
func interactiveUser() (string, error) {
	session := windows.WTSGetActiveConsoleSessionId()
	if session == noConsoleSession {
		return "", nil
	}
	name, err := querySession(session, wtsUserName)
	if err != nil || name == "" {
		return "", err
	}
	domain, err := querySession(session, wtsDomainName)
	if err != nil || domain == "" {
		return name, err
	}
	return domain + `\` + name, nil
}

func querySession(session, class uint32) (string, error) {
	var (
		buf  *uint16
		size uint32
	)
	r1, _, e1 := procWTSQuerySessionInformationW.Call(
		wtsCurrentServerHandle,
		uintptr(session),
		uintptr(class),
		uintptr(unsafe.Pointer(&buf)),
		uintptr(unsafe.Pointer(&size)),
	)
	if r1 == 0 {
		return "", fmt.Errorf("WTSQuerySessionInformation(class %d) failed: %w", class, e1)
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(buf)))
	return windows.UTF16PtrToString(buf), nil
}
