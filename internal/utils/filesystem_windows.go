//go:build windows

package utils

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"syscall"
)

// errSharingViolation is ERROR_SHARING_VIOLATION. Windows maps a running image
// to it: the loader keeps the executable open denying write sharing, so opening
// it for writing fails with this error for exactly as long as the process lives.
// It is the same error that made an in-place update fail after the fixed five
// second sleep, used here as the signal to wait on instead of the symptom.
const errSharingViolation = syscall.Errno(32)

// executableInUse probes whether name is currently running. A successful open
// means no process holds the image and the file can be replaced; a sharing
// violation means the old process is still alive.
//
// The file is opened without O_TRUNC and closed immediately, so a successful
// probe leaves its contents untouched.
func executableInUse(name string) (bool, error) {
	file, err := os.OpenFile(name, os.O_WRONLY, 0)
	if err == nil {
		return false, file.Close()
	}

	if errors.Is(err, os.ErrNotExist) {
		// Nothing installed at that path yet: no process can be holding it.
		return false, nil
	}

	if errors.Is(err, errSharingViolation) {
		return true, nil
	}

	return false, err
}

// EnsureSecureDir creates path if it does not exist. Windows has no POSIX
// mode/ownership bits to re-assert here, so an existing entry is only checked
// for being a plain directory; a symlink or non-directory entry at path is
// refused rather than followed or replaced, since MkdirAll never produces
// one. This is deliberately the only check on Windows: the command scripts
// directory (agent.GetScriptsDirectory) stays at its historical location on
// this platform rather than moving under the agent-owned data directory the
// way it did on Linux/macOS — see the comment on that function for why — so
// there is no ProgramData-inherited ACL to lean on here the way there would
// be if it had moved.
func EnsureSecureDir(path string) error {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(path, SecureDirMode); err != nil {
			return fmt.Errorf("failed to create secure directory %s: %w", path, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to stat %s: %w", path, err)
	}

	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("refusing to use %s: exists and is not a plain directory", path)
	}

	return nil
}

// EnsureSecureFile locks path down to SYSTEM, Administrators, and the
// account this process is currently running as, the file-level counterpart
// to SecureDataDirectoryACL. It exists for the device config file
// specifically (sc-108849): ProgramData's default ACL grants ordinary
// authenticated Users read access, which is exactly the exposure this
// closes, and an installation that pre-dates this hardening needs its
// already-written config.json corrected in place, not just files written
// after the fix.
//
// A path that does not exist is not an error — nothing has been written yet.
// A symlink or reparse point is refused rather than followed, since
// icacls operates on whatever the link resolves to.
func EnsureSecureFile(path string) error {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to stat %s: %w", path, err)
	}

	if fi.Mode()&os.ModeSymlink != 0 || fi.IsDir() {
		return fmt.Errorf("refusing to use %s: exists and is not a plain file", path)
	}

	return applyOwnerOnlyACL(path, "", false)
}

// windowsSystemSID and windowsAdministratorsSID are the well-known SIDs for
// the SYSTEM account and the built-in Administrators group. Well-known SIDs
// are used instead of the localized "SYSTEM"/"Administrators" account names
// so this works regardless of the OS display language.
const (
	windowsSystemSID         = "*S-1-5-18"
	windowsAdministratorsSID = "*S-1-5-32-544"
)

// buildOwnerOnlyGrants returns the icacls /grant:r arguments that restrict an
// object to SYSTEM, Administrators, the account this process is currently
// running as, and extraAccount when set. Including the current process's own
// account — whichever it is, SYSTEM or a custom service account configured
// via ServiceUsername — is what keeps this self-healing: whoever calls this
// function is, by construction, an identity that must still be able to read
// the file afterwards.
func buildOwnerOnlyGrants(extraAccount string, isDir bool) []string {
	perm := "F"
	if isDir {
		perm = "(OI)(CI)F"
	}

	grants := []string{
		windowsSystemSID + ":" + perm,
		windowsAdministratorsSID + ":" + perm,
	}
	seen := map[string]bool{windowsSystemSID: true, windowsAdministratorsSID: true}

	if u, err := user.Current(); err == nil && u.Uid != "" {
		sid := "*" + u.Uid
		if !seen[sid] {
			seen[sid] = true
			grants = append(grants, sid+":"+perm)
		}
	}

	if extraAccount != "" && !seen[extraAccount] {
		grants = append(grants, extraAccount+":"+perm)
	}

	return grants
}

// applyOwnerOnlyACL shells out to icacls to strip inherited access from path
// and grant only the accounts buildOwnerOnlyGrants selects. /inheritance:r
// removes every inherited entry — including the ProgramData default that
// grants ordinary Users read access — before /grant:r applies the
// replacement list. When recursive is true, /T /C additionally reapplies the
// same reset to every file already inside the directory (config.json, the
// log file, spooled postbacks) so an installation from before this hardening
// is corrected in place rather than only protecting files written after it;
// /C lets the recursive pass continue past a single file it cannot touch
// (e.g. one held open) instead of aborting the whole operation.
//
// icacls is a stock component of every supported Windows release, so
// shelling out to it (rather than hand-rolling security-descriptor
// construction) keeps this both readable and consistent with the well-known
// SIDs used above.
func applyOwnerOnlyACL(path string, extraAccount string, recursive bool) error {
	// recursive is only ever true for the data directory itself: the (OI)(CI)
	// inheritance flags it implies are what let the grant apply to files
	// created under it later, and they only mean something on a container.
	grants := buildOwnerOnlyGrants(extraAccount, recursive)

	args := append([]string{path, "/inheritance:r", "/grant:r"}, grants...)
	if recursive {
		args = append(args, "/T", "/C")
	}

	cmd := exec.Command("icacls", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"icacls failed to secure %s: %w (output: %s)",
			path, err, strings.TrimSpace(string(output)),
		)
	}

	return nil
}

// SecureDataDirectoryACL restricts path — the org's data directory, which
// holds config.json and its Azure IoT Hub SharedAccessKey and GitHub token
// (sc-108849) — to SYSTEM, Administrators, the account this process runs as,
// and extraAccount when set. Pass the configured service account
// (ServiceUsername) as extraAccount at install/update time so a service
// that runs as a different account than the one performing the
// install/update is not locked out of its own config file; service startup
// itself needs no extraAccount, since by then the process already runs as
// whichever account was configured.
//
// This is deliberately separate from EnsureSecureDir: that function is also
// used for the command scripts directory (agent.GetScriptsDirectory), and
// leaving its Windows behaviour untouched keeps this credential-exposure fix
// from carrying any risk of regressing command execution.
//
// Failure is returned to the caller rather than treated as fatal here:
// callers use this as a best-effort migration step so a transient icacls
// failure cannot take an otherwise-healthy agent down.
func SecureDataDirectoryACL(path string, extraAccount string) error {
	return applyOwnerOnlyACL(path, extraAccount, true)
}
